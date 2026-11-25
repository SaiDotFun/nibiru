package indexer

import (
	"math/big"
	"time"

	"github.com/NibiruChain/nibiru/v2/bft/libs/pubsub/query"
	"github.com/NibiruChain/nibiru/v2/bft/types"
)

// QueryRanges defines a mapping between a composite event key and a QueryRange.
//
// e.g.account.number => queryRange{lowerBound: 1, upperBound: 5}
type QueryRanges map[string]QueryRange

// QueryRange defines a range within a query condition.
type QueryRange struct {
	LowerBound        interface{} // int || time.Time
	UpperBound        interface{} // int || time.Time
	Key               string
	IncludeLowerBound bool
	IncludeUpperBound bool
}

// AnyBound returns either the lower bound if non-nil, otherwise the upper bound.
func (qr QueryRange) AnyBound() interface{} {
	if qr.LowerBound != nil {
		return qr.LowerBound
	}

	return qr.UpperBound
}

// LowerBoundValue returns the value for the lower bound. If the lower bound is
// nil, nil will be returned.
func (qr QueryRange) LowerBoundValue() interface{} {
	if qr.LowerBound == nil {
		return nil
	}

	if qr.IncludeLowerBound {
		return qr.LowerBound
	}

	switch t := qr.LowerBound.(type) {
	case int64:
		return t + 1
	case *big.Int:
		tmp := new(big.Int)
		return tmp.Add(t, big.NewInt(1))

	case time.Time:
		return t.Unix() + 1

	default:
		panic("not implemented")
	}
}

// UpperBoundValue returns the value for the upper bound. If the upper bound is
// nil, nil will be returned.
func (qr QueryRange) UpperBoundValue() interface{} {
	if qr.UpperBound == nil {
		return nil
	}

	if qr.IncludeUpperBound {
		return qr.UpperBound
	}

	switch t := qr.UpperBound.(type) {
	case int64:
		return t - 1
	case *big.Int:
		tmp := new(big.Int)
		return tmp.Sub(t, big.NewInt(1))
	case time.Time:
		return t.Unix() - 1

	default:
		panic("not implemented")
	}
}

// LookForRangesWithHeight returns a mapping of QueryRanges and the matching indexes in
// the provided query conditions.
func LookForRangesWithHeight(conditions []query.Condition) (queryRange QueryRanges, indexes []int, heightRange QueryRange) {
	queryRange = make(QueryRanges)
	for i, c := range conditions {
		if !IsRangeOperation(c.Op) {
			continue
		}
		r := queryRange[c.CompositeKey]
		r.Key = c.CompositeKey
		r.addBound(c)
		queryRange[c.CompositeKey] = r
		indexes = append(indexes, i)
		if c.CompositeKey == types.BlockHeightKey || c.CompositeKey == types.TxHeightKey {
			heightRange = r
		}
	}

	return queryRange, indexes, heightRange
}

// Deprecated: Use LookForRangesWithHeight.
func LookForRanges(conditions []query.Condition) (ranges QueryRanges, indexes []int) {
	ranges, indexes, _ = LookForRangesWithHeight(conditions)
	return ranges, indexes
}

// addBound keeps the tightest integer bound in each direction.
func (qr *QueryRange) addBound(condition query.Condition) {
	value := condition.Operand
	switch condition.Op {
	case query.OpGreater, query.OpGreaterEqual:
		inclusive := condition.Op == query.OpGreaterEqual
		order, comparable := compareIntegerBounds(value, qr.LowerBound)
		if !comparable || order > 0 || (order == 0 && !inclusive) {
			qr.LowerBound = value
			qr.IncludeLowerBound = inclusive
		}
	case query.OpLess, query.OpLessEqual:
		inclusive := condition.Op == query.OpLessEqual
		order, comparable := compareIntegerBounds(value, qr.UpperBound)
		if !comparable || order < 0 || (order == 0 && !inclusive) {
			qr.UpperBound = value
			qr.IncludeUpperBound = inclusive
		}
	}
}

func compareIntegerBounds(a, b any) (int, bool) {
	left, leftOK := a.(*big.Int)
	right, rightOK := b.(*big.Int)
	if !leftOK || !rightOK {
		return 0, false
	}
	return left.Cmp(right), true
}

// IsRangeOperation returns a boolean signifying if a query Operator is a range
// operation or not.
func IsRangeOperation(op query.Operator) bool {
	switch op {
	case query.OpGreater, query.OpGreaterEqual, query.OpLess, query.OpLessEqual:
		return true

	default:
		return false
	}
}
