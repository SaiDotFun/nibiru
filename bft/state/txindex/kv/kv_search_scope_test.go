package kv

import (
	"context"
	"fmt"
	"testing"

	db "github.com/cometbft/cometbft-db"
	"github.com/stretchr/testify/require"

	abci "github.com/NibiruChain/nibiru/v2/bft/abci/types"
	"github.com/NibiruChain/nibiru/v2/bft/libs/pubsub/query"
	"github.com/NibiruChain/nibiru/v2/bft/types"
)

func TestSearchByTransaction(t *testing.T) {
	idx := NewTxIndex(db.NewMemDB())
	old := txResultWithEvents([]abci.Event{
		{Type: "message", Attributes: []abci.EventAttribute{{Key: "action", Value: "/old.type", Index: true}}},
	})
	current := txResultWithEvents([]abci.Event{
		{Type: "message", Attributes: []abci.EventAttribute{{Key: "action", Value: "/bank.send", Index: true}}},
		{Type: "transfer", Attributes: []abci.EventAttribute{
			{Key: "sender", Value: "alice", Index: true},
			{Key: "amount", Value: "1", Index: true},
		}},
		{Type: "transfer", Attributes: []abci.EventAttribute{
			{Key: "sender", Value: "bob", Index: true},
			{Key: "amount", Value: "9", Index: true},
		}},
	})
	current.Height++
	require.NoError(t, idx.Index(old))
	require.NoError(t, idx.Index(current))
	other := txResultWithEvents([]abci.Event{
		{Type: "transfer", Attributes: []abci.EventAttribute{{Key: "sender", Value: "carol", Index: true}}},
	})
	other.Tx = []byte("different transaction")
	other.Height = current.Height
	other.Index++
	require.NoError(t, idx.Index(other))
	hash := fmt.Sprintf("tx.hash = '%X'", types.Tx(current.Tx).Hash())

	for _, scope := range []struct {
		name    string
		search  func(context.Context, *query.Query) ([]*abci.TxResult, error)
		crosses bool
	}{
		{"default event", idx.Search, false},
		{"transaction", idx.SearchByTransaction, true},
	} {
		t.Run(scope.name, func(t *testing.T) {
			for _, test := range []struct {
				predicates string
				matches    bool
			}{
				{"message.action = '/bank.send' AND transfer.amount = 9", scope.crosses},
				{"transfer.sender = 'alice' AND transfer.amount >= 9", scope.crosses},
				{"transfer.sender = 'bob' AND transfer.amount >= 9", true},
				{"message.action = '/old.type' AND transfer.amount = 9", false},
				{"message.action = '/bank.send' AND transfer.sender = 'carol'", false},
			} {
				for _, suffix := range []string{"", " AND " + hash} {
					results, err := scope.search(t.Context(), query.MustParse(test.predicates+suffix))
					require.NoError(t, err)
					if test.matches {
						require.Equal(t, []*abci.TxResult{current}, results, test.predicates+suffix)
					} else {
						require.Empty(t, results, test.predicates+suffix)
					}
				}
			}
		})
	}
}

func TestSearchByTransactionLiteralAttributes(t *testing.T) {
	idx := NewTxIndex(db.NewMemDB())
	tx := txResultWithEvents([]abci.Event{
		{Type: "memo", Attributes: []abci.EventAttribute{
			{Key: "label", Value: "desk/sai", Index: true},
			{Key: "label", Value: "desk/9", Index: true},
			{Key: "path", Value: "/root", Index: true},
			{Key: "empty", Value: "", Index: true},
			{Key: "note", Value: "  desk ", Index: true},
			{Key: "count", Value: " 7 ", Index: true},
		}},
	})
	require.NoError(t, idx.Index(tx))

	for _, test := range []struct {
		predicates string
		matches    bool
	}{
		{"memo.label = 'desk'", false},
		{"memo.label = 'desk/sai'", true},
		{"memo.label = 'desk/9'", true},
		{"memo.label/desk > 1", false},
		{"memo.label CONTAINS '/sai'", true},
		{"memo.path = ''", false},
		{"memo.empty = ''", true},
		{"memo.note CONTAINS ' desk '", true},
		{"memo.note = '  desk '", true},
		{"memo.note = 'desk'", false},
		{"memo.count > 6", true},
		{"memo.count = 7", false},
	} {
		t.Run(test.predicates, func(t *testing.T) {
			for _, suffix := range []string{"", fmt.Sprintf(" AND tx.height = %d", tx.Height)} {
				results, err := idx.SearchByTransaction(t.Context(), query.MustParse(test.predicates+suffix))
				require.NoError(t, err)
				if test.matches {
					require.Equal(t, []*abci.TxResult{tx}, results)
				} else {
					require.Empty(t, results)
				}
			}
		})
	}
}

func TestSearchByTransactionTypedComparisons(t *testing.T) {
	idx := NewTxIndex(db.NewMemDB())
	tx := txResultWithEvents([]abci.Event{
		{Type: "memo", Attributes: []abci.EventAttribute{
			{Key: "price", Value: "0.20000000000000000001", Index: true},
			{Key: "price", Value: "0.01", Index: true},
			{Key: "date", Value: "2026-11-25", Index: true},
			{Key: "time", Value: "2026-11-25T19:00:00-05:00", Index: true},
		}},
		{Type: "block", Attributes: []abci.EventAttribute{
			{Key: "height", Value: "8", Index: true},
		}},
	})
	require.NoError(t, idx.Index(tx))

	for _, test := range []struct {
		predicates string
		matches    bool
	}{
		{"memo.price > 0.20 AND memo.price < 1", true},
		{"memo.price = 0.200000000000000000010", true},
		{"memo.price = 0.20", false},
		{"memo.price > 0.1 AND memo.price < 0.2", false},
		{"memo.price = '0.200000000000000000010'", false},
		{"memo.date = DATE 2026-11-25", true},
		{"memo.date >= DATE 2026-11-24 AND memo.date < DATE 2026-11-26", true},
		{"memo.time = TIME 2026-11-26T00:00:00Z", true},
		{"memo.time < TIME 2026-11-26T00:00:00Z", false},
		{"block.height >= 8 AND block.height < 9", true},
		{"block.height = 8 AND tx.height < 8", true},
		{"block.height >= 8 AND tx.height >= 8", false},
	} {
		t.Run(test.predicates, func(t *testing.T) {
			results, err := idx.SearchByTransaction(t.Context(), query.MustParse(test.predicates))
			require.NoError(t, err)
			if test.matches {
				require.Equal(t, []*abci.TxResult{tx}, results)
			} else {
				require.Empty(t, results)
			}
		})
	}

	absentHash := fmt.Sprintf("tx.hash = '%X'", types.Tx("absent").Hash())
	mixed := "memo.price > 0.1 AND memo.price < DATE 2026-11-25 AND " + absentHash
	_, err := idx.SearchByTransaction(t.Context(), query.MustParse(mixed))
	require.ErrorContains(t, err, "cannot combine numeric and temporal comparisons")
}
