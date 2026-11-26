package kv_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	db "github.com/cometbft/cometbft-db"

	abci "github.com/NibiruChain/nibiru/v2/bft/abci/types"
	"github.com/NibiruChain/nibiru/v2/bft/libs/pubsub/query"
	blockidxkv "github.com/NibiruChain/nibiru/v2/bft/state/indexer/block/kv"
	"github.com/NibiruChain/nibiru/v2/bft/types"
)

func TestBlockIndexer(t *testing.T) {
	store := db.NewPrefixDB(db.NewMemDB(), []byte("block_events"))
	indexer := blockidxkv.New(store)

	require.NoError(t, indexer.Index(types.EventDataNewBlockHeader{
		Header: types.Header{Height: 1},
		ResultBeginBlock: abci.ResponseBeginBlock{
			Events: []abci.Event{
				{
					Type: "begin_event",
					Attributes: []abci.EventAttribute{
						{
							Key:   "proposer",
							Value: "FCAA001",
							Index: true,
						},
					},
				},
			},
		},
		ResultEndBlock: abci.ResponseEndBlock{
			Events: []abci.Event{
				{
					Type: "end_event",
					Attributes: []abci.EventAttribute{
						{
							Key:   "foo",
							Value: "100",
							Index: true,
						},
					},
				},
			},
		},
	}))

	for i := 2; i < 12; i++ {
		var index bool
		if i%2 == 0 {
			index = true
		}

		require.NoError(t, indexer.Index(types.EventDataNewBlockHeader{
			Header: types.Header{Height: int64(i)},
			ResultBeginBlock: abci.ResponseBeginBlock{
				Events: []abci.Event{
					{
						Type: "begin_event",
						Attributes: []abci.EventAttribute{
							{
								Key:   "proposer",
								Value: "FCAA001",
								Index: true,
							},
						},
					},
				},
			},
			ResultEndBlock: abci.ResponseEndBlock{
				Events: []abci.Event{
					{
						Type: "end_event",
						Attributes: []abci.EventAttribute{
							{
								Key:   "foo",
								Value: fmt.Sprintf("%d", i),
								Index: index,
							},
						},
					},
				},
			},
		}))
	}

	testCases := map[string]struct {
		q       *query.Query
		results []int64
	}{
		"block.height = 100": {
			q:       query.MustParse("block.height = 100"),
			results: []int64{},
		},
		"block.height = 5": {
			q:       query.MustParse("block.height = 5"),
			results: []int64{5},
		},
		"begin_event.key1 = 'value1'": {
			q:       query.MustParse("begin_event.key1 = 'value1'"),
			results: []int64{},
		},
		"begin_event.proposer = 'FCAA001'": {
			q:       query.MustParse("begin_event.proposer = 'FCAA001'"),
			results: []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11},
		},
		"repeated lower bounds": {
			q:       query.MustParse("end_event.foo >= 100 AND end_event.foo >= 10"),
			results: []int64{1},
		},
		"strict and inclusive upper bounds": {
			q:       query.MustParse("end_event.foo < 4 AND end_event.foo <= 4"),
			results: []int64{2},
		},
		"end_event.foo <= 5": {
			q:       query.MustParse("end_event.foo <= 5"),
			results: []int64{2, 4},
		},
		"end_event.foo >= 100": {
			q:       query.MustParse("end_event.foo >= 100"),
			results: []int64{1},
		},
		"block.height > 2 AND end_event.foo <= 8": {
			q:       query.MustParse("block.height > 2 AND end_event.foo <= 8"),
			results: []int64{4, 6, 8},
		},
		"end_event.foo > 100": {
			q:       query.MustParse("end_event.foo > 100"),
			results: []int64{},
		},
		"block.height >= 2 AND end_event.foo < 8": {
			q:       query.MustParse("block.height >= 2 AND end_event.foo < 8"),
			results: []int64{2, 4, 6},
		},
		"begin_event.proposer CONTAINS 'FFFFFFF'": {
			q:       query.MustParse("begin_event.proposer CONTAINS 'FFFFFFF'"),
			results: []int64{},
		},
		"begin_event.proposer CONTAINS 'FCAA001'": {
			q:       query.MustParse("begin_event.proposer CONTAINS 'FCAA001'"),
			results: []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11},
		},
		"end_event.foo CONTAINS '1'": {
			q:       query.MustParse("end_event.foo CONTAINS '1'"),
			results: []int64{1, 10},
		},
	}

	for name, tc := range testCases {
		tc := tc
		t.Run(name, func(t *testing.T) {
			results, err := indexer.Search(context.Background(), tc.q)
			require.NoError(t, err)
			require.Equal(t, tc.results, results)
		})
	}
}

