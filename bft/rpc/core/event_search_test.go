package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	abci "github.com/NibiruChain/nibiru/v2/bft/abci/types"
	cmtlog "github.com/NibiruChain/nibiru/v2/bft/libs/log"
	tmstate "github.com/NibiruChain/nibiru/v2/bft/proto/tendermint/state"
	rpc "github.com/NibiruChain/nibiru/v2/bft/rpc/jsonrpc/server"
	"github.com/NibiruChain/nibiru/v2/bft/state/eventsearch"
	tmtypes "github.com/NibiruChain/nibiru/v2/bft/types"
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
			{Key: "zebra", Value: "first"},
			{Key: "action", Value: "", Index: true},
			{Key: "action", Value: "settle"},
		},
	}
	tx := tmtypes.Tx("event search fixture")
	block := &tmtypes.Block{Header: tmtypes.Header{Height: 9}, Data: tmtypes.Data{Txs: tmtypes.Txs{tx}}}
	require.NoError(t, index.IndexBlock(9, block, &tmstate.ABCIResponses{
		BeginBlock: &abci.ResponseBeginBlock{Events: []abci.Event{blockEvent}},
		DeliverTxs: []*abci.ResponseDeliverTx{{Events: []abci.Event{blockEvent}}},
		EndBlock:   &abci.ResponseEndBlock{Events: []abci.Event{blockEvent, {Type: "empty"}}},
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

	call := func(params string) map[string]json.RawMessage {
		t.Helper()
		body := `{"jsonrpc":"2.0","id":1,"method":"event_search","params":` + params + `}`
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body)))
		var response struct {
			Result map[string]json.RawMessage `json:"result"`
		}
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
		require.NotNil(t, response.Result, recorder.Body.String())
		return response.Result
	}
	filter := `"filter":{"type":"wasm","attributes":[{"key":"action","eq":"settle"}]},"limit":"1"`
	object := call(`{` + filter + `}`)
	require.Equal(t, `[{"height":"9","phase":"begin_block","event_index":0,"event":{"wasm":{"zebra":"first","action":""}}}]`, string(object["events"]))
	require.NotEmpty(t, object["next_cursor"])
	require.JSONEq(t, `[{"min_height":"9","max_height":"9"}]`, string(object["coverage"]))
	next := call(`{` + filter + `,"cursor":` + string(object["next_cursor"]) + `}`)
	require.Equal(t, fmt.Sprintf(`[{"height":"9","phase":"tx","tx_index":0,"tx_hash":"%X","event_index":0,"event":{"wasm":{"zebra":"first","action":""}}}]`, tx.Hash()), string(next["events"]))
	empty := call(`{"filter":{"type":"empty"}}`)
	require.JSONEq(t, `[{"height":"9","phase":"end_block","event_index":1,"event":{"empty":{}}}]`, string(empty["events"]))
}
