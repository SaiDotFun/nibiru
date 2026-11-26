package kv

import (
	"bytes"
	"context"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"github.com/google/orderedcode"

	dbm "github.com/cometbft/cometbft-db"

	abci "github.com/NibiruChain/nibiru/v2/bft/abci/types"
	"github.com/NibiruChain/nibiru/v2/bft/libs/pubsub/query"
	"github.com/NibiruChain/nibiru/v2/bft/state/indexer"
	"github.com/NibiruChain/nibiru/v2/bft/types"
)

var _ indexer.BlockIndexer = (*BlockerIndexer)(nil)

// BlockerIndexer implements a block indexer, indexing BeginBlock and EndBlock
// events with an underlying KV store. Block events are indexed by their height,
// such that matching search criteria returns the respective block height(s).
type BlockerIndexer struct {
	store dbm.DB

	// Add unique event identifier to use when querying
	// Matching will be done both on height AND eventSeq
	eventSeq int64
}

func New(store dbm.DB) *BlockerIndexer {
	return &BlockerIndexer{
		store: store,
	}
}

// Has returns true if the given height has been indexed. An error is returned
// upon database query failure.
func (idx *BlockerIndexer) Has(height int64) (bool, error) {
	key, err := heightKey(height)
	if err != nil {
		return false, fmt.Errorf("failed to create block height index key: %w", err)
	}

	return idx.store.Has(key)
}

// Index indexes BeginBlock and EndBlock events for a given block by its height.
// The following is indexed:
//
// primary key: encode(block.height | height) => encode(height)
// BeginBlock events: encode(eventType.eventAttr|eventValue|height|begin_block) => encode(height)
// EndBlock events: encode(eventType.eventAttr|eventValue|height|end_block) => encode(height)
func (idx *BlockerIndexer) Index(bh types.EventDataNewBlockHeader) error {
	batch := idx.store.NewBatch()
	defer batch.Close()

	height := bh.Header.Height

	// 1. index by height
	key, err := heightKey(height)
	if err != nil {
		return fmt.Errorf("failed to create block height index key: %w", err)
	}
	if err := batch.Set(key, int64ToBytes(height)); err != nil {
		return err
	}

	// 2. index BeginBlock events
	if err := idx.indexEvents(batch, bh.ResultBeginBlock.Events, "begin_block", height); err != nil {
		return fmt.Errorf("failed to index BeginBlock events: %w", err)
	}

	// 3. index EndBlock events
	if err := idx.indexEvents(batch, bh.ResultEndBlock.Events, "end_block", height); err != nil {
		return fmt.Errorf("failed to index EndBlock events: %w", err)
	}

	return batch.WriteSync()
}

// Search returns indexed block heights whose block-level height predicates
// and event predicates all match. Event predicates match within one BeginBlock
// or EndBlock event.
func (idx *BlockerIndexer) Search(ctx context.Context, q *query.Query) ([]int64, error) {
	results := make([]int64, 0)
	if ctx.Err() != nil {
		return results, nil
	}

	conditions, err := q.ConditionsWithExactDecimals()
	if err != nil {
		return nil, fmt.Errorf("failed to parse query conditions: %w", err)
	}
	conditions, heightRange, err := indexer.ExtractHeightFilter(conditions, types.BlockHeightKey)
	if err != nil {
		return nil, err
	}
	for _, c := range conditions {
		if c.CompositeKey == types.BlockHeightKey {
			return nil, fmt.Errorf("%s requires an integer comparison", types.BlockHeightKey)
		}
	}
	ranges, rangeIndexes, err := indexer.CollectHistoricalRanges(conditions)
	if err != nil {
		return nil, err
	}

	lower, _ := heightRange.LowerBound.(*big.Int)
	upper, _ := heightRange.UpperBound.(*big.Int)
	// Exclusive bounds at the ends of the signed range can leave no valid heights.
	if (lower != nil && !lower.IsInt64()) || (upper != nil && !upper.IsInt64()) {
		return results, nil
	}
	if lower != nil && upper != nil && lower.Cmp(upper) > 0 {
		return results, nil
	}
	if len(conditions) == 0 && lower != nil && upper != nil && lower.Cmp(upper) == 0 {
		height := lower.Int64()
		found, err := idx.Has(height)
		if err != nil {
			return nil, err
		}
		if found {
			return []int64{height}, nil
		}
		return results, nil
	}

	var filteredHeights map[string][]byte
	if len(conditions) == 0 && heightRange.Key != "" {
		filteredHeights, err = idx.matchRange(ctx, types.BlockHeightKey, indexer.IntegerHistoricalRange(heightRange), nil, heightRange)
		if err != nil {
			return nil, err
		}
	}

	for key, eventRange := range ranges {
		filteredHeights, err = idx.matchRange(ctx, key, eventRange, filteredHeights, heightRange)
		if err != nil {
			return nil, err
		}
		if len(filteredHeights) == 0 {
			return results, nil
		}
	}

	for i, c := range conditions {
		if intInSlice(i, rangeIndexes) {
			continue
		}
		filteredHeights, err = idx.match(ctx, c, filteredHeights, heightRange)
		if err != nil {
			return nil, err
		}
		if len(filteredHeights) == 0 {
			return results, nil
		}
	}

	// fetch matching heights
	results = make([]int64, 0, len(filteredHeights))
	resultMap := make(map[int64]struct{})

FOR_LOOP:
	for _, hBz := range filteredHeights {
		h := int64FromBytes(hBz)

		ok, err := idx.Has(h)
		if err != nil {
			return nil, err
		}
		if ok {
			if _, ok := resultMap[h]; !ok {
				resultMap[h] = struct{}{}
				results = append(results, h)
			}
		}

		select {
		case <-ctx.Done():
			break FOR_LOOP

		default:
		}
	}

	sort.Slice(results, func(i, j int) bool { return results[i] < results[j] })

	return results, nil
}