func TestBlockIndexerMulti(t *testing.T) {
	store := db.NewPrefixDB(db.NewMemDB(), []byte("block_events"))
	indexer := blockidxkv.New(store)

	require.NoError(t, indexer.Index(types.EventDataNewBlockHeader{
		Header: types.Header{Height: 1},
		ResultBeginBlock: abci.ResponseBeginBlock{
			Events: []abci.Event{},
		},
		ResultEndBlock: abci.ResponseEndBlock{
			Events: []abci.Event{
				{
					Type: "end_event",
					Attributes: []abci.EventAttribute{
						{
							Key:   "foo",
							Value: "100",
							Index: true,
						},
						{
							Key:   "bar",
							Value: "200",
							Index: true,
						},
					},
				},
				{
					Type: "end_event",
					Attributes: []abci.EventAttribute{
						{
							Key:   "foo",
							Value: "300",
							Index: true,
						},
						{
							Key:   "bar",
							Value: "500",
							Index: true,
						},
					},
				},
			},
		},
	}))

	require.NoError(t, indexer.Index(types.EventDataNewBlockHeader{
		Header: types.Header{Height: 2},
		ResultBeginBlock: abci.ResponseBeginBlock{
			Events: []abci.Event{},
		},
		ResultEndBlock: abci.ResponseEndBlock{
			Events: []abci.Event{
				{
					Type: "end_event",
					Attributes: []abci.EventAttribute{
						{
							Key:   "foo",
							Value: "100",
							Index: true,
						},
						{
							Key:   "bar",
							Value: "200",
							Index: true,
						},
					},
				},
				{
					Type: "end_event",
					Attributes: []abci.EventAttribute{
						{
							Key:   "foo",
							Value: "300",
							Index: true,
						},
						{
							Key:   "bar",
							Value: "400",
							Index: true,
						},
					},
				},
			},
		},
	}))

	testCases := map[string]struct {
		q       *query.Query
		results []int64
	}{

		"query return all events from a height - exact": {
			q:       query.MustParse("block.height = 1"),
			results: []int64{1},
		},
		"different height equalities return no blocks": {
			q:       query.MustParse("block.height = 1 AND block.height = 2"),
			results: []int64{},
		},
		"query return all events from a height - range": {
			q:       query.MustParse("block.height < 2 AND block.height > 0 AND block.height > 0"),
			results: []int64{1},
		},
		"query return all events from a height - range 2": {
			q:       query.MustParse("block.height < 3 AND block.height < 2 AND block.height > 0 AND block.height > 0"),
			results: []int64{1},
		},
		"query return all events from a height - range 3": {
			q:       query.MustParse("block.height < 1 AND block.height > 1"),
			results: []int64{},
		},
		"query matches fields from same event": {
			q:       query.MustParse("end_event.bar < 300 AND end_event.foo = 100 AND block.height > 0 AND block.height <= 2"),
			results: []int64{1, 2},
		},
		"query matches fields from multiple events": {
			q:       query.MustParse("end_event.foo = 100 AND end_event.bar = 400 AND block.height = 2"),
			results: []int64{},
		},
		"query matches fields from multiple events 2": {
			q:       query.MustParse("end_event.foo = 100 AND end_event.bar > 200 AND block.height > 0 AND block.height < 3"),
			results: []int64{},
		},
		"query matches fields from multiple events allowed": {
			q:       query.MustParse("end_event.foo = 100 AND end_event.bar = 400"),
			results: []int64{},
		},
		"query matches fields from all events whose attribute is within range": {
			q:       query.MustParse("block.height  = 2 AND end_event.foo < 300"),
			results: []int64{2},
		},
		"deduplication test - match.events multiple 2": {
			q:       query.MustParse("end_event.foo = 100 AND end_event.bar = 400 AND block.height = 2"),
			results: []int64{},
		},
		"query using CONTAINS matches fields from all events whose attribute is within range": {
			q:       query.MustParse("block.height  = 2 AND end_event.foo CONTAINS '30'"),
			results: []int64{2},
		},
		"query matches all fields from multiple events": {
			q:       query.MustParse("end_event.bar > 100 AND end_event.bar <= 500"),
			results: []int64{1, 2},
		},
		"contradictory height equality and range return no blocks": {
			q:       query.MustParse("block.height = 2 AND end_event.foo >= 100 AND block.height < 2"),
			results: []int64{},
		},
		"query with non-existent field": {
			q:       query.MustParse("end_event.bar = 100"),
			results: []int64{},
		},
	}

	for name, tc := range testCases {
		tc := tc
		t.Run(name, func(t *testing.T) {
			results, err := indexer.Search(context.Background(), tc.q)
			require.NoError(t, err)
			require.Equal(t, tc.results, results)
		})
	}
}

