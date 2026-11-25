package kv

import (
	"fmt"
	"testing"

	db "github.com/cometbft/cometbft-db"
	"github.com/stretchr/testify/require"

	abci "github.com/NibiruChain/nibiru/v2/bft/abci/types"
	"github.com/NibiruChain/nibiru/v2/bft/libs/pubsub/query"
	"github.com/NibiruChain/nibiru/v2/bft/state/txindex"
	"github.com/NibiruChain/nibiru/v2/bft/types"
)

func TestSearchMatchesReceiptPosition(t *testing.T) {
	for _, test := range []struct {
		name  string
		write func(*testing.T, *TxIndex, *abci.TxResult, *abci.TxResult)
	}{
		{"different heights", func(t *testing.T, idx *TxIndex, first, second *abci.TxResult) {
			second.Height++
			require.NoError(t, idx.Index(first))
			require.NoError(t, idx.Index(second))
		}},
		{"different indexes in a batch", func(t *testing.T, idx *TxIndex, first, second *abci.TxResult) {
			second.Index++
			batch := txindex.NewBatch(2)
			require.NoError(t, batch.Add(first))
			require.NoError(t, batch.Add(second))
			require.NoError(t, idx.AddBatch(batch))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := db.NewMemDB()
			idx := NewTxIndex(store)
			first := txResultWithEvents([]abci.Event{
				{Type: "transfer", Attributes: []abci.EventAttribute{
					{Key: "route", Value: "/old/route", Index: true},
					{Key: "amount", Value: "1", Index: true},
				}},
			})
			second := txResultWithEvents([]abci.Event{
				{Type: "transfer", Attributes: []abci.EventAttribute{
					{Key: "route", Value: "/new/route", Index: true},
					{Key: "amount", Value: "2", Index: true},
				}},
				{Type: "transfer", Attributes: []abci.EventAttribute{
					{Key: "other", Value: "x", Index: true},
					{Key: "amount", Value: "3", Index: true},
				}},
			})
			test.write(t, idx, first, second)
			hash := types.Tx(first.Tx).Hash()
			hashPredicate := fmt.Sprintf("tx.hash = '%X'", hash)

			// Older nodes did not append an event sequence to the key.
			for _, legacy := range []struct {
				name   string
				result *abci.TxResult
			}{
				{"old", first},
				{"new", second},
			} {
				key := fmt.Sprintf("legacy.%s/yes/%d/%d", legacy.name, legacy.result.Height, legacy.result.Index)
				require.NoError(t, store.Set([]byte(key), hash))
			}

			for _, predicates := range []string{
				"transfer.route = '/old/route'",
				"transfer.route CONTAINS '/old/'",
				"transfer.amount <= 1",
				"transfer.route = '/old/route' AND transfer.amount = 1",
				"transfer.route = '/new/route' AND transfer.amount = 3",
				"legacy.old EXISTS",
				"legacy.old EXISTS AND legacy.new EXISTS",
			} {
				for _, suffix := range []string{"", " AND " + hashPredicate} {
					results, err := idx.Search(t.Context(), query.MustParse(predicates+suffix))
					require.NoError(t, err)
					require.Empty(t, results, predicates+suffix)
				}
			}
			if first.Height != second.Height {
				for _, predicate := range []string{
					fmt.Sprintf("tx.height = %d", first.Height),
					fmt.Sprintf("tx.height <= %d", first.Height),
				} {
					results, err := idx.Search(t.Context(), query.MustParse(predicate))
					require.NoError(t, err)
					require.Empty(t, results, predicate)
				}
			}

			for _, predicate := range []string{
				hashPredicate,
				"transfer.route = '/new/route' AND transfer.amount = 2",
				"transfer.amount EXISTS",
				"legacy.new EXISTS",
				fmt.Sprintf("tx.height = %d", second.Height),
			} {
				results, err := idx.Search(t.Context(), query.MustParse(predicate))
				require.NoError(t, err)
				require.Equal(t, []*abci.TxResult{second}, results, predicate)
			}
		})
	}
}

func TestSearchSkipsMissingReceipt(t *testing.T) {
	store := db.NewMemDB()
	idx := NewTxIndex(store)
	result := txResultWithEvents(nil)
	hash := types.Tx(result.Tx).Hash()
	require.NoError(t, idx.Index(result))
	require.NoError(t, store.Delete(hash))

	results, err := idx.Search(t.Context(), query.MustParse(fmt.Sprintf("tx.height = %d", result.Height)))
	require.NoError(t, err)
	require.Empty(t, results)
}
