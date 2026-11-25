package node

import (
	"errors"
	"testing"

	abci "github.com/NibiruChain/nibiru/v2/bft/abci/types"
	tmstate "github.com/NibiruChain/nibiru/v2/bft/proto/tendermint/state"
	sm "github.com/NibiruChain/nibiru/v2/bft/state"
	"github.com/NibiruChain/nibiru/v2/bft/state/eventsearch"
	"github.com/NibiruChain/nibiru/v2/bft/types"
	dbm "github.com/cometbft/cometbft-db"
	"github.com/stretchr/testify/require"
)

type eventSearchTestSource struct {
	base      int64
	committed int64
	load      func(int64) (*tmstate.ABCIResponses, error)
}

func (s *eventSearchTestSource) Base() int64                { return s.base }
func (*eventSearchTestSource) LoadBlock(int64) *types.Block { return nil }
func (s *eventSearchTestSource) Load() (sm.State, error) {
	return sm.State{LastBlockHeight: s.committed}, nil
}
func (s *eventSearchTestSource) ReadABCIResponses(h int64) (*tmstate.ABCIResponses, error) {
	return s.load(h)
}

func TestEventSearchCatchUp(t *testing.T) {
	index := eventsearch.New(dbm.NewMemDB())
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	empty := &tmstate.ABCIResponses{BeginBlock: &abci.ResponseBeginBlock{}, EndBlock: &abci.ResponseEndBlock{}}
	require.NoError(t, index.IndexBlock(10, nil, empty))
	var attempts []int64
	transient := errors.New("temporary read failure")
	source := &eventSearchTestSource{base: 10, committed: 13}
	source.load = func(h int64) (*tmstate.ABCIResponses, error) {
		attempts = append(attempts, h)
		switch h {
		case 9:
			return nil, sm.ErrNoABCIResponsesForHeight{Height: h}
		case 12:
			if attempts[len(attempts)-2] != 12 {
				return nil, transient
			}
		}
		return empty, nil
	}
	next := int64(9)
	require.ErrorIs(t, catchUpEventSearch(t.Context(), source, source, index, &next), transient)
	require.Equal(t, int64(12), next)
	require.NoError(t, catchUpEventSearch(t.Context(), source, source, index, &next))
	require.Equal(t, []int64{9, 11, 12, 12, 13}, attempts)
	require.Equal(t, int64(14), next)
	ranges, err := index.Coverage()
	require.NoError(t, err)
	require.Equal(t, []eventsearch.HeightRange{{MinHeight: 10, MaxHeight: 13}}, ranges)
}
