// Package eventsearch indexes events from committed ABCI block results.
package eventsearch

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/google/orderedcode"

	abci "github.com/NibiruChain/nibiru/v2/bft/abci/types"
	tmstate "github.com/NibiruChain/nibiru/v2/bft/proto/tendermint/state"
	"github.com/NibiruChain/nibiru/v2/bft/types"
)

const (
	PhaseBeginBlock = "begin_block"
	PhaseTx         = "tx"
	PhaseEndBlock   = "end_block"
)

const (
	// The leading byte versions every key in the dedicated event DB.
	keyVersion byte = 1
	keyData    byte = 'd'
	keyType    byte = 't'
	keyAttr    byte = 'a'
	keyExact   byte = 'e'
	keyJSON    byte = 'j'

	// The suffix sorts by block height, phase, transaction index, and event index.
	positionSize = 8 + 1 + 4 + 4
)

var coverageKey = []byte{keyVersion, 'c'}
var errClosed = errors.New("event index is closed")

// ErrPositionMismatch means the supplied transactions and results cannot be
// associated by block position.
var ErrPositionMismatch = errors.New("transaction positions disagree with block results")

// Event is one occurrence in a committed block result. EventIndex is zero-based
// within the begin-block, transaction, or end-block response that emitted it.
type Event struct {
	Height     int64                 `json:"height"`
	Phase      string                `json:"phase"`
	TxIndex    *uint32               `json:"tx_index,omitempty"`
	TxHash     string                `json:"tx_hash,omitempty"`
	EventIndex uint32                `json:"event_index"`
	Type       string                `json:"type"`
	Attributes []abci.EventAttribute `json:"attributes"`
}

// HeightRange identifies an inclusive interval for which block results were indexed.
// A height with no events still belongs to the coverage interval.
type HeightRange struct {
	MinHeight int64 `json:"min_height"`
	MaxHeight int64 `json:"max_height"`
}

// Store indexes committed block results independently of the consensus and
// transaction stores.
type Store struct {
	db     dbm.DB
	mu     sync.RWMutex
	closed bool
}

// New opens an event index on the supplied database.
func New(db dbm.DB) *Store {
	return &Store{db: db}
}

// Close releases the database after ongoing searches and writes finish.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.db.Close()
}

// Coverage returns the complete, non-overlapping intervals indexed by this store.
func (s *Store) Coverage() ([]HeightRange, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.readCoverage()
}

// readCoverage must be called with the store locked for reading or writing.
func (s *Store) readCoverage() ([]HeightRange, error) {
	if s.closed {
		return nil, errClosed
	}
	data, err := s.db.Get(coverageKey)
	if err != nil {
		return nil, err
	}
	coverage := []HeightRange{}
	if len(data) != 0 {
		if err := json.Unmarshal(data, &coverage); err != nil {
			return nil, fmt.Errorf("decode event index coverage: %w", err)
		}
	}
	return coverage, nil
}

