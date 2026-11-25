package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/stretchr/testify/require"

	abci "github.com/NibiruChain/nibiru/v2/bft/abci/types"
	cmtjson "github.com/NibiruChain/nibiru/v2/bft/libs/json"
	"github.com/NibiruChain/nibiru/v2/bft/libs/log"
	coretypes "github.com/NibiruChain/nibiru/v2/bft/rpc/core/types"
	rpc "github.com/NibiruChain/nibiru/v2/bft/rpc/jsonrpc/server"
	rpctypes "github.com/NibiruChain/nibiru/v2/bft/rpc/jsonrpc/types"
	"github.com/NibiruChain/nibiru/v2/bft/state/txindex/kv"
	"github.com/NibiruChain/nibiru/v2/bft/types"
)

func TestTxSearchHTTP(t *testing.T) {
	index := kv.NewTxIndex(dbm.NewMemDB())
	tx := types.Tx("typed event transaction")
	require.NoError(t, index.Index(&abci.TxResult{
		Height: 7,
		Tx:     tx,
		Result: abci.ResponseDeliverTx{Events: []abci.Event{{
			Type: "eth.evm.v1.EventEthereumTx",
			Attributes: []abci.EventAttribute{{
				Key: "eth_hash", Value: `"0x1234"`, Index: true,
			}},
		}}},
	}))
	previous := env
	SetEnvironment(&Environment{TxIndexer: index})
	t.Cleanup(func() { SetEnvironment(previous) })
	mux := http.NewServeMux()
	rpc.RegisterRPCFuncs(mux, map[string]*rpc.RPCFunc{"tx_search": Routes["tx_search"]}, log.NewNopLogger())

	const typed = `eth.evm.v1.EventEthereumTx.eth_hash = '"0x1234"'`
	for _, test := range []struct {
		name  string
		query string
		count int
	}{
		{"typed equality", typed, 1},
		{"hash conjunction", fmt.Sprintf("tx.hash='%X' AND %s", tx.Hash(), typed), 1},
		{"filter mismatch", fmt.Sprintf("tx.hash='%X' AND tx.height=8", tx.Hash()), 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{
				"jsonrpc": "2.0", "method": "tx_search", "id": 1,
				"params": map[string]string{"query": test.query},
			})
			require.NoError(t, err)
			value, err := json.Marshal(test.query)
			require.NoError(t, err)
			params := url.Values{"query": {string(value)}}
			requests := map[string]*http.Request{
				"JSON-RPC POST": httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body)),
				"HTTP GET":      httptest.NewRequest(http.MethodGet, "/tx_search?"+params.Encode(), nil),
			}
			for name, request := range requests {
				t.Run(name, func(t *testing.T) {
					recorder := httptest.NewRecorder()
					mux.ServeHTTP(recorder, request)
					require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
					var response rpctypes.RPCResponse
					require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
					require.Nil(t, response.Error)
					var result coretypes.ResultTxSearch
					require.NoError(t, cmtjson.Unmarshal(response.Result, &result))
					require.Equal(t, test.count, result.TotalCount)
					require.Len(t, result.Txs, test.count)
					if test.count != 0 {
						require.EqualValues(t, tx.Hash(), result.Txs[0].Hash)
					}
				})
			}
		})
	}
}
