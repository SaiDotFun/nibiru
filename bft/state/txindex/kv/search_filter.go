package kv

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"

	abci "github.com/NibiruChain/nibiru/v2/bft/abci/types"
	"github.com/NibiruChain/nibiru/v2/bft/crypto/tmhash"
	"github.com/NibiruChain/nibiru/v2/bft/libs/pubsub/query"
	"github.com/NibiruChain/nibiru/v2/bft/state/eventsearch"
	"github.com/NibiruChain/nibiru/v2/bft/types"
)

// SearchFilter finds transactions by hash, height, and groups of event attributes.
type SearchFilter struct {
	Hash      string             `json:"hash,omitempty"`
	MinHeight int64              `json:"min_height,omitempty"`
	MaxHeight int64              `json:"max_height,omitempty"`
	Events    []EventGroupFilter `json:"events,omitempty"`
}

// EventGroupFilter requires one transaction event to satisfy every attribute predicate.
type EventGroupFilter struct {
	Type       string                        `json:"type"`
	Attributes []eventsearch.AttributeFilter `json:"attributes,omitempty"`
}

// SearchFilter uses the transaction index for candidate selection, then verifies
// each event group against the corresponding transaction execution result.
func (txi *TxIndex) SearchFilter(ctx context.Context, filter SearchFilter) ([]*abci.TxResult, error) {
	conditions, err := filter.candidateConditions()
	if err != nil {
		return nil, err
	}

	if len(conditions) == 2 && conditions[0].CompositeKey == types.TxHeightKey &&
		conditions[1].CompositeKey == types.TxHeightKey {
		var matches []*abci.TxResult
		for height := filter.MinHeight; ; height++ {
			exact := []query.Condition{
				{CompositeKey: types.TxHeightKey, Op: query.OpEqual, Operand: big.NewInt(height)},
			}
			rows, err := txi.matchingEventGroups(ctx, filter, exact)
			if err != nil {
				return nil, err
			}
			matches = append(matches, rows...)
			if height == filter.MaxHeight {
				return matches, nil
			}
		}
	}
	return txi.matchingEventGroups(ctx, filter, conditions)
}

func (txi *TxIndex) matchingEventGroups(ctx context.Context, filter SearchFilter, conditions []query.Condition) ([]*abci.TxResult, error) {
	candidates, err := txi.searchConditions(ctx, conditions, true)
	if err != nil {
		return nil, err
	}
	matches := candidates[:0]
	for _, tx := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if filter.matchesEvents(tx.Result.Events) {
			matches = append(matches, tx)
		}
	}
	return matches, ctx.Err()
}

const maxHeightScan = 10_000

func (f SearchFilter) candidateConditions() ([]query.Condition, error) {
	if f.MinHeight < 0 || f.MaxHeight < 0 || (f.MaxHeight > 0 && f.MinHeight > f.MaxHeight) {
		return nil, errors.New("invalid transaction height range")
	}
	var conditions []query.Condition
	if f.Hash != "" {
		hash := f.Hash
		if strings.HasPrefix(hash, "0x") || strings.HasPrefix(hash, "0X") {
			hash = hash[2:]
		}
		if decoded, err := hex.DecodeString(hash); err != nil || len(decoded) != tmhash.Size {
			return nil, errors.New("transaction hash must be a 32-byte hexadecimal string")
		}
		conditions = append(conditions, query.Condition{CompositeKey: types.TxHashKey, Op: query.OpEqual, Operand: hash})
	}
	if f.MinHeight > 0 {
		conditions = append(conditions, query.Condition{CompositeKey: types.TxHeightKey, Op: query.OpGreaterEqual, Operand: big.NewInt(f.MinHeight)})
	}
	if f.MaxHeight > 0 {
		conditions = append(conditions, query.Condition{CompositeKey: types.TxHeightKey, Op: query.OpLessEqual, Operand: big.NewInt(f.MaxHeight)})
	}
	hasIndexedPredicate := false
	for i, group := range f.Events {
		if group.Type == "" {
			return nil, fmt.Errorf("event group %d requires a type", i)
		}
		if err := eventsearch.ValidateAttributes(group.Attributes); err != nil {
			return nil, fmt.Errorf("event group %d: %w", i, err)
		}
		for _, attr := range group.Attributes {
			hasIndexedPredicate = true
			key := group.Type + "." + attr.Key
			if key == types.TxHashKey || key == types.TxHeightKey {
				return nil, fmt.Errorf("event attribute %q is reserved", key)
			}
			// JSON string equality requires decoding the raw stored operand, so
			// select by attribute presence and compare the decoded value below.
			condition := query.Condition{CompositeKey: key, Op: query.OpExists}
			if attr.Eq != nil {
				condition.Op = query.OpEqual
				condition.Operand = *attr.Eq
			}
			conditions = append(conditions, condition)
		}
	}
	if f.Hash == "" && !hasIndexedPredicate &&
		(f.MinHeight == 0 || f.MaxHeight == 0 || f.MaxHeight-f.MinHeight >= maxHeightScan) {
		return nil, fmt.Errorf("structured transaction search without an indexed attribute or hash requires a height range of at most %d blocks", maxHeightScan)
	}
	return conditions, nil
}

func (f SearchFilter) matchesEvents(events []abci.Event) bool {
	for _, group := range f.Events {
		anyMatch := false
		for _, event := range events {
			if event.Type != group.Type {
				continue
			}
			// Transaction search uses the existing transaction index, whose
			// searchable set is limited to attributes marked for indexing.
			indexed := make([]abci.EventAttribute, 0, len(event.Attributes))
			for _, attr := range event.Attributes {
				if attr.Index {
					indexed = append(indexed, attr)
				}
			}
			if eventsearch.MatchesAttributes(indexed, group.Attributes) {
				anyMatch = true
				break
			}
		}
		if !anyMatch {
			return false
		}
	}
	return true
}