func TestBigInt(t *testing.T) {

	bigInt := "10000000000000000000"
	store := db.NewPrefixDB(db.NewMemDB(), []byte("block_events"))
	indexer := blockidxkv.New(store)

	require.NoError(t, indexer.Index(types.EventDataNewBlockHeader{
		Header: types.Header{Height: 1},
		ResultBeginBlock: abci.ResponseBeginBlock{
			Events: []abci.Event{},
		},
		ResultEndBlock: abci.ResponseEndBlock{
			Events: []abci.Event{
				{
					Type: "end_event",
					Attributes: []abci.EventAttribute{
						{
							Key:   "foo",
							Value: "100",
							Index: true,
						},
						{
							Key:   "bar",
							Value: "10000000000000000000.76",
							Index: true,
						},
						{
							Key:   "bar_lower",
							Value: "10000000000000000000.1",
							Index: true,
						},
					},
				},
				{
					Type: "end_event",
					Attributes: []abci.EventAttribute{
						{
							Key:   "foo",
							Value: bigInt,
							Index: true,
						},
						{
							Key:   "bar",
							Value: "500",
							Index: true,
						},
						{
							Key:   "bla",
							Value: "500.5",
							Index: true,
						},
					},
				},
			},
		},
	}))

	testCases := map[string]struct {
		q       *query.Query
		results []int64
	}{

		"query return all events from a height - exact": {
			q:       query.MustParse("block.height = 1"),
			results: []int64{1},
		},
		"different height equalities return no blocks": {
			q:       query.MustParse("block.height = 1 AND block.height = 2"),
			results: []int64{},
		},
		"query return all events from a height - range": {
			q:       query.MustParse("block.height < 2 AND block.height > 0 AND block.height > 0"),
			results: []int64{1},
		},
		"query matches fields with big int and height - no match": {
			q:       query.MustParse("end_event.foo = " + bigInt + " AND end_event.bar = 500 AND block.height = 2"),
			results: []int64{},
		},
		"query matches fields with big int with less and height - no match": {
			q:       query.MustParse("end_event.foo <= " + bigInt + " AND end_event.bar = 500 AND block.height = 2"),
			results: []int64{},
		},
		"query matches fields with big int and height - match": {
			q:       query.MustParse("end_event.foo = " + bigInt + " AND end_event.bar = 500 AND block.height = 1"),
			results: []int64{1},
		},
		"query matches big int in range": {
			q:       query.MustParse("end_event.foo = " + bigInt),
			results: []int64{1},
		},
		"query matches big int in range with float - does not pass as float is not converted to int": {
			q:       query.MustParse("end_event.bar >= " + bigInt),
			results: []int64{},
		},
		"query matches big int in range with float - fails because float is converted to int": {
			q:       query.MustParse("end_event.bar > " + bigInt),
			results: []int64{},
		},
		"query matches big int in range with float lower dec point - fails because float is converted to int": {
			q:       query.MustParse("end_event.bar_lower > " + bigInt),
			results: []int64{},
		},
		"query matches big int in range with float with less - found": {
			q:       query.MustParse("end_event.foo <= " + bigInt),
			results: []int64{1},
		},
		"query matches big int in range with float with less with height range - found": {
			q:       query.MustParse("end_event.foo <= " + bigInt + " AND block.height > 0"),
			results: []int64{1},
		},
		"query matches big int in range with float with less - not found": {
			q:       query.MustParse("end_event.foo < " + bigInt + " AND end_event.foo > 100"),
			results: []int64{},
		},
		"query does not parse float": {
			q:       query.MustParse("end_event.bla >= 500"),
			results: []int64{},
		},
	}

	for name, tc := range testCases {
		tc := tc
		t.Run(name, func(t *testing.T) {
			results, err := indexer.Search(context.Background(), tc.q)
			require.NoError(t, err)
			require.Equal(t, tc.results, results)
		})
	}
}