func (idx *BlockerIndexer) matchRange(
	ctx context.Context,
	key string,
	eventRange indexer.HistoricalRange,
	filteredHeights map[string][]byte,
	heightRange indexer.QueryRange,
) (map[string][]byte, error) {
	prefix, err := orderedcode.Append(nil, key)
	if err != nil {
		return nil, fmt.Errorf("failed to create prefix key: %w", err)
	}
	return idx.matchPrefix(ctx, prefix, key == types.BlockHeightKey, eventRange.Match, filteredHeights, heightRange)
}

func (idx *BlockerIndexer) match(
	ctx context.Context,
	c query.Condition,
	filteredHeights map[string][]byte,
	heightRange indexer.QueryRange,
) (map[string][]byte, error) {
	var (
		prefix       []byte
		err          error
		matchesValue func(string) bool
	)
	switch c.Op {
	case query.OpEqual:
		prefix, err = orderedcode.Append(nil, c.CompositeKey, fmt.Sprintf("%v", c.Operand))
	case query.OpExists:
		prefix, err = orderedcode.Append(nil, c.CompositeKey)
	case query.OpContains:
		prefix, err = orderedcode.Append(nil, c.CompositeKey)
		operand := c.Operand.(string)
		matchesValue = func(value string) bool { return strings.Contains(value, operand) }
	default:
		return nil, fmt.Errorf("unsupported operator for block event %q", c.CompositeKey)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create prefix key: %w", err)
	}
	return idx.matchPrefix(ctx, prefix, false, matchesValue, filteredHeights, heightRange)
}

// matchPrefix retains event identities matched by this predicate and all earlier
// predicates. A nil map means this is the first predicate.
func (idx *BlockerIndexer) matchPrefix(
	ctx context.Context,
	prefix []byte,
	primary bool,
	matchesValue func(string) bool,
	filteredHeights map[string][]byte,
	heightRange indexer.QueryRange,
) (map[string][]byte, error) {
	it, err := dbm.IteratePrefix(idx.store, prefix)
	if err != nil {
		return nil, fmt.Errorf("failed to create prefix iterator: %w", err)
	}
	defer it.Close()

	matches := make(map[string][]byte)
	for ; it.Valid() && ctx.Err() == nil; it.Next() {
		var eventSeq int64
		height := int64FromBytes(it.Value())
		if !primary {
			if !checkHeightConditions(heightRange, height) {
				continue
			}
			eventSeq, err = parseEventSeqFromEventKey(it.Key())
			if err != nil {
				continue
			}
		}
		if matchesValue != nil {
			var value string
			if primary {
				value, err = parseValueFromPrimaryKey(it.Key())
			} else {
				value, err = parseValueFromEventKey(it.Key())
			}
			if err != nil || !matchesValue(value) {
				continue
			}
		}

		key := string(it.Value()) + strconv.FormatInt(eventSeq, 10)
		if filteredHeights != nil {
			if previous := filteredHeights[key]; bytes.Equal(previous, it.Value()) {
				matches[key] = previous
			}
			continue
		}

		// Copy the height because the iterator can reuse its value buffer.
		matches[key] = bytes.Clone(it.Value())
	}
	if err := it.Error(); err != nil {
		return nil, err
	}
	return matches, nil
}

func (idx *BlockerIndexer) indexEvents(batch dbm.Batch, events []abci.Event, typ string, height int64) error {
	heightBz := int64ToBytes(height)

	for _, event := range events {
		idx.eventSeq = idx.eventSeq + 1
		// only index events with a non-empty type
		if len(event.Type) == 0 {
			continue
		}

		for _, attr := range event.Attributes {
			if len(attr.Key) == 0 {
				continue
			}

			// index iff the event specified index:true and it's not a reserved event
			compositeKey := fmt.Sprintf("%s.%s", event.Type, attr.Key)
			if compositeKey == types.BlockHeightKey {
				return fmt.Errorf("event type and attribute key \"%s\" is reserved; please use a different key", compositeKey)
			}

			if attr.GetIndex() {
				key, err := eventKey(compositeKey, typ, attr.Value, height, idx.eventSeq)
				if err != nil {
					return fmt.Errorf("failed to create block index key: %w", err)
				}

				if err := batch.Set(key, heightBz); err != nil {
					return err
				}
			}
		}
	}

	return nil
}