// IndexBlock atomically persists the events and coverage for one height.
// Repeated writes of a committed height are harmless. A retained response
// without its block body still supplies event positions and attributes.
func (s *Store) IndexBlock(height int64, block *types.Block, responses *tmstate.ABCIResponses) error {
	if height <= 0 || responses == nil {
		return fmt.Errorf("positive height and block results required")
	}
	if block != nil {
		if block.Height != height {
			return fmt.Errorf("block height %d does not match event index height %d", block.Height, height)
		}
		if len(block.Data.Txs) != len(responses.DeliverTxs) {
			return fmt.Errorf("%w at height %d: %d transactions, %d results",
				ErrPositionMismatch, height, len(block.Data.Txs), len(responses.DeliverTxs))
		}
		for i, result := range responses.DeliverTxs {
			if result == nil {
				return fmt.Errorf("%w at height %d: missing transaction result at position %d",
					ErrPositionMismatch, height, i)
			}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	coverage, err := s.readCoverage()
	if err != nil {
		return err
	}
	if slices.ContainsFunc(coverage, func(r HeightRange) bool {
		return height >= r.MinHeight && height <= r.MaxHeight
	}) {
		return nil
	}

	batch := s.db.NewBatch()
	defer batch.Close()
	base := Event{Height: height, Phase: PhaseBeginBlock}
	if err := indexEvents(batch, base, 0, responses.GetBeginBlock().GetEvents()); err != nil {
		return err
	}

	base.Phase = PhaseTx
	for i, result := range responses.DeliverTxs {
		txIndex := uint32(i)
		base.TxIndex = &txIndex
		if block != nil {
			base.TxHash = fmt.Sprintf("%X", block.Data.Txs[i].Hash())
		}
		if err := indexEvents(batch, base, 1, result.GetEvents()); err != nil {
			return err
		}
	}

	base.Phase, base.TxIndex, base.TxHash = PhaseEndBlock, nil, ""
	if err := indexEvents(batch, base, 2, responses.GetEndBlock().GetEvents()); err != nil {
		return err
	}

	coverage = addHeight(coverage, height)
	encoded, err := json.Marshal(coverage)
	if err != nil {
		return err
	}
	if err := batch.Set(coverageKey, encoded); err != nil {
		return err
	}
	return batch.WriteSync()
}

func indexEvents(batch dbm.Batch, base Event, phase byte, events []abci.Event) error {
	for i, event := range events {
		base.EventIndex, base.Type, base.Attributes = uint32(i), event.Type, event.Attributes
		if base.Attributes == nil {
			base.Attributes = []abci.EventAttribute{}
		}
		position := encodePosition(base, phase)
		data, err := json.Marshal(base)
		if err != nil {
			return err
		}
		if err := batch.Set(append(indexPrefix(keyData), position...), data); err != nil {
			return err
		}
		if err := addPosting(batch, indexPrefix(keyType, event.Type), position); err != nil {
			return err
		}
		for _, attr := range event.Attributes {
			if err := addPosting(batch, indexPrefix(keyAttr, attr.Key), position); err != nil {
				return err
			}
			if err := addPosting(batch, indexPrefix(keyExact, attr.Key, attr.Value), position); err != nil {
				return err
			}
			if decoded, ok := jsonString(attr.Value); ok {
				if err := addPosting(batch, indexPrefix(keyJSON, attr.Key, decoded), position); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func addPosting(batch dbm.Batch, prefix, position []byte) error {
	return batch.Set(append(prefix, position...), []byte{1})
}

func encodePosition(event Event, phase byte) []byte {
	key := binary.BigEndian.AppendUint64(nil, uint64(event.Height))
	key = append(key, phase)
	var txIndex uint32
	if event.TxIndex != nil {
		txIndex = *event.TxIndex
	}
	key = binary.BigEndian.AppendUint32(key, txIndex)
	return binary.BigEndian.AppendUint32(key, event.EventIndex)
}

// Each component is an orderedcode string, so dots and whitespace are literal.
func indexPrefix(kind byte, parts ...string) []byte {
	key := []byte{keyVersion, kind}
	for _, part := range parts {
		// orderedcode.Append returns an error only for unsupported types.
		key, _ = orderedcode.Append(key, part)
	}
	return key
}

func jsonString(raw string) (string, bool) {
	var decoded string
	raw = strings.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal([]byte(raw), &decoded) != nil {
		return "", false
	}
	return decoded, true
}

func addHeight(coverage []HeightRange, height int64) []HeightRange {
	coverage = append(coverage, HeightRange{height, height})
	slices.SortFunc(coverage, func(a, b HeightRange) int {
		switch {
		case a.MinHeight < b.MinHeight:
			return -1
		case a.MinHeight > b.MinHeight:
			return 1
		default:
			return 0
		}
	})
	merged := coverage[:1]
	for _, next := range coverage[1:] {
		last := &merged[len(merged)-1]
		if next.MinHeight-last.MaxHeight <= 1 {
			last.MaxHeight = max(last.MaxHeight, next.MaxHeight)
		} else {
			merged = append(merged, next)
		}
	}
	return merged
}
