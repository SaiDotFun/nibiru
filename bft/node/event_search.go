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
	"github.com/NibiruChain/nibiru/v2/bft/types"
)

type eventSearchState interface {
	Load() (sm.State, error)
	ReadABCIResponses(height int64) (*tmstate.ABCIResponses, error)
}

type eventSearchBlocks interface {
	Base() int64
	LoadBlock(height int64) *types.Block
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
			if err := catchUpEventSearch(ctx, state, blocks, index, &next); err != nil && ctx.Err() == nil {
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

func catchUpEventSearch(ctx context.Context, state eventSearchState, blocks eventSearchBlocks, index eventSearchIndex, next *int64) error {
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
		responses, err := state.ReadABCIResponses(*next)
		if err != nil {
			var missing sm.ErrNoABCIResponsesForHeight
			if !errors.As(err, &missing) {
				return fmt.Errorf("load block results at height %d: %w", *next, err)
			}
			// Missing history stays visible as a gap in coverage.
		} else {
			block, err := loadEventSearchBlock(blocks, *next)
			if err != nil {
				return err
			}
			if err := index.IndexBlock(*next, block, responses); err != nil && !errors.Is(err, eventsearch.ErrPositionMismatch) {
				return fmt.Errorf("index block results at height %d: %w", *next, err)
			}
			// A position mismatch also leaves a gap rather than assigning wrong coordinates.
		}
		*next = *next + 1
	}
	return nil
}

// Legacy block readers panic on historical corruption. Keep that failure
// within the optional indexer so it cannot terminate the node.
func loadEventSearchBlock(blocks eventSearchBlocks, height int64) (block *types.Block, err error) {
	defer func() {
		if failure := recover(); failure != nil {
			err = fmt.Errorf("load block %d for event search: %v", height, failure)
		}
	}()
	return blocks.LoadBlock(height), nil
}
