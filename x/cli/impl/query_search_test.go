package impl

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NibiruChain/nibiru/v2/app"
	"github.com/NibiruChain/nibiru/v2/lib/cosmos-sdk/client"
	sdk "github.com/NibiruChain/nibiru/v2/lib/cosmos-sdk/types"
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
	got, err := callSearchCommand(t, document, `{"from":100,"address":"nibi1é","count":5}`, server.URL)
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
	got, err := callSearchCommand(t, document, "{}", server.URL)
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
		{"operation selection", `query A { transactions(where: {}) { totalCount } } query B { transactions(where: {}) { totalCount } }`, "{}", "specify --operation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := callSearchCommand(t, tc.document, tc.vars, "http://127.0.0.1:1")
			require.ErrorContains(t, err, tc.errorText)
		})
	}
}

func TestQuerySearchDefaultHelp(t *testing.T) {
	cmd := QueryCmd()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetArgs([]string{"search"})
	require.NoError(t, cmd.ExecuteContext(t.Context()))
	for _, example := range []string{"transactions(where:", "events(where:", "--gql", "--file", "--schema", "supports structured search"} {
		require.Contains(t, out.String(), example)
	}

	out.Reset()
	cmd = QueryCmd()
	cmd.SetOut(out)
	cmd.SetArgs([]string{"--help"})
	require.NoError(t, cmd.ExecuteContext(t.Context()))
	require.NotRegexp(t, `(?m)^  txs\s`, out.String())
}

func TestQuerySearchRejectsIntrospectionBeforeDispatch(t *testing.T) {
	for _, field := range []string{`__schema { queryType { name } }`, `__type(name: "Query") { name }`} {
		document := "{ transactions(where: {height: {gte: 132, lte: 132}}) { totalCount } " + field + " }"
		_, err := callSearchCommand(t, document, "{}", "http://127.0.0.1:1")
		require.ErrorContains(t, err, "Use --schema to inspect the schema")
	}

	out, err := callSearchCommand(t, "{ __typename }", "{}", "http://127.0.0.1:1")
	require.NoError(t, err)
	require.JSONEq(t, `{"data":{"__typename":"Query"}}`, out)
}

func TestQuerySearchSchema(t *testing.T) {
	cmd := QueryCmd()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetArgs([]string{"search", "--schema", "--node", "http://127.0.0.1:1"})
	require.NoError(t, cmd.ExecuteContext(t.Context()))
	require.Contains(t, out.String(), "type IndexedEvent")
	require.Contains(t, out.String(), "type TransactionPage")
	require.Contains(t, out.String(), "BEGIN_BLOCK")
}

func callSearchCommand(t *testing.T, document, variables, node string) (string, error) {
	t.Helper()
	cmd := QueryCmd()
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	cmd.SetIn(strings.NewReader(document))
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetArgs([]string{"search", "--file", "-", "--variables", variables, "--node", node})
	err := cmd.ExecuteContext(t.Context())
	return out.String(), err
}

func TestQuerySearchBothFieldsWithEveryInput(t *testing.T) {
	const document = `{
		transactions(where: {height: {gte: 132, lte: 132}}) { totalCount }
		events(where: {height: {gte: 132, lte: 132}}) { nodes { phase } }
	}`
	server, calls := searchResponses(t, map[string]searchFixture{
		"tx_search": {
			`{"filter":{"min_height":"132","max_height":"132"},"order_by":"asc","page":"1","per_page":"20"}`,
			`{"txs":[],"total_count":"1"}`,
		},
		"event_search": {
			`{"filter":{"min_height":"132","max_height":"132","attributes":[]},"limit":"20"}`,
			`{"events":[{"height":"132","phase":"end_block","event_index":0,"type":"wasm","attributes":[]}],"coverage":[]}`,
		},
	})
	path := filepath.Join(t.TempDir(), "search.graphql")
	require.NoError(t, os.WriteFile(path, []byte(document), 0o600))

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"inline", []string{"--gql", document}},
		{"file", []string{"--file", path}},
		{"stdin", []string{"--file", "-"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := QueryCmd()
			out := new(bytes.Buffer)
			cmd.SetOut(out)
			cmd.SetIn(strings.NewReader(document))
			cmd.SetArgs(append([]string{"search", "--node", server.URL}, tc.args...))
			require.NoError(t, cmd.ExecuteContext(t.Context()))
			require.JSONEq(t, `{"data":{"transactions":{"totalCount":1},"events":{"nodes":[{"phase":"END_BLOCK"}]}}}`, out.String())
		})
	}
	require.Equal(t, int32(6), calls.Load())
}

func TestQuerySearchInputFlags(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"disabled schema", []string{"--schema=false"}, "provide a GraphQL document"},
		{"node without document", []string{"--node", "http://127.0.0.1:1"}, "provide a GraphQL document"},
		{"two documents", []string{"--file", "-", "--gql", "{}"}, "none of the others can be"},
		{"schema and document", []string{"--schema", "--gql", "{}"}, "none of the others can be"},
		{"empty document", []string{"--gql", ""}, "provide a GraphQL document"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := QueryCmd()
			cmd.SilenceErrors, cmd.SilenceUsage = true, true
			cmd.SetArgs(append([]string{"search"}, tc.args...))
			require.ErrorContains(t, cmd.ExecuteContext(t.Context()), tc.want)
		})
	}
}

func TestLegacyTxsByEvents(t *testing.T) {
	server := searchResponse(t, "tx_search", `{
		"query":"tx.height=132 AND transfer.amount='12345unibi'",
		"page":"1","per_page":"20","prove":true,"order_by":""
	}`, `{"txs":[],"total_count":"0"}`)
	cfg := app.MakeEncodingConfig()
	out := new(bytes.Buffer)
	clientCtx := client.Context{}.WithCodec(cfg.Codec).WithTxConfig(cfg.TxConfig).
		WithOutput(out).WithOutputFormat("json")
	cmd := QueryCmd()
	cmd.SetArgs([]string{"txs", "--events", "tx.height=132&transfer.amount=12345unibi", "--page", "1", "--limit", "20", "--node", server.URL})
	require.NoError(t, cmd.ExecuteContext(context.WithValue(t.Context(), client.ClientContextKey, &clientCtx)))
	expected, err := cfg.Codec.MarshalJSON(sdk.NewSearchTxsResult(0, 0, 1, 20, []*sdk.TxResponse{}))
	require.NoError(t, err)
	require.JSONEq(t, string(expected), out.String())
}

type searchFixture struct {
	params string
	result string
}

func searchResponse(t *testing.T, method, params, result string) *httptest.Server {
	t.Helper()
	server, _ := searchResponses(t, map[string]searchFixture{method: {params, result}})
	return server
}

func searchResponses(t *testing.T, fixtures map[string]searchFixture) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
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
		fixture, ok := fixtures[call.Method]
		require.True(t, ok, "unexpected RPC method %s", call.Method)
		require.JSONEq(t, fixture.params, string(call.Params))
		calls.Add(1)
		resp := map[string]any{
			"jsonrpc": "2.0", "id": call.ID, "result": json.RawMessage(fixture.result),
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(resp))
	}))
	t.Cleanup(server.Close)
	return server, &calls
}