func TestBlockHeightPredicates(t *testing.T) {
	indexer := blockidxkv.New(db.NewMemDB())
	for _, height := range []int64{7, 8, 9223372036854775807} {
		require.NoError(t, indexer.Index(types.EventDataNewBlockHeader{
			Header: types.Header{Height: height},
			ResultBeginBlock: abci.ResponseBeginBlock{
				Events: []abci.Event{
					{Type: "tx", Attributes: []abci.EventAttribute{{Key: "height", Value: "1", Index: true}}},
				},
			},
		}))
	}

	for _, tc := range []struct {
		query   string
		heights []int64
	}{
		{"block.height = 7 AND block.height = 7", []int64{7}},
		{"block.height = 8 AND block.height = 7", []int64{}},
		{"block.height = 7 AND block.height >= 8", []int64{}},
		{"block.height >= 8 AND block.height = 7", []int64{}},
		{"block.height > 6 AND block.height = 7", []int64{7}},
		{"block.height = 7 AND block.height >= 7 AND tx.height >= 1", []int64{7}},
		{"tx.height >= 1 AND block.height = 7 AND block.height >= 7", []int64{7}},
		{"block.height = 0 AND tx.height >= 1", []int64{}},
		{"block.height = 9223372036854775807", []int64{9223372036854775807}},
		{"block.height > 9223372036854775807", []int64{}},
		{"block.height > 9223372036854775808", []int64{}},
		{"block.height = 9223372036854775808", []int64{}},
		{"block.height < 9223372036854775808", []int64{7, 8, 9223372036854775807}},
	} {
		t.Run(tc.query, func(t *testing.T) {
			actual, err := indexer.Search(context.Background(), query.MustParse(tc.query))
			require.NoError(t, err)
			require.Equal(t, tc.heights, actual)
		})
	}

	for _, predicate := range []string{
		"block.height = 7.0",
		"block.height = '7'",
		"block.height = 18446744073709551623",
		"block.height < TIME 2025-01-02T00:00:00Z",
	} {
		t.Run(predicate, func(t *testing.T) {
			_, err := indexer.Search(context.Background(), query.MustParse(predicate))
			require.ErrorContains(t, err, "block.height requires an integer operand")
		})
	}
}

func TestBlockHistoricalTypedPredicates(t *testing.T) {
	indexer := blockidxkv.New(db.NewMemDB())
	require.NoError(t, indexer.Index(types.EventDataNewBlockHeader{
		Header: types.Header{Height: 7},
		ResultBeginBlock: abci.ResponseBeginBlock{
			Events: []abci.Event{{
				Type: "sample",
				Attributes: []abci.EventAttribute{
					{Key: "value", Value: "9007199254740993.1", Index: true},
					{Key: "stamped", Value: "2025-01-02T01:30:00+01:00", Index: true},
					{Key: "effective", Value: "2025-01-02", Index: true},
				},
			}},
		},
		ResultEndBlock: abci.ResponseEndBlock{
			Events: []abci.Event{
				{Type: "sample", Attributes: []abci.EventAttribute{{Key: "value", Value: "2", Index: true}}},
				{Type: "sample", Attributes: []abci.EventAttribute{{Key: "source", Value: "end", Index: true}}},
			},
		},
	}))

	for _, tc := range []struct {
		query   string
		heights []int64
	}{
		{"sample.value = 9007199254740993.1", []int64{7}},
		{"sample.value = 9007199254740993.2", []int64{}},
		{"sample.value > 9007199254740993 AND sample.value < 9007199254740993.2", []int64{7}},
		{"sample.value < 9007199254740993.2 AND sample.value > 9007199254740993", []int64{7}},
		{"sample.value > 9007199254740993.2 AND sample.value <= 9007199254740993", []int64{}},
		{"sample.stamped = TIME 2025-01-02T00:30:00Z", []int64{7}},
		{"sample.effective = DATE 2025-01-02", []int64{7}},
		{"sample.effective >= DATE 2025-01-02 AND sample.stamped < TIME 2025-01-02T01:00:00Z", []int64{7}},
		{"sample.stamped > TIME 2025-01-02T00:00:00Z AND sample.source = 'end'", []int64{}},
		{"sample.value >= 3.0 AND sample.value < 7.0", []int64{}},
	} {
		t.Run(tc.query, func(t *testing.T) {
			actual, err := indexer.Search(context.Background(), query.MustParse(tc.query))
			require.NoError(t, err)
			require.Equal(t, tc.heights, actual)
		})
	}

	_, err := indexer.Search(context.Background(), query.MustParse("sample.value >= 1 AND sample.value < TIME 2025-01-02T00:00:00Z"))
	require.ErrorContains(t, err, "sample.value cannot combine numeric and temporal comparisons")
}
