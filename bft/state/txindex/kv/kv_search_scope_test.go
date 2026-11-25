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
