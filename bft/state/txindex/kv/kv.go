package kv

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/cosmos/gogoproto/proto"

	dbm "github.com/cometbft/cometbft-db"

	abci "github.com/NibiruChain/nibiru/v2/bft/abci/types"
	"github.com/NibiruChain/nibiru/v2/bft/libs/pubsub/query"
	"github.com/NibiruChain/nibiru/v2/bft/state/indexer"
	"github.com/NibiruChain/nibiru/v2/bft/state/txindex"
	"github.com/NibiruChain/nibiru/v2/bft/types"
)

const (
	tagKeySeparator     = "/"
	tagKeySeparatorRune = '/'
	eventSeqSeparator   = "$es$"
)

var _ txindex.TxIndexer = (*TxIndex)(nil)

// TxIndex is the simplest possible indexer, backed by key-value storage (levelDB).
type TxIndex struct {
	store dbm.DB
	// Number the events in the event list
	eventSeq int64
}

// NewTxIndex creates new KV indexer.
func NewTxIndex(store dbm.DB) *TxIndex {
	return &TxIndex{
		store: store,
	}
}

// Get gets transaction from the TxIndex storage and returns it or nil if the
// transaction is not found.
func (txi *TxIndex) Get(hash []byte) (*abci.TxResult, error) {
	if len(hash) == 0 {
		return nil, txindex.ErrorEmptyHash
	}

	rawBytes, err := txi.store.Get(hash)
	if err != nil {
		panic(err)
	}
	if rawBytes == nil {
		return nil, nil
	}

	txResult := new(abci.TxResult)
	err = proto.Unmarshal(rawBytes, txResult)
	if err != nil {
		return nil, fmt.Errorf("error reading TxResult: %v", err)
	}

	return txResult, nil
}

// AddBatch indexes a batch of transactions using the given list of events. Each
// key that indexed from the tx's events is a composite of the event type and
// the respective attribute's key delimited by a "." (eg. "account.number").
// Any event with an empty type is not indexed.
func (txi *TxIndex) AddBatch(b *txindex.Batch) error {
	storeBatch := txi.store.NewBatch()
	defer storeBatch.Close()

	for _, result := range b.Ops {
		hash := types.Tx(result.Tx).Hash()

		// index tx by events
		err := txi.indexEvents(result, hash, storeBatch)
		if err != nil {
			return err
		}

		// index by height (always)
		err = storeBatch.Set(keyForHeight(result), hash)
		if err != nil {
			return err
		}

		rawBytes, err := proto.Marshal(result)
		if err != nil {
			return err
		}
		// index by hash (always)
		err = storeBatch.Set(hash, rawBytes)
		if err != nil {
			return err
		}
	}

	return storeBatch.WriteSync()
}

// Index indexes a single transaction using the given list of events. Each key
// that indexed from the tx's events is a composite of the event type and the
// respective attribute's key delimited by a "." (eg. "account.number").
// Any event with an empty type is not indexed.
//
// If a transaction is indexed with the same hash as a previous transaction, it will
// be overwritten unless the tx result was NOT OK and the prior result was OK i.e.
// more transactions that successfully executed overwrite transactions that failed
// or successful yet older transactions.
func (txi *TxIndex) Index(result *abci.TxResult) error {
	b := txi.store.NewBatch()
	defer b.Close()

	hash := types.Tx(result.Tx).Hash()

	if !result.Result.IsOK() {
		oldResult, err := txi.Get(hash)
		if err != nil {
			return err
		}

		// if the new transaction failed and it's already indexed in an older block and was successful
		// we skip it as we want users to get the older successful transaction when they query.
		if oldResult != nil && oldResult.Result.Code == abci.CodeTypeOK {
			return nil
		}
	}

	// index tx by events
	err := txi.indexEvents(result, hash, b)
	if err != nil {
		return err
	}

	// index by height (always)
	err = b.Set(keyForHeight(result), hash)
	if err != nil {
		return err
	}

	rawBytes, err := proto.Marshal(result)
	if err != nil {
		return err
	}
	// index by hash (always)
	err = b.Set(hash, rawBytes)
	if err != nil {
		return err
	}

	return b.WriteSync()
}

