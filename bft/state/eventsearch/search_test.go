package eventsearch

import (
	"errors"
	"fmt"
	"testing"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/stretchr/testify/require"

	abci "github.com/NibiruChain/nibiru/v2/bft/abci/types"
	tmstate "github.com/NibiruChain/nibiru/v2/bft/proto/tendermint/state"
	"github.com/NibiruChain/nibiru/v2/bft/types"
)

func TestSearchCommittedPhasesInOrder(t *testing.T) {
	store := New(dbm.NewMemDB())
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	const height = 7
	tx := types.Tx("committed tx")
	block := &types.Block{Header: types.Header{Height: height}, Data: types.Data{Txs: types.Txs{tx}}}

	event := abci.Event{
		Type: "wasm.test",
		Attributes: []abci.EventAttribute{
			{Key: "action", Value: "transfer"},
			{Key: "label.part", Value: `"\u0061"`, Index: true},
			{Key: "label.part", Value: "other"},
		},
	}
	results := &tmstate.ABCIResponses{
		BeginBlock: &abci.ResponseBeginBlock{Events: []abci.Event{event}},
		DeliverTxs: []*abci.ResponseDeliverTx{{Events: []abci.Event{event, event}}},
		EndBlock:   &abci.ResponseEndBlock{Events: []abci.Event{event}},
	}
	require.NoError(t, store.IndexBlock(height, block, results))
	require.NoError(t, store.IndexBlock(height, block, results))

	filter := Filter{
		Type: "wasm.test",
		Attributes: []AttributeFilter{
			{Key: "action", Eq: valuePtr("transfer")},
			{Key: "label.part", JSONStringEq: valuePtr("a")},
			{Key: "label.part", Eq: valuePtr("other")},
		},
	}
	first, err := store.Search(t.Context(), filter, "", 2)
	require.NoError(t, err)
	require.Len(t, first.Events, 2)
	require.Equal(t, []HeightRange{{height, height}}, first.Coverage)
	require.NotEmpty(t, first.NextCursor)
	require.Equal(t, PhaseBeginBlock, first.Events[0].Phase)
	require.Nil(t, first.Events[0].TxIndex)
	require.Empty(t, first.Events[0].TxHash)
	require.Equal(t, uint32(0), first.Events[0].EventIndex)
	require.Equal(t, event.Attributes, first.Events[0].Attributes)
	require.Equal(t, PhaseTx, first.Events[1].Phase)
	require.Equal(t, uint32(0), *first.Events[1].TxIndex)
	require.Equal(t, uint32(0), first.Events[1].EventIndex)
	require.Equal(t, fmt.Sprintf("%X", tx.Hash()), first.Events[1].TxHash)

	second, err := store.Search(t.Context(), filter, first.NextCursor, 2)
	require.NoError(t, err)
	require.Len(t, second.Events, 2)
	require.Empty(t, second.NextCursor)
	require.Equal(t, PhaseTx, second.Events[0].Phase)
	require.Equal(t, uint32(1), second.Events[0].EventIndex)
	require.Equal(t, PhaseEndBlock, second.Events[1].Phase)
	require.Nil(t, second.Events[1].TxIndex)
	require.Empty(t, second.Events[1].TxHash)

	page, err := store.Search(t.Context(), Filter{
		Phases:     []string{PhaseEndBlock},
		Attributes: []AttributeFilter{{Key: "label.part", Exists: true}},
	}, "", 0)
	require.NoError(t, err)
	require.Equal(t, []Event{second.Events[1]}, page.Events)

	page, err = store.Search(t.Context(), Filter{
		Attributes: []AttributeFilter{{Key: "label.part", Eq: valuePtr("a")}},
	}, "", 0)
	require.NoError(t, err)
	require.Empty(t, page.Events)
	require.Equal(t, []HeightRange{{height, height}}, page.Coverage)
}

func TestSearchCoverageSurvivesRestartAndPinsCursor(t *testing.T) {
	db := dbm.NewMemDB()
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	store := New(db)
	results := &tmstate.ABCIResponses{
		DeliverTxs: []*abci.ResponseDeliverTx{{Events: []abci.Event{{Type: "wasm"}}}},
	}
	require.NoError(t, store.IndexBlock(5, nil, results))
	require.NoError(t, store.IndexBlock(7, nil, results))

	store = New(db)
	filter := Filter{MinHeight: 4, Type: "wasm"}
	first, err := store.Search(t.Context(), filter, "", 1)
	require.NoError(t, err)
	require.Equal(t, []HeightRange{{5, 5}, {7, 7}}, first.Coverage)
	require.Len(t, first.Events, 1)
	require.Equal(t, int64(5), first.Events[0].Height)
	require.Equal(t, uint32(0), *first.Events[0].TxIndex)
	require.Empty(t, first.Events[0].TxHash)

	require.NoError(t, store.IndexBlock(8, nil, results))
	second, err := store.Search(t.Context(), filter, first.NextCursor, 1)
	require.NoError(t, err)
	require.Equal(t, first.Coverage, second.Coverage)
	require.Len(t, second.Events, 1)
	require.Equal(t, int64(7), second.Events[0].Height)
	require.Empty(t, second.NextCursor)

	// A backfilled height with no events still expands indexed coverage.
	require.NoError(t, store.IndexBlock(6, nil, &tmstate.ABCIResponses{}))
	_, err = store.Search(t.Context(), filter, first.NextCursor, 1)
	require.ErrorContains(t, err, "coverage changed")

	page, err := store.Search(t.Context(), Filter{MinHeight: 6, MaxHeight: 6}, "", 1)
	require.NoError(t, err)
	require.Equal(t, []HeightRange{{6, 6}}, page.Coverage)
	require.Empty(t, page.Events)
	page, err = store.Search(t.Context(), Filter{MinHeight: 4, MaxHeight: 4}, "", 1)
	require.NoError(t, err)
	require.Empty(t, page.Coverage)
	require.Empty(t, page.Events)
}

