package eventsearch

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"slices"

	abci "github.com/NibiruChain/nibiru/v2/bft/abci/types"
)

const (
	defaultLimit = 50
	maxLimit     = 200
)

// Filter selects individual events. Each attribute condition must match the
// same event. Phases are alternatives. Zero height bounds are open-ended.
type Filter struct {
	MinHeight  int64             `json:"min_height,omitempty"`
	MaxHeight  int64             `json:"max_height,omitempty"`
	Phases     []string          `json:"phases,omitempty"`
	Type       string            `json:"type,omitempty"`
	Attributes []AttributeFilter `json:"attributes,omitempty"`
}

// AttributeFilter matches a raw value, a decoded JSON string, or key presence.
type AttributeFilter struct {
	Key          string  `json:"key"`
	Eq           *string `json:"eq,omitempty"`
	JSONStringEq *string `json:"json_string_eq,omitempty"`
	Exists       bool    `json:"exists,omitempty"`
}

// ValidateAttributes checks that each attribute filter selects one operation.
func ValidateAttributes(filters []AttributeFilter) error {
	for _, f := range filters {
		if f.Key == "" {
			return fmt.Errorf("event attribute key is required")
		}
		options := 0
		for _, set := range []bool{f.Eq != nil, f.JSONStringEq != nil, f.Exists} {
			if set {
				options++
			}
		}
		if options != 1 {
			return fmt.Errorf("event attribute %q requires exactly one of eq, json_string_eq, or exists", f.Key)
		}
	}
	return nil
}

// MatchesAttributes requires every condition to match an attribute of the event.
// Each condition can match any occurrence of a repeated key.
func MatchesAttributes(attrs []abci.EventAttribute, filters []AttributeFilter) bool {
	for _, f := range filters {
		if !slices.ContainsFunc(attrs, f.matches) {
			return false
		}
	}
	return true
}

func (f AttributeFilter) matches(attr abci.EventAttribute) bool {
	if f.Key != attr.Key {
		return false
	}
	switch {
	case f.Exists:
		return true
	case f.Eq != nil:
		return attr.Value == *f.Eq
	case f.JSONStringEq != nil:
		decoded, ok := jsonString(attr.Value)
		return ok && decoded == *f.JSONStringEq
	}
	return false
}

func (f Filter) validate() error {
	if f.MinHeight < 0 || f.MaxHeight < 0 || (f.MaxHeight > 0 && f.MinHeight > f.MaxHeight) {
		return fmt.Errorf("invalid event height range")
	}
	for _, phase := range f.Phases {
		switch phase {
		case PhaseBeginBlock, PhaseTx, PhaseEndBlock:
		default:
			return fmt.Errorf("unknown event phase %q", phase)
		}
	}
	return ValidateAttributes(f.Attributes)
}

func (f Filter) matches(event Event) bool {
	return (f.Type == "" || f.Type == event.Type) &&
		(len(f.Phases) == 0 || slices.Contains(f.Phases, event.Phase)) &&
		MatchesAttributes(event.Attributes, f.Attributes)
}

// Page reports the actual indexed coverage of the selected height range at the
// time of the first page. A gap between intervals represents an unindexed height.
type Page struct {
	Events     []Event       `json:"events"`
	NextCursor string        `json:"next_cursor,omitempty"`
	Coverage   []HeightRange `json:"coverage"`
}