func (txi *TxIndex) indexEvents(result *abci.TxResult, hash []byte, store dbm.Batch) error {
	for _, event := range result.Result.Events {
		txi.eventSeq = txi.eventSeq + 1
		// only index events with a non-empty type
		if len(event.Type) == 0 {
			continue
		}

		for _, attr := range event.Attributes {
			if len(attr.Key) == 0 {
				continue
			}

			// index if `index: true` is set
			compositeTag := fmt.Sprintf("%s.%s", event.Type, attr.Key)
			// ensure event does not conflict with a reserved prefix key
			if compositeTag == types.TxHashKey || compositeTag == types.TxHeightKey {
				return fmt.Errorf("event type and attribute key \"%s\" is reserved; please use a different key", compositeTag)
			}
			if attr.GetIndex() {
				err := store.Set(keyForEvent(compositeTag, attr.Value, result, txi.eventSeq), hash)
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// Search performs a search using the given query.
//
// It breaks the query into conditions (like "tx.height > 5"). For each
// condition, it queries the DB index. A hash-only query uses direct lookup.
// Other conditions use the event and height indexes, and any hash condition
// further restricts their matches. Providing both bounds for a range can
// reduce the scan. Results are returned in no particular order.
//
// Search will exit early and return any result fetched so far,
// when a message is received on the context chan.
func (txi *TxIndex) Search(ctx context.Context, q *query.Query) ([]*abci.TxResult, error) {
	return txi.search(ctx, q, false)
}

// SearchByTransaction allows conditions to match different indexed events
// within the same transaction inclusion.
func (txi *TxIndex) SearchByTransaction(ctx context.Context, q *query.Query) ([]*abci.TxResult, error) {
	return txi.search(ctx, q, true)
}

func (txi *TxIndex) search(ctx context.Context, q *query.Query, byTransaction bool) ([]*abci.TxResult, error) {
	select {
	case <-ctx.Done():
		return make([]*abci.TxResult, 0), nil

	default:
	}

	var hashesInitialized bool
	filteredMatches := make(map[eventMatch]struct{})

	// get a list of conditions (like "tx.height > 5")
	conditions, err := q.Conditions()
	if err != nil {
		return nil, fmt.Errorf("error during parsing conditions from query: %w", err)
	}

	hash, conditions, conflicting, err := extractHashFilter(conditions)
	if err != nil {
		return nil, fmt.Errorf("error during searching for a hash in the query: %w", err)
	}
	if conflicting {
		return []*abci.TxResult{}, nil
	}
	var hashResult *abci.TxResult
	if hash != nil {
		hashResult, err = txi.Get(hash)
		if err != nil {
			return []*abci.TxResult{}, fmt.Errorf("error while retrieving the result: %w", err)
		}
		if hashResult == nil {
			return []*abci.TxResult{}, nil
		}
		if len(conditions) == 0 {
			return []*abci.TxResult{hashResult}, nil
		}
	}

	conditions, heightRange, err := extractHeightFilter(conditions)
	if err != nil {
		return nil, err
	}
	heightInfo := HeightInfo{heightRange: heightRange}
	if hashResult != nil {
		if !checkHeightConditions(heightInfo, hashResult.Height) {
			return []*abci.TxResult{}, nil
		}
		if len(conditions) == 0 {
			return []*abci.TxResult{hashResult}, nil
		}
		// Only event entries at the known transaction height can match the hash.
		heightInfo.height = hashResult.Height
	}
	lower, _ := heightRange.LowerBound.(*big.Int)
	upper, _ := heightRange.UpperBound.(*big.Int)
	exactHeight := false
	if lower != nil && upper != nil {
		switch lower.Cmp(upper) {
		case 1:
			return []*abci.TxResult{}, nil
		case 0:
			exactHeight = true
			if lower.IsInt64() {
				heightInfo.height = lower.Int64()
			}
		}
	}

	// Height bounds narrow event matches. A height-only query uses the height index.
	ranges, rangeIndexes, otherHeightRange := indexer.LookForRangesWithHeight(conditions)
	if len(conditions) == 0 && heightRange.Key != "" {
		if exactHeight {
			conditions = append(conditions, query.Condition{CompositeKey: types.TxHeightKey, Op: query.OpEqual, Operand: lower})
		} else {
			ranges[types.TxHeightKey] = heightRange
		}
	} else if heightRange.Key == "" {
		heightInfo.heightRange = otherHeightRange
	}
	if len(ranges) > 0 {
		for _, qr := range ranges {
			if !hashesInitialized {
				filteredMatches = txi.matchRange(ctx, qr, startKey(qr.Key), filteredMatches, true, heightInfo, byTransaction)
				hashesInitialized = true

				// Ignore any remaining conditions if the first condition resulted
				// in no matches (assuming implicit AND operand).
				if len(filteredMatches) == 0 {
					break
				}
			} else {
				filteredMatches = txi.matchRange(ctx, qr, startKey(qr.Key), filteredMatches, false, heightInfo, byTransaction)
			}
		}
	}

	// Match the remaining non-range conditions.
	for i, c := range conditions {
		if intInSlice(i, rangeIndexes) {
			continue
		}

		if !hashesInitialized {
			filteredMatches = txi.match(ctx, c, startKeyForCondition(c, heightInfo.height), filteredMatches, true, heightInfo, byTransaction)
			hashesInitialized = true

			// Ignore any remaining conditions if the first condition resulted
			// in no matches (assuming implicit AND operand).
			if len(filteredMatches) == 0 {
				break
			}
		} else {
			filteredMatches = txi.match(ctx, c, startKeyForCondition(c, heightInfo.height), filteredMatches, false, heightInfo, byTransaction)
		}
	}

	results := make([]*abci.TxResult, 0, len(filteredMatches))
	type receipt struct {
		result   *abci.TxResult
		returned bool
	}
	loaded := make(map[string]receipt)
	hashString := string(hash)
RESULTS_LOOP:
	for match := range filteredMatches {
		select {
		case <-ctx.Done():
			break RESULTS_LOOP
		default:
		}
		if hashResult != nil {
			if match.hash == hashString && match.matchesReceipt(hashResult) {
				return []*abci.TxResult{hashResult}, nil
			}
			continue
		}

		entry, ok := loaded[match.hash]
		if !ok {
			res, err := txi.Get([]byte(match.hash))
			if err != nil {
				return nil, fmt.Errorf("failed to get Tx{%X}: %w", match.hash, err)
			}
			entry.result = res
			loaded[match.hash] = entry
		}
		if !entry.returned && match.matchesReceipt(entry.result) {
			results = append(results, entry.result)
			entry.returned = true
			loaded[match.hash] = entry
		}
	}
	return results, nil
}

func extractHashFilter(conditions []query.Condition) (hash []byte, remaining []query.Condition, conflicting bool, err error) {
	remaining = conditions[:0]
	for _, c := range conditions {
		if c.CompositeKey != types.TxHashKey {
			remaining = append(remaining, c)
			continue
		}
		value, ok := c.Operand.(string)
		if c.Op != query.OpEqual || !ok {
			return nil, nil, false, errors.New("tx.hash requires equality with a quoted hexadecimal hash")
		}
		decoded, err := hex.DecodeString(value)
		if err != nil {
			return nil, nil, false, err
		}
		if hash != nil && !bytes.Equal(hash, decoded) {
			conflicting = true
		}
		hash = decoded
	}
	return hash, remaining, conflicting, nil
}

// eventMatch identifies an event within one inclusion of a transaction.
type eventMatch struct {
	hash     string
	height   int64
	index    uint32
	eventSeq string
}

func (m eventMatch) matchesReceipt(result *abci.TxResult) bool {
	return result != nil && m.height == result.Height && m.index == result.Index
}

func addEventMatch(matches map[eventMatch]struct{}, key, hash []byte, heightInfo HeightInfo, byTransaction bool) {
	match, ok := eventMatchFromKey(key)
	if !ok || !checkHeightConditions(heightInfo, match.height) {
		return
	}
	if byTransaction {
		match.eventSeq = ""
	}
	// The iterator may reuse its buffers on the next iteration.
	match.hash = string(hash)
	matches[match] = struct{}{}
}

// match returns all matching txs by hash that meet a given condition and start
// key. An already filtered result (filteredMatches) is provided such that any
// non-intersecting matches are removed.
//
// NOTE: filteredMatches may be empty if no previous condition has matched.
func (txi *TxIndex) match(
	ctx context.Context,
	c query.Condition,
	startKeyBz []byte,
	filteredMatches map[eventMatch]struct{},
	firstRun bool,
	heightInfo HeightInfo,
	byTransaction bool,
) map[eventMatch]struct{} {
	// A previous match was attempted but resulted in no matches, so we return
	// no matches (assuming AND operand).
	if !firstRun && len(filteredMatches) == 0 {
		return filteredMatches
	}

	matches := make(map[eventMatch]struct{})

	switch c.Op {
	case query.OpEqual:
		it, err := dbm.IteratePrefix(txi.store, startKeyBz)
		if err != nil {
			panic(err)
		}
		defer it.Close()

	EQ_LOOP:
		for ; it.Valid(); it.Next() {

			addEventMatch(matches, it.Key(), it.Value(), heightInfo, byTransaction)
			// Potentially exit early.
			select {
			case <-ctx.Done():
				break EQ_LOOP
			default:
			}
		}
		if err := it.Error(); err != nil {
			panic(err)
		}

	case query.OpExists:
		// XXX: can't use startKeyBz here because c.Operand is nil
		// (e.g. "account.owner/<nil>/" won't match w/ a single row)
		it, err := dbm.IteratePrefix(txi.store, startKey(c.CompositeKey))
		if err != nil {
			panic(err)
		}
		defer it.Close()

	EXISTS_LOOP:
		for ; it.Valid(); it.Next() {
			addEventMatch(matches, it.Key(), it.Value(), heightInfo, byTransaction)

			// Potentially exit early.
			select {
			case <-ctx.Done():
				break EXISTS_LOOP
			default:
			}
		}
		if err := it.Error(); err != nil {
			panic(err)
		}

	case query.OpContains:
		// XXX: startKey does not apply here.
		// For example, if startKey = "account.owner/an/" and search query = "account.owner CONTAINS an"
		// we can't iterate with prefix "account.owner/an/" because we might miss keys like "account.owner/Ulan/"
		it, err := dbm.IteratePrefix(txi.store, startKey(c.CompositeKey))
		if err != nil {
			panic(err)
		}
		defer it.Close()

	CONTAINS_LOOP:
		for ; it.Valid(); it.Next() {
			if !isTagKey(it.Key()) {
				continue
			}

			if strings.Contains(extractValueFromKey(it.Key()), c.Operand.(string)) {
				addEventMatch(matches, it.Key(), it.Value(), heightInfo, byTransaction)
			}

			// Potentially exit early.
			select {
			case <-ctx.Done():
				break CONTAINS_LOOP
			default:
			}
		}
		if err := it.Error(); err != nil {
			panic(err)
		}
	default:
		panic("other operators should be handled already")
	}

	if len(matches) == 0 || firstRun {
		// Either:
		//
		// 1. Regardless if a previous match was attempted, which may have had
		// results, but no match was found for the current condition, then we
		// return no matches (assuming AND operand).
		//
		// 2. A previous match was not attempted, so we return all results.
		return matches
	}

	// Remove/reduce matches in filteredMatches that were not found in this
	// match (matches).
REMOVE_LOOP:
	for k := range filteredMatches {
		if _, ok := matches[k]; !ok {
			delete(filteredMatches, k)

			// Potentially exit early.
			select {
			case <-ctx.Done():
				break REMOVE_LOOP
			default:
			}
		}
	}

	return filteredMatches
}

// matchRange returns all matching txs by hash that meet a given queryRange and
// start key. An already filtered result (filteredMatches) is provided such that
// any non-intersecting matches are removed.
//
// NOTE: filteredMatches may be empty if no previous condition has matched.
func (txi *TxIndex) matchRange(
	ctx context.Context,
	qr indexer.QueryRange,
	startKey []byte,
	filteredMatches map[eventMatch]struct{},
	firstRun bool,
	heightInfo HeightInfo,
	byTransaction bool,
) map[eventMatch]struct{} {
	// A previous match was attempted but resulted in no matches, so we return
	// no matches (assuming AND operand).
	if !firstRun && len(filteredMatches) == 0 {
		return filteredMatches
	}

	matches := make(map[eventMatch]struct{})

	it, err := dbm.IteratePrefix(txi.store, startKey)
	if err != nil {
		panic(err)
	}
	defer it.Close()
	bigIntValue := new(big.Int)

LOOP:
	for ; it.Valid(); it.Next() {
		// TODO: We need to make a function for getting it.Key() as a byte slice with no copies.
		// It currently copies the source data (which can change on a subsequent .Next() call) but that
		// is not an issue for us.
		key := it.Key()
		if !isTagKey(key) {
			continue
		}

		if _, ok := qr.AnyBound().(*big.Int); ok {
			value := extractValueFromKey(key)
			v, ok := bigIntValue.SetString(value, 10)
			if !ok {
				continue LOOP
			}
			if checkBounds(qr, v) {
				addEventMatch(matches, key, it.Value(), heightInfo, byTransaction)
			}

			// XXX: passing time in a ABCI Events is not yet implemented
			// case time.Time:
			// 	v := strconv.ParseInt(extractValueFromKey(it.Key()), 10, 64)
			// 	if v == r.upperBound {
			// 		break
			// 	}
		}

		// Potentially exit early.
		select {
		case <-ctx.Done():
			break LOOP
		default:
		}
	}
	if err := it.Error(); err != nil {
		panic(err)
	}

	if len(matches) == 0 || firstRun {
		// Either:
		//
		// 1. Regardless if a previous match was attempted, which may have had
		// results, but no match was found for the current condition, then we
		// return no matches (assuming AND operand).
		//
		// 2. A previous match was not attempted, so we return all results.
		return matches
	}

	// Remove/reduce matches in filteredMatches that were not found in this
	// match (matches).
REMOVE_LOOP:
	for k := range filteredMatches {
		if _, ok := matches[k]; !ok {
			delete(filteredMatches, k)

			// Potentially exit early.
			select {
			case <-ctx.Done():
				break REMOVE_LOOP
			default:
			}
		}
	}

	return filteredMatches
}

// Keys

func isTagKey(key []byte) bool {
	// Normally, if the event was indexed with an event sequence, the number of
	// tags should 4. Alternatively it should be 3 if the event was not indexed
	// with the corresponding event sequence. However, some attribute values in
	// production can contain the tag separator. Therefore, the condition is >= 3.
	numTags := 0
	for i := 0; i < len(key); i++ {
		if key[i] == tagKeySeparatorRune {
			numTags++
			if numTags >= 3 {
				return true
			}
		}
	}
	return false
}

// eventMatchFromKey reads the height and index from the suffix because attribute
// values can contain slashes. Legacy keys have no event sequence.
func eventMatchFromKey(key []byte) (eventMatch, bool) {
	end := bytes.LastIndexByte(key, tagKeySeparatorRune)
	if end < 1 {
		return eventMatch{}, false
	}
	start := bytes.LastIndexByte(key[:end], tagKeySeparatorRune)
	if start < 0 {
		return eventMatch{}, false
	}
	height, err := strconv.ParseInt(string(key[start+1:end]), 10, 64)
	if err != nil {
		return eventMatch{}, false
	}

	indexText, eventSeq, hasSeq := strings.Cut(string(key[end+1:]), eventSeqSeparator)
	index, err := strconv.ParseUint(indexText, 10, 32)
	if err != nil {
		return eventMatch{}, false
	}
	if !hasSeq {
		eventSeq = "0"
	}
	return eventMatch{height: height, index: uint32(index), eventSeq: eventSeq}, true
}

func extractValueFromKey(key []byte) string {
	// Find the positions of tagKeySeparator in the byte slice
	var indices []int
	for i, b := range key {
		if b == tagKeySeparatorRune {
			indices = append(indices, i)
		}
	}

	// If there are less than 2 occurrences of tagKeySeparator, return an empty string
	if len(indices) < 2 {
		return ""
	}

	// Extract the value between the first and second last occurrence of tagKeySeparator
	value := key[indices[0]+1 : indices[len(indices)-2]]

	// Trim any leading or trailing whitespace
	value = bytes.TrimSpace(value)

	// TODO: Do an unsafe cast to avoid an extra allocation here
	return string(value)
}

func keyForEvent(key string, value string, result *abci.TxResult, eventSeq int64) []byte {
	return []byte(fmt.Sprintf("%s/%s/%d/%d%s",
		key,
		value,
		result.Height,
		result.Index,
		eventSeqSeparator+strconv.FormatInt(eventSeq, 10),
	))
}

func keyForHeight(result *abci.TxResult) []byte {
	return []byte(fmt.Sprintf("%s/%d/%d/%d%s",
		types.TxHeightKey,
		result.Height,
		result.Height,
		result.Index,
		// Added to facilitate having the eventSeq in event keys
		// Otherwise queries break expecting 5 entries
		eventSeqSeparator+"0",
	))
}

func startKeyForCondition(c query.Condition, height int64) []byte {
	if height > 0 {
		return startKey(c.CompositeKey, c.Operand, height)
	}
	return startKey(c.CompositeKey, c.Operand)
}

func startKey(fields ...interface{}) []byte {
	var b bytes.Buffer
	for _, f := range fields {
		b.Write([]byte(fmt.Sprintf("%v", f) + tagKeySeparator))
	}
	return b.Bytes()
}

func checkBounds(ranges indexer.QueryRange, v *big.Int) bool {
	include := true
	lowerBound := ranges.LowerBoundValue()
	upperBound := ranges.UpperBoundValue()
	if lowerBound != nil && v.Cmp(lowerBound.(*big.Int)) == -1 {
		include = false
	}

	if upperBound != nil && v.Cmp(upperBound.(*big.Int)) == 1 {
		include = false
	}

	return include
}

//nolint:unused,deadcode
func lookForHeight(conditions []query.Condition) (height int64) {
	for _, c := range conditions {
		if c.CompositeKey == types.TxHeightKey && c.Op == query.OpEqual {
			return c.Operand.(int64)
		}
	}
	return 0
}