func TestSearchDoesNotCombineSeparateEvents(t *testing.T) {
	store := New(dbm.NewMemDB())
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	require.NoError(t, store.IndexBlock(1, nil, &tmstate.ABCIResponses{
		DeliverTxs: []*abci.ResponseDeliverTx{{Events: []abci.Event{
			{Type: "transfer", Attributes: []abci.EventAttribute{{Key: "sender", Value: "Alice"}}},
			{Type: "transfer", Attributes: []abci.EventAttribute{{Key: "recipient", Value: "Dave"}}},
		}}},
	}))
	page, err := store.Search(t.Context(), Filter{
		Type: "transfer",
		Attributes: []AttributeFilter{
			{Key: "sender", Eq: valuePtr("Alice")},
			{Key: "recipient", Eq: valuePtr("Dave")},
		},
	}, "", 1)
	require.NoError(t, err)
	require.Empty(t, page.Events)
}

func TestIndexBlockRetriesAfterBatchFailure(t *testing.T) {
	db := &failingBatchDB{DB: dbm.NewMemDB(), failWrites: true}
	store := New(db)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	results := &tmstate.ABCIResponses{
		EndBlock: &abci.ResponseEndBlock{Events: []abci.Event{{Type: "wasm"}}},
	}

	require.ErrorIs(t, store.IndexBlock(1, nil, results), errTestBatch)
	page, err := store.Search(t.Context(), Filter{}, "", 1)
	require.NoError(t, err)
	require.Empty(t, page.Events)
	require.Empty(t, page.Coverage)

	db.failWrites = false
	require.NoError(t, store.IndexBlock(1, nil, results))
	page, err = store.Search(t.Context(), Filter{}, "", 1)
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	require.Equal(t, []HeightRange{{1, 1}}, page.Coverage)
}

func TestIndexBlockRejectsTransactionPositionMismatch(t *testing.T) {
	store := New(dbm.NewMemDB())
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	block := &types.Block{
		Header: types.Header{Height: 1},
		Data:   types.Data{Txs: types.Txs{types.Tx("committed tx")}},
	}
	for _, responses := range [][]*abci.ResponseDeliverTx{nil, {nil}} {
		results := &tmstate.ABCIResponses{
			BeginBlock: &abci.ResponseBeginBlock{Events: []abci.Event{{Type: "wasm"}}},
			DeliverTxs: responses,
		}
		require.ErrorIs(t, store.IndexBlock(1, block, results), ErrPositionMismatch)
		coverage, err := store.Coverage()
		require.NoError(t, err)
		require.Empty(t, coverage)
	}

	require.NoError(t, store.IndexBlock(1, block, &tmstate.ABCIResponses{
		DeliverTxs: []*abci.ResponseDeliverTx{{Events: []abci.Event{{Type: "wasm"}}}},
	}))
	page, err := store.Search(t.Context(), Filter{}, "", 1)
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	require.Equal(t, fmt.Sprintf("%X", block.Data.Txs[0].Hash()), page.Events[0].TxHash)
}

var errTestBatch = errors.New("injected batch error")

type failingBatchDB struct {
	dbm.DB
	failWrites bool
}

func (db *failingBatchDB) NewBatch() dbm.Batch {
	batch := db.DB.NewBatch()
	if !db.failWrites {
		return batch
	}
	return failingBatch{batch}
}

type failingBatch struct{ dbm.Batch }

func (failingBatch) WriteSync() error { return errTestBatch }

func TestSearchRejectsInvalidInput(t *testing.T) {
	store := New(dbm.NewMemDB())
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	for _, filter := range []Filter{
		{MinHeight: -1},
		{MinHeight: 2, MaxHeight: 1},
		{Phases: []string{"block"}},
		{Attributes: []AttributeFilter{{}}},
		{Attributes: []AttributeFilter{{Key: "missing-operator"}}},
		{Attributes: []AttributeFilter{{Key: "mixed-operators", Eq: valuePtr("a"), Exists: true}}},
	} {
		_, err := store.Search(t.Context(), filter, "", 1)
		require.Error(t, err)
	}
	_, err := store.Search(t.Context(), Filter{}, "", maxLimit+1)
	require.ErrorContains(t, err, "limit")
	_, err = store.Search(t.Context(), Filter{}, "malformed", 1)
	require.ErrorContains(t, err, "cursor")
	require.NoError(t, store.Close())
	_, err = store.Search(t.Context(), Filter{}, "", 1)
	require.ErrorIs(t, err, errClosed)
}

func valuePtr(s string) *string { return &s }