// Search returns individual events in ascending block execution order. A cursor
// holds the upper height and validates the covered intervals so appends and
// backfills cannot silently change the result set between pages.
func (s *Store) Search(ctx context.Context, filter Filter, cursor string, limit int) (Page, error) {
	if err := filter.validate(); err != nil {
		return Page{}, err
	}
	if limit == 0 {
		limit = defaultLimit
	}
	if limit < 1 || limit > maxLimit {
		return Page{}, fmt.Errorf("event search limit must be between 1 and %d", maxLimit)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return Page{}, err
	}
	coverage, err := s.readCoverage()
	if err != nil {
		return Page{}, err
	}
	page := Page{Events: []Event{}, Coverage: []HeightRange{}}
	if len(coverage) == 0 && cursor == "" {
		return page, nil
	}

	head := int64(0)
	if len(coverage) > 0 {
		head = coverage[len(coverage)-1].MaxHeight
	}
	if filter.MaxHeight > 0 {
		head = min(head, filter.MaxHeight)
	}
	filterDigest := digestJSON(filter)
	var after []byte
	var coverageDigest [sha256.Size]byte
	if cursor != "" {
		state, err := decodeCursor(cursor)
		if err != nil {
			return Page{}, err
		}
		if state.filter != filterDigest || state.head > head {
			return Page{}, fmt.Errorf("event search cursor does not match the query")
		}
		head, after, coverageDigest = state.head, state.after, state.coverage
	}
	page.Coverage = intersectCoverage(coverage, max(1, filter.MinHeight), head)
	actualCoverageDigest := digestJSON(page.Coverage)
	if cursor != "" && coverageDigest != actualCoverageDigest {
		return Page{}, fmt.Errorf("event index coverage changed; start a new search")
	}
	if len(page.Coverage) == 0 {
		return page, nil
	}

	prefix, postings := candidatePrefix(filter)
	lower := uint64(page.Coverage[0].MinHeight)
	start := binary.BigEndian.AppendUint64(slices.Clone(prefix), lower)
	if len(after) != 0 {
		afterHeight := binary.BigEndian.Uint64(after)
		if afterHeight < lower || afterHeight > uint64(head) {
			return Page{}, fmt.Errorf("invalid event search cursor position")
		}
		start = append(append(slices.Clone(prefix), after...), 0)
	}
	end := binary.BigEndian.AppendUint64(slices.Clone(prefix), uint64(head)+1)
	iter, err := s.db.Iterator(start, end)
	if err != nil {
		return Page{}, err
	}
	defer iter.Close()

	var last []byte
	for ; iter.Valid(); iter.Next() {
		if err := ctx.Err(); err != nil {
			return Page{}, err
		}
		position := iter.Key()[len(prefix):]
		if len(position) != positionSize {
			return Page{}, fmt.Errorf("invalid event index position")
		}
		data := iter.Value()
		if postings {
			data, err = s.db.Get(append(indexPrefix(keyData), position...))
			if err != nil {
				return Page{}, err
			}
		}
		var event Event
		if err := json.Unmarshal(data, &event); err != nil {
			return Page{}, fmt.Errorf("decode indexed event: %w", err)
		}
		if !filter.matches(event) {
			continue
		}
		if len(page.Events) == limit {
			page.NextCursor = encodeCursor(cursorState{head, last, filterDigest, actualCoverageDigest})
			break
		}
		page.Events = append(page.Events, event)
		last = append(last[:0], position...)
	}
	if err := iter.Error(); err != nil {
		return Page{}, err
	}
	return page, nil
}

// candidatePrefix chooses a point-value posting when possible, then type or
// attribute presence. The raw event supplies the remaining conditions.
func candidatePrefix(filter Filter) ([]byte, bool) {
	for _, attr := range filter.Attributes {
		if attr.Eq != nil {
			return indexPrefix(keyExact, attr.Key, *attr.Eq), true
		}
		if attr.JSONStringEq != nil {
			return indexPrefix(keyJSON, attr.Key, *attr.JSONStringEq), true
		}
	}
	if filter.Type != "" {
		return indexPrefix(keyType, filter.Type), true
	}
	if len(filter.Attributes) > 0 {
		return indexPrefix(keyAttr, filter.Attributes[0].Key), true
	}
	return indexPrefix(keyData), false
}

func intersectCoverage(coverage []HeightRange, minHeight, maxHeight int64) []HeightRange {
	result := []HeightRange{}
	for _, r := range coverage {
		lower, upper := max(minHeight, r.MinHeight), min(maxHeight, r.MaxHeight)
		if lower <= upper {
			result = append(result, HeightRange{lower, upper})
		}
	}
	return result
}

type cursorState struct {
	head     int64
	after    []byte
	filter   [sha256.Size]byte
	coverage [sha256.Size]byte
}

const (
	cursorSize    = 1 + 8 + positionSize + 2*sha256.Size
	afterStart    = 1 + 8
	filterStart   = afterStart + positionSize
	coverageStart = filterStart + sha256.Size
)

func encodeCursor(state cursorState) string {
	data := binary.BigEndian.AppendUint64([]byte{keyVersion}, uint64(state.head))
	data = append(data, state.after...)
	data = append(data, state.filter[:]...)
	data = append(data, state.coverage[:]...)
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeCursor(cursor string) (cursorState, error) {
	badCursor := fmt.Errorf("invalid event search cursor")
	if len(cursor) != base64.RawURLEncoding.EncodedLen(cursorSize) {
		return cursorState{}, badCursor
	}
	data, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(data) != cursorSize || data[0] != keyVersion {
		return cursorState{}, badCursor
	}
	head := binary.BigEndian.Uint64(data[1:afterStart])
	if head == 0 || head > math.MaxInt64 {
		return cursorState{}, badCursor
	}
	return cursorState{
		head: int64(head), after: data[afterStart:filterStart],
		filter: [sha256.Size]byte(data[filterStart:coverageStart]), coverage: [sha256.Size]byte(data[coverageStart:]),
	}, nil
}

func digestJSON(value any) [sha256.Size]byte {
	// The filter and coverage hold only JSON-compatible values.
	data, _ := json.Marshal(value)
	return sha256.Sum256(data)
}
