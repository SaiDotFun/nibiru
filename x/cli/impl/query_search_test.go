package impl

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQuerySearchTransactions(t *testing.T) {
	document := `query Find($from: Long!, $address: String!, $wantEvents: Boolean = true, $count: Int = 10) {
  results: transactions(where: {
    height: { gte: $from, lte: 102 }
    events: [
      { type: "message", attributes: { key: "action", eq: "/bank.Send" } }
      { type: "transfer", attributes: [{ key: "sender", jsonStringEq: $address }, { key: "recipient", exists: true }] }
    ]
  }, first: $count, order: DESC) {
    totalCount
    nodes {
      ...TxFields
      events @include(if: $wantEvents) { type attributes { key value } }
    }
  }
}
fragment TxFields on Transaction { where: height code }
`
	server := searchResponse(t, "tx_search", `{
  "filter":{"min_height":"100","max_height":"102","events":[
    {"type":"message","attributes":[{"key":"action","eq":"/bank.Send"}]},
    {"type":"transfer","attributes":[
      {"key":"sender","json_string_eq":"nibi1é"},
      {"key":"recipient","exists":true}
    ]}
  ]},
  "order_by":"desc","page":"1","per_page":"5"
}`, `{
  "txs":[{"hash":"AB","height":"101","index":0,"tx_result":{
    "code":0,"events":[{"type":"transfer","attributes":[{"key":"amount","value":"42unibi","index":true}]}]
  }}],
  "total_count":"1"
}`)
	got, err := callSearchCommand(t, "transactions", document, `{"from":100,"address":"nibi1é","count":5}`, server.URL)
	require.NoError(t, err)
	require.JSONEq(t, `{
  "data":{"results":{"totalCount":1,"nodes":[
    {"where":101,"code":0,"events":[{"type":"transfer","attributes":[{"key":"amount","value":"42unibi"}]}]}
  ]}}
}`, got)
}

func TestQuerySearchEvents(t *testing.T) {
	const document = `query {
  events(where: {
    height: { gte: 100, lte: 103 }, phases: [BEGIN_BLOCK, END_BLOCK], type: "wasm",
    attributes: { key: "position", eq: "" }
  }, after: "cursor", first: 2) {
    nodes { height phase txIndex txHash eventIndex type attributes { key index } }
    nextCursor
    coverage { minHeight maxHeight }
  }
}`
	server := searchResponse(t, "event_search", `{
  "filter":{"min_height":"100","max_height":"103","phases":["begin_block","end_block"],"type":"wasm",
  "attributes":[{"key":"position","eq":""}]},
  "cursor":"cursor","limit":"2"
}`, `{
  "events":[{
    "height":"101","phase":"end_block","event_index":0,"type":"wasm",
    "attributes":[{"key":"position","value":"","index":false},{"key":"position","value":"later","index":true}]
  }],
  "coverage":[{"min_height":"101","max_height":"103"}]
}`)
	got, err := callSearchCommand(t, "events", document, "{}", server.URL)
	require.NoError(t, err)
	require.JSONEq(t, `{
  "data":{"events":{
    "nodes":[{"height":101,"phase":"END_BLOCK","txIndex":null,"txHash":null,"eventIndex":0,"type":"wasm",
      "attributes":[{"key":"position","index":false},{"key":"position","index":true}]}],
    "nextCursor":null,"coverage":[{"minHeight":101,"maxHeight":103}]
  }}
}`, got)
}

func TestQuerySearchInvalidDocument(t *testing.T) {
	for _, tc := range []struct {
		name, document, vars, errorText string
	}{
		{"schema", `{ transactions(where: {}) { nodes { badField } } }`, "{}", "Cannot query field"},
		{"variables", `query($height: Long!) { transactions(where: {height: {gte: $height}}) { totalCount } }`, "{}", "must be defined"},
		{"custom scalar overflow", `{ transactions(where: {height: {gte: 9223372036854775808}}) { totalCount } }`, "{}", "Expected value of type"},
		{"wrong root", `{ events(where: {}) { nextCursor } }`, "{}", "accepts only the GraphQL field transactions"},
		{"operation selection", `query A { transactions(where: {}) { totalCount } } query B { transactions(where: {}) { totalCount } }`, "{}", "specify --operation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := callSearchCommand(t, "transactions", tc.document, tc.vars, "http://127.0.0.1:1")
			require.ErrorContains(t, err, tc.errorText)
		})
	}
}

func TestQuerySearchSchema(t *testing.T) {
	cmd := QueryCmd()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetArgs([]string{"events", "--schema"})
	require.NoError(t, cmd.ExecuteContext(t.Context()))
	require.Contains(t, out.String(), "type IndexedEvent")
	require.Contains(t, out.String(), "BEGIN_BLOCK")
}

func callSearchCommand(t *testing.T, name, document, variables, node string) (string, error) {
	t.Helper()
	cmd := QueryCmd()
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	cmd.SetIn(strings.NewReader(document))
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetArgs([]string{name, "--file", "-", "--variables", variables, "--node", node})
	err := cmd.ExecuteContext(t.Context())
	return out.String(), err
}

func searchResponse(t *testing.T, method, params, result string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var call struct {
			ID     json.RawMessage
			Method string
			Params json.RawMessage
		}
		if err := json.NewDecoder(req.Body).Decode(&call); err != nil {
			t.Errorf("decode RPC request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		require.Equal(t, method, call.Method)
		require.JSONEq(t, params, string(call.Params))
		resp := map[string]any{
			"jsonrpc": "2.0", "id": call.ID, "result": json.RawMessage(result),
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(resp))
	}))
	t.Cleanup(server.Close)
	return server
}
