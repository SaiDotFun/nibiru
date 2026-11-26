package core

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	abci "github.com/NibiruChain/nibiru/v2/bft/abci/types"
	cmtlog "github.com/NibiruChain/nibiru/v2/bft/libs/log"
	tmstate "github.com/NibiruChain/nibiru/v2/bft/proto/tendermint/state"
	rpc "github.com/NibiruChain/nibiru/v2/bft/rpc/jsonrpc/server"
	"github.com/NibiruChain/nibiru/v2/bft/state/eventsearch"
	dbm "github.com/cometbft/cometbft-db"
	"github.com/stretchr/testify/require"
)

func TestEventSearchHTTP(t *testing.T) {
	previous := env
	index := eventsearch.New(dbm.NewMemDB())
	t.Cleanup(func() {
		SetEnvironment(previous)
		require.NoError(t, index.Close())
	})
	SetEnvironment(&Environment{EventSearch: index})
	blockEvent := abci.Event{
		Type: "wasm",
		Attributes: []abci.EventAttribute{
			{Key: "action", Value: "settle"},
		},
	}
	require.NoError(t, index.IndexBlock(9, nil, &tmstate.ABCIResponses{
		BeginBlock: &abci.ResponseBeginBlock{Events: []abci.Event{blockEvent}},
		DeliverTxs: []*abci.ResponseDeliverTx{{Events: []abci.Event{blockEvent}}},
		EndBlock:   &abci.ResponseEndBlock{Events: []abci.Event{blockEvent}},
	}))
	mux := http.NewServeMux()
	rpc.RegisterRPCFuncs(mux, map[string]*rpc.RPCFunc{"event_search": Routes["event_search"]}, cmtlog.NewNopLogger())

	for _, tc := range []struct {
		params, want string
	}{
		{`,"params":{"filter":{"min_height":"9","max_height":"9","phases":["end_block"],"type":"wasm","attributes":[{"key":"action","eq":"settle"}]}} `, `"phase":"end_block"`},
		{"", "event_search requires a filter"},
	} {
		body := `{"jsonrpc":"2.0","id":1,"method":"event_search"` + tc.params + "}"
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body)))
		require.Contains(t, recorder.Body.String(), tc.want)
	}
}
