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
	"github.com/NibiruChain/nibiru/v2/bft/state/txindex"
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

func TestTxSearchStructuredHTTP(t *testing.T) {
	const method = "tx_search"
	const typed = `eth.evm.v1.EventEthereumTx.eth_hash = '"0x1234"'`
	const query = typed + ` AND transfer.amount = '42unibi'`
	index := kv.NewTxIndex(dbm.NewMemDB())
	older := types.Tx("older transaction")
	newer := types.Tx("newer transaction")
	for i, tx := range []types.Tx{older, newer} {
		require.NoError(t, index.Index(&abci.TxResult{
			Height: int64(i + 7),
			Tx:     tx,
			Result: abci.ResponseDeliverTx{Events: []abci.Event{
				{Type: "eth.evm.v1.EventEthereumTx", Attributes: []abci.EventAttribute{
					{Key: "eth_hash", Value: `"0x1234"`, Index: true},
				}},
				{Type: "transfer", Attributes: []abci.EventAttribute{
					{Key: "amount", Value: "42unibi", Index: true},
					{Key: "sender", Value: "Alice", Index: true},
					{Key: "recipient", Value: "Bob", Index: true},
				}},
				{Type: "transfer", Attributes: []abci.EventAttribute{
					{Key: "sender", Value: "Carol", Index: true},
					{Key: "recipient", Value: "Dave", Index: true},
				}},
				{Type: "example.v1.TypedEvent", Attributes: []abci.EventAttribute{
					{Key: "text.value", Value: `"L'étoile/東京 \u003c3"`, Index: true},
				}},
				{Type: "pending_ethereum_tx", Attributes: []abci.EventAttribute{
					{Key: "eth_hash", Value: fmt.Sprintf("0x%064x", i+1), Index: true},
					{Key: "index", Value: "0", Index: true},
				}},
			}},
		}))
	}

	previous := env
	SetEnvironment(&Environment{TxIndexer: index})
	t.Cleanup(func() { SetEnvironment(previous) })
	mux := http.NewServeMux()
	rpc.RegisterRPCFuncs(mux, map[string]*rpc.RPCFunc{method: Routes[method]}, log.NewNopLogger())

	post := func(t *testing.T, method string, params any) *http.Request {
		t.Helper()
		body, err := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "method": method, "id": 1, "params": params,
		})
		require.NoError(t, err)
		return httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	}
	call := func(t *testing.T, request *http.Request) rpctypes.RPCResponse {
		t.Helper()
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		var response rpctypes.RPCResponse
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
		return response
	}
	encoded, err := json.Marshal(query)
	require.NoError(t, err)
	params := url.Values{"query": {string(encoded)}}
	structured := func(recipient string) map[string]any {
		return map[string]any{"filter": map[string]any{
			"min_height": "7", "max_height": "7", "events": []map[string]any{
				{"type": "example.v1.TypedEvent", "attributes": []map[string]any{
					{"key": "text.value", "json_string_eq": "L'étoile/東京 <3"},
				}},
				{"type": "transfer", "attributes": []map[string]any{
					{"key": "sender", "eq": "Alice"}, {"key": "recipient", "eq": recipient},
				}},
			},
		}}
	}
	for _, test := range []struct {
		name    string
		request *http.Request
		total   int
		txs     []types.Tx
	}{
		{"positional cross-event", post(t, method, []any{"transfer.sender='Alice' AND transfer.recipient='Dave'", false, nil, nil, "asc"}), 2, []types.Tx{older, newer}},
		{"positional single-event", post(t, method, []any{typed, false, nil, nil, "asc"}), 2, []types.Tx{older, newer}},
		{"positional paging", post(t, method, []any{query, false, "1", "1", "desc"}), 2, []types.Tx{newer}},
		{"named paging", post(t, method, map[string]any{
			"query": query, "page": "2", "per_page": "1", "order_by": "desc",
		}), 2, []types.Tx{older}},
		{"EVM fallback by hash", post(t, method, map[string]any{
			"query": fmt.Sprintf("pending_ethereum_tx.eth_hash='0x%064x'", 2),
		}), 1, []types.Tx{newer}},
		{"EVM fallback by height and index", post(t, method, map[string]any{
			"query": "tx.height=7 AND pending_ethereum_tx.index=0",
		}), 1, []types.Tx{older}},
		{"GET", httptest.NewRequest(http.MethodGet, "/"+method+"?"+params.Encode(), nil), 2, []types.Tx{older, newer}},
		{"structured filters match across groups", post(t, method, structured("Bob")), 1, []types.Tx{older}},
		{"structured conditions require the same event", post(t, method, structured("Dave")), 0, nil},
		{"structured hash lookup", post(t, method, map[string]any{"filter": map[string]any{
			"hash": fmt.Sprintf("0x%X", newer.Hash()),
		}}), 1, []types.Tx{newer}},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := call(t, test.request)
			require.Nil(t, response.Error)
			var result coretypes.ResultTxSearch
			require.NoError(t, cmtjson.Unmarshal(response.Result, &result))
			require.Equal(t, test.total, result.TotalCount)
			require.Len(t, result.Txs, len(test.txs))
			for i, tx := range test.txs {
				require.EqualValues(t, tx.Hash(), result.Txs[i].Hash)
			}
		})
	}

	t.Run("invalid query", func(t *testing.T) {
		response := call(t, post(t, method, map[string]string{"query": "transfer.amount ="}))
		require.NotNil(t, response.Error)
		require.Contains(t, response.Error.Data, "parse error")
	})

	t.Run("unsupported backend", func(t *testing.T) {
		// The wrapper exposes exactly the base indexer interface.
		SetEnvironment(&Environment{TxIndexer: struct{ txindex.TxIndexer }{index}})
		response := call(t, post(t, method, structured("Bob")))
		require.NotNil(t, response.Error)
		require.Equal(t, "structured transaction search is not supported by the configured transaction indexer", response.Error.Data)
	})

	t.Run("structured and legacy parameters are mutually exclusive", func(t *testing.T) {
		params := structured("Bob")
		params["query"] = query
		response := call(t, post(t, method, params))
		require.NotNil(t, response.Error)
		require.Equal(t, "provide exactly one of query or filter", response.Error.Data)
	})

	t.Run("structured filter is invalid in positional JSON-RPC", func(t *testing.T) {
		response := call(t, post(t, method, []any{nil, false, nil, nil, "asc", structured("Bob")["filter"]}))
		require.NotNil(t, response.Error)
		require.Equal(t, -32602, response.Error.Code)
	})

	t.Run("neither query nor filter was supplied", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"jsonrpc":"2.0","method":"tx_search","id":1}`))
		response := call(t, request)
		require.NotNil(t, response.Error)
		require.Equal(t, "provide exactly one of query or filter", response.Error.Data)
	})
}
