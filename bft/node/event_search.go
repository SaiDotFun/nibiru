package node

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/NibiruChain/nibiru/v2/bft/libs/log"
	tmstate "github.com/NibiruChain/nibiru/v2/bft/proto/tendermint/state"
	sm "github.com/NibiruChain/nibiru/v2/bft/state"
	"github.com/NibiruChain/nibiru/v2/bft/state/eventsearch"
	"github.com/NibiruChain/nibiru/v2/bft/store"
	"github.com/NibiruChain/nibiru/v2/bft/types"
)

type eventSearchState interface {
	Load() (sm.State, error)
	ReadABCIResponses(height int64) (*tmstate.ABCIResponses, error)
}

type eventSearchBlocks interface {
	Base() int64
	ReadBlock(height int64) (*types.Block, error)
}

type eventSearchIndex interface {
	Coverage() ([]eventsearch.HeightRange, error)
	IndexBlock(int64, *types.Block, *tmstate.ABCIResponses) error
}

// startEventSearch reads committed state off the consensus path. The cursor
// resumes on each error and the index persists completed block ranges.
func startEventSearch(state eventSearchState, blocks eventSearchBlocks, index eventSearchIndex, first int64, logger log.Logger) func() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		next := first
		for {
			if err := catchUpEventSearch(ctx, state, blocks, index, &next, logger); err != nil && ctx.Err() == nil {
				logger.Error("Updating event search index", "height", next, "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

func catchUpEventSearch(ctx context.Context, state eventSearchState, blocks eventSearchBlocks, index eventSearchIndex, next *int64, logger log.Logger) error {
	base := blocks.Base()
	if *next == 0 && base == 0 {
		return nil
	}
	committed, err := state.Load()
	if err != nil {
		return err
	}
	if *next == 0 {
		*next = base
	}
	if *next > committed.LastBlockHeight {
		return nil
	}
	coverage, err := index.Coverage()
	if err != nil {
		return err
	}

	for *next <= committed.LastBlockHeight {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(coverage) > 0 && coverage[0].MaxHeight < *next {
			coverage = coverage[1:]
			continue
		}
		if len(coverage) > 0 && coverage[0].MinHeight <= *next {
			*next = coverage[0].MaxHeight + 1
			continue
		}
		err := indexEventSearchHeight(state, blocks, index, *next)
		if err != nil {
			var missing sm.ErrNoABCIResponsesForHeight
			switch {
			case errors.As(err, &missing):
				// Missing history stays visible as a gap in coverage.
			case errors.Is(err, sm.ErrCorruptABCIResponses), errors.Is(err, store.ErrCorruptBlock), errors.Is(err, eventsearch.ErrPositionMismatch):
				logger.Error("Skipping unreadable event search history", "height", *next, "err", err)
			default:
				return err
			}
		}
		*next = *next + 1
	}
	return nil
}

func indexEventSearchHeight(state eventSearchState, blocks eventSearchBlocks, index eventSearchIndex, height int64) error {
	responses, err := state.ReadABCIResponses(height)
	if err != nil {
		return fmt.Errorf("load block results at height %d: %w", height, err)
	}
	block, err := blocks.ReadBlock(height)
	if err != nil {
		return fmt.Errorf("read block at height %d: %w", height, err)
	}
	if err := index.IndexBlock(height, block, responses); err != nil {
		return fmt.Errorf("index block results at height %d: %w", height, err)
	}
	return nil
}

// prepareEventSearch uses the node's response retention policy. The caller can
// start the node even if the auxiliary index is unavailable.
func (n *Node) prepareEventSearch() (_ eventSearchState, first int64, err error) {
	if n.config.Storage.DiscardABCIResponses {
		return nil, 0, nil
	}
	source, ok := n.stateStore.(sm.BootstrapStore)
	if !ok {
		return nil, 0, errors.New("event search requires a bootstrap state store")
	}
	db, err := n.eventSearchDBProvider(&DBContext{"event_search_v1", n.config})
	if err != nil {
		return nil, 0, fmt.Errorf("open event search index: %w", err)
	}
	index := eventsearch.New(db)
	defer func() {
		if err != nil {
			err = errors.Join(err, index.Close())
		}
	}()
	state, err := source.Load()
	if err != nil {
		return nil, 0, err
	}
	ranges, err := index.Coverage()
	if err != nil {
		return nil, 0, err
	}
	if len(ranges) > 0 && ranges[len(ranges)-1].MaxHeight > state.LastBlockHeight {
		return nil, 0, errors.New("event search index is ahead of committed state; rebuild the event index")
	}

	first = n.blockStore.Base()
	if len(ranges) > 0 {
		if first == 0 || ranges[0].MinHeight < first {
			first = ranges[0].MinHeight
		}
	} else if first != 1 && state.LastBlockHeight > 0 {
		// Discover response-only history before consensus writes to the
		// iterator's domain. A block store starting at 1 needs no scan.
		retained, err := source.FirstRetainedABCIHeight(context.Background())
		if err != nil {
			return nil, 0, fmt.Errorf("locate retained ABCI responses: %w", err)
		}
		if retained != 0 {
			first = retained
		}
	}
	n.eventSearch = index
	return source, first, nil
}
