package kv

import (
	"fmt"
	"math/big"

	"github.com/NibiruChain/nibiru/v2/bft/libs/pubsub/query"
	"github.com/NibiruChain/nibiru/v2/bft/state/indexer"
	"github.com/NibiruChain/nibiru/v2/bft/types"
	"github.com/google/orderedcode"
)

type HeightInfo struct {
	heightRange indexer.QueryRange
	height      int64
}

// IntInSlice returns true if a is found in the list.
func intInSlice(a int, list []int) bool {
	for _, b := range list {
		if b == a {
			return true
		}
	}
	return false
}

func ParseEventSeqFromEventKey(key []byte) (int64, error) {
	var (
		compositeKey, typ, eventValue string
		height                        int64
		eventSeq                      int64
	)

	remaining, err := orderedcode.Parse(string(key), &compositeKey, &eventValue, &height, &typ, &eventSeq)
	if err != nil {
		return 0, fmt.Errorf("failed to parse event key: %w", err)
	}

	if len(remaining) != 0 {
		return 0, fmt.Errorf("unexpected remainder in key: %s", remaining)
	}

	return eventSeq, nil
}

// extractHeightFilter intersects the numeric height predicates before event matching.
// Height bounds are inclusive because transaction heights are integers.
func extractHeightFilter(conditions []query.Condition) ([]query.Condition, indexer.QueryRange, error) {
	remaining := conditions[:0]
	heightRange := indexer.QueryRange{
		IncludeLowerBound: true,
		IncludeUpperBound: true,
	}
	var lower, upper *big.Int
	for _, c := range conditions {
		if c.CompositeKey != types.TxHeightKey || (c.Op != query.OpEqual && !indexer.IsRangeOperation(c.Op)) {
			remaining = append(remaining, c)
			continue
		}
		height, ok := c.Operand.(*big.Int)
		if !ok {
			return nil, indexer.QueryRange{}, fmt.Errorf("tx.height requires an integer operand")
		}
		heightRange.Key = types.TxHeightKey

		switch c.Op {
		case query.OpGreater:
			height = new(big.Int).Add(height, big.NewInt(1))
		case query.OpLess:
			height = new(big.Int).Sub(height, big.NewInt(1))
		}
		if c.Op == query.OpEqual || c.Op == query.OpGreater || c.Op == query.OpGreaterEqual {
			if lower == nil || height.Cmp(lower) > 0 {
				lower = height
			}
		}
		if c.Op == query.OpEqual || c.Op == query.OpLess || c.Op == query.OpLessEqual {
			if upper == nil || height.Cmp(upper) < 0 {
				upper = height
			}
		}
	}
	if lower != nil {
		heightRange.LowerBound = lower
	}
	if upper != nil {
		heightRange.UpperBound = upper
	}
	return remaining, heightRange, nil
}

func checkHeightConditions(heightInfo HeightInfo, keyHeight int64) bool {
	if heightInfo.height > 0 {
		return keyHeight == heightInfo.height
	}
	return heightInfo.heightRange.Key == "" || checkBounds(heightInfo.heightRange, big.NewInt(keyHeight))
}
