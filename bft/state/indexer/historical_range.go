package indexer

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/NibiruChain/nibiru/v2/bft/libs/pubsub/query"
)

// HistoricalRange compares all bounds against one stored event attribute value.
type HistoricalRange struct {
	conditions []query.Condition
	decimal    bool
	temporal   bool
}

// CollectHistoricalRanges groups range predicates and typed equality predicates.
// Quoted strings and integer equality keep their existing exact lookup.
func CollectHistoricalRanges(conditions []query.Condition) (map[string]HistoricalRange, []int, error) {
	ranges := make(map[string]HistoricalRange)
	var indexes []int
	for i, condition := range conditions {
		_, decimal := condition.Operand.(*big.Rat)
		_, temporal := condition.Operand.(time.Time)
		if !IsRangeOperation(condition.Op) && !(condition.Op == query.OpEqual && (decimal || temporal)) {
			continue
		}
		_, integer := condition.Operand.(*big.Int)
		if !integer && !decimal && !temporal {
			return nil, nil, fmt.Errorf("%s requires a numeric, DATE, or TIME operand", condition.CompositeKey)
		}

		r := ranges[condition.CompositeKey]
		if len(r.conditions) != 0 && r.temporal != temporal {
			return nil, nil, fmt.Errorf("%s cannot combine numeric and temporal comparisons", condition.CompositeKey)
		}
		r.conditions = append(r.conditions, condition)
		r.decimal = r.decimal || decimal
		r.temporal = temporal
		ranges[condition.CompositeKey] = r
		indexes = append(indexes, i)
	}
	for key, r := range ranges {
		if r.decimal {
			for i, c := range r.conditions {
				if integer, ok := c.Operand.(*big.Int); ok {
					r.conditions[i].Operand = new(big.Rat).SetInt(integer)
				}
			}
			ranges[key] = r
		}
	}
	return ranges, indexes, nil
}

// IntegerHistoricalRange turns normalized height bounds into a range matcher.
func IntegerHistoricalRange(r QueryRange) HistoricalRange {
	var result HistoricalRange
	if r.LowerBound != nil {
		result.conditions = append(result.conditions, query.Condition{CompositeKey: r.Key, Op: query.OpGreaterEqual, Operand: r.LowerBound})
	}
	if r.UpperBound != nil {
		result.conditions = append(result.conditions, query.Condition{CompositeKey: r.Key, Op: query.OpLessEqual, Operand: r.UpperBound})
	}
	return result
}

// MatchWithTrimmedNumbers preserves the transaction index's numeric whitespace handling.
func (r HistoricalRange) MatchWithTrimmedNumbers(value string) bool {
	if !r.temporal {
		value = strings.TrimSpace(value)
	}
	return r.Match(value)
}

// Match compares the stored value in the domain of the query operands.
func (r HistoricalRange) Match(value string) bool {
	// Protobuf events store each attribute as its JSON representation.
	if (r.temporal || r.decimal) && strings.HasPrefix(value, `"`) {
		if err := json.Unmarshal([]byte(value), &value); err != nil {
			return false
		}
	}
	switch {
	case r.temporal:
		layout := query.DateLayout
		if strings.Contains(value, "T") {
			layout = query.TimeLayout
		}
		parsed, err := time.Parse(layout, value)
		if err != nil {
			return false
		}
		for _, c := range r.conditions {
			if !acceptComparison(c.Op, parsed.Compare(c.Operand.(time.Time))) {
				return false
			}
		}
	case r.decimal:
		parsed, ok := parseDecimal(value)
		if !ok {
			return false
		}
		for _, c := range r.conditions {
			if !acceptComparison(c.Op, parsed.Cmp(c.Operand.(*big.Rat))) {
				return false
			}
		}
	default:
		parsed, ok := new(big.Int).SetString(value, 10)
		if !ok {
			return false
		}
		for _, c := range r.conditions {
			if !acceptComparison(c.Op, parsed.Cmp(c.Operand.(*big.Int))) {
				return false
			}
		}
	}
	return true
}

func parseDecimal(value string) (*big.Rat, bool) {
	// Rat.SetString also accepts fractions, exponents, and nondecimal bases.
	// Event attributes must contain a plain decimal number for these comparisons.
	digits := strings.TrimPrefix(strings.TrimPrefix(value, "-"), "+")
	for _, ch := range digits {
		if (ch < '0' || ch > '9') && ch != '.' {
			return nil, false
		}
	}
	return new(big.Rat).SetString(value)
}

func acceptComparison(op query.Operator, order int) bool {
	switch op {
	case query.OpEqual:
		return order == 0
	case query.OpGreater:
		return order > 0
	case query.OpGreaterEqual:
		return order >= 0
	case query.OpLess:
		return order < 0
	case query.OpLessEqual:
		return order <= 0
	default:
		return false
	}
}

// ExtractHeightFilter intersects predicates on the index's reserved height key.
// It normalizes exclusive bounds to inclusive integer bounds and leaves event
// attributes, including the other index's height key, in the returned conditions.
func ExtractHeightFilter(conditions []query.Condition, key string) ([]query.Condition, QueryRange, error) {
	remaining := conditions[:0]
	heightRange := QueryRange{IncludeLowerBound: true, IncludeUpperBound: true}
	var lower, upper *big.Int
	for _, c := range conditions {
		if c.CompositeKey != key || (c.Op != query.OpEqual && !IsRangeOperation(c.Op)) {
			remaining = append(remaining, c)
			continue
		}
		height, ok := c.Operand.(*big.Int)
		if !ok || !height.IsInt64() {
			return nil, QueryRange{}, fmt.Errorf("%s requires a signed 64-bit integer operand", key)
		}
		heightRange.Key = key

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
