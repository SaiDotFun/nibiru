package impl

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	abci "github.com/NibiruChain/nibiru/v2/bft/abci/types"
	coretypes "github.com/NibiruChain/nibiru/v2/bft/rpc/core/types"
	rpcclient "github.com/NibiruChain/nibiru/v2/bft/rpc/jsonrpc/client"
	"github.com/NibiruChain/nibiru/v2/bft/state/eventsearch"
	"github.com/NibiruChain/nibiru/v2/bft/state/txindex/kv"
	"github.com/NibiruChain/nibiru/v2/lib/cosmos-sdk/client"
	"github.com/NibiruChain/nibiru/v2/lib/cosmos-sdk/client/flags"
	"github.com/spf13/cobra"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/validator"
)

//go:embed query_search.graphqls
var searchSchemaSource string

var searchSchema = gqlparser.MustLoadSchema(&ast.Source{Name: "query_search.graphqls", Input: searchSchemaSource})

const maxSearchDocumentSize = 1 << 20

func querySearchCmd() *cobra.Command {
	var gql, file, variables, operation string
	var showSchema bool
	cmd := &cobra.Command{
		Use:   "search",
		Short: "Find transactions and events with GraphQL",
		Long: `Search indexed chain data with GraphQL.
The transactions field returns matching transaction results.
The events field returns individual events, including begin-block and end-block events.
A document can request both fields. Use --file - to read from stdin.
Use --schema to see filters and result fields without contacting a node.`,
		Example: `  # At an example block height, get each transaction's hash and result code.
  nibiru query search --gql '{
    transactions(where: {height: {gte: 132, lte: 132}}) {
      nodes { hash code }
    }
  }'

  # Get transaction and end-block events and check indexed height coverage.
  nibiru query search --gql '{
    events(where: {height: {gte: 132, lte: 132}, phases: [TX, END_BLOCK]}) {
      nodes { type phase txHash attributes { key value } }
      nextCursor
      coverage { minHeight maxHeight }
    }
  }'

  nibiru query search --file search.graphql
  nibiru query search --schema`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if showSchema {
				_, err := io.WriteString(cmd.OutOrStdout(), searchSchemaSource)
				return err
			}
			if cmd.Flags().NFlag() == 0 {
				return cmd.Help()
			}
			document := gql
			if cmd.Flags().Changed("file") {
				data, err := readSearchDocument(cmd, file)
				if err != nil {
					return err
				}
				document = string(data)
			}
			if strings.TrimSpace(document) == "" {
				return errors.New("provide a GraphQL document with --gql or --file, or use --schema")
			}
			if len(document) > maxSearchDocumentSize {
				return errors.New("GraphQL document exceeds 1 MiB")
			}
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			rpc, err := rpcclient.New(clientCtx.NodeURI)
			if err != nil {
				return err
			}
			data, err := executeSearchDocument(cmd.Context(), rpc, document, variables, operation)
			if err != nil {
				return err
			}
			return printJSON(cmd, map[string]any{"data": data})
		},
	}
	cmd.Flags().String(flags.FlagNode, "tcp://localhost:26657", "BFT RPC endpoint")
	cmd.Flags().StringVar(&gql, "gql", "", "Inline GraphQL document")
	cmd.Flags().StringVar(&file, "file", "", "GraphQL document file, or - to read from stdin")
	cmd.Flags().StringVar(&variables, "variables", "{}", "GraphQL variables as a JSON object")
	cmd.Flags().StringVar(&operation, "operation", "", "Operation name for a document with several operations")
	cmd.Flags().BoolVar(&showSchema, "schema", false, "Print the GraphQL schema")
	cmd.MarkFlagsMutuallyExclusive("gql", "file", "schema")
	return cmd
}

func readSearchDocument(cmd *cobra.Command, file string) ([]byte, error) {
	var reader io.Reader = cmd.InOrStdin()
	if file != "-" {
		f, err := os.Open(file)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		reader = f
	}
	return io.ReadAll(io.LimitReader(reader, maxSearchDocumentSize+1))
}

func executeSearchDocument(ctx context.Context, rpc rpcclient.Caller, document, variables, operation string) (map[string]any, error) {
	doc, errs := gqlparser.LoadQuery(searchSchema, document)
	if len(errs) > 0 {
		return nil, errs
	}

	op := doc.Operations.ForName(operation)
	if op == nil {
		if operation == "" {
			return nil, errors.New("specify --operation when the document contains multiple operations")
		}
		return nil, fmt.Errorf("GraphQL operation %q was not found", operation)
	}
	var input map[string]any
	dec := json.NewDecoder(strings.NewReader(variables))
	dec.UseNumber()
	if err := dec.Decode(&input); err != nil || input == nil {
		return nil, errors.New("--variables must be a JSON object")
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, errors.New("--variables must contain one JSON object")
	}
	bound, err := validator.VariableValues(searchSchema, op, input)
	if err != nil {
		return nil, err
	}

	fields := searchFields(op.SelectionSet, "Query", bound)
	for _, field := range fields {
		switch field.Name {
		case "transactions", "events", "__typename":
		default:
			return nil, fmt.Errorf("unsupported GraphQL field %q. Use --schema to inspect the schema", field.Name)
		}
	}
	data := make(map[string]any, len(fields))
	for _, field := range fields {
		if field.Name == "__typename" {
			data[field.Alias] = "Query"
			continue
		}
		result, err := executeSearchField(ctx, rpc, field, bound)
		if err != nil {
			return nil, err
		}
		data[field.Alias] = result
	}
	return data, nil
}

// searchFields expands enabled fragments and merges field selections by their
// response names. The schema only uses concrete object types.
func searchFields(selections ast.SelectionSet, typ string, vars map[string]any) []*ast.Field {
	fields := make([]*ast.Field, 0, len(selections))
	byAlias := make(map[string]*ast.Field)
	var collect func(ast.SelectionSet)
	collect = func(set ast.SelectionSet) {
		for _, selection := range set {
			switch s := selection.(type) {
			case *ast.Field:
				if !searchIncludes(s.Directives, vars) {
					continue
				}
				if found := byAlias[s.Alias]; found != nil {
					found.SelectionSet = append(found.SelectionSet, s.SelectionSet...)
					continue
				}
				field := *s
				field.SelectionSet = append(ast.SelectionSet(nil), s.SelectionSet...)
				byAlias[s.Alias] = &field
				fields = append(fields, &field)
			case *ast.FragmentSpread:
				if s.Definition.TypeCondition == typ && searchIncludes(s.Directives, vars) {
					collect(s.Definition.SelectionSet)
				}
			case *ast.InlineFragment:
				if (s.TypeCondition == "" || s.TypeCondition == typ) && searchIncludes(s.Directives, vars) {
					collect(s.SelectionSet)
				}
			}
		}
	}
	collect(selections)
	return fields
}

func searchIncludes(directives ast.DirectiveList, vars map[string]any) bool {
	for _, d := range directives {
		switch d.Name {
		case "skip":
			if enabled, _ := d.ArgumentMap(vars)["if"].(bool); enabled {
				return false
			}
		case "include":
			if enabled, _ := d.ArgumentMap(vars)["if"].(bool); !enabled {
				return false
			}
		}
	}
	return true
}

func projectSearchResult(value any, selections ast.SelectionSet, typ string, vars map[string]any) any {
	switch v := value.(type) {
	case []map[string]any:
		rows := make([]any, 0, len(v))
		for _, row := range v {
			rows = append(rows, projectSearchResult(row, selections, typ, vars))
		}
		return rows
	case map[string]any:
		out := make(map[string]any)
		for _, field := range searchFields(selections, typ, vars) {
			if field.Name == "__typename" {
				out[field.Alias] = typ
				continue
			}
			out[field.Alias] = projectSearchResult(v[field.Name], field.SelectionSet, field.Definition.Type.Name(), vars)
		}
		return out
	default:
		return value
	}
}

// coerceSearchInput applies singleton list coercion to inline GraphQL values,
// which gqlparser validates but does not normalize in the document's AST.
func coerceSearchInput(value any, typ *ast.Type) any {
	if value == nil {
		return nil
	}
	if typ.Elem != nil {
		list, ok := value.([]any)
		if !ok {
			list = []any{value}
		}
		coerced := make([]any, len(list))
		for i, item := range list {
			coerced[i] = coerceSearchInput(item, typ.Elem)
		}
		return coerced
	}
	if input, ok := value.(map[string]any); ok && searchSchema.Types[typ.NamedType].Kind == ast.InputObject {
		coerced := make(map[string]any, len(input))
		for _, field := range searchSchema.Types[typ.NamedType].Fields {
			if val, provided := input[field.Name]; provided {
				coerced[field.Name] = coerceSearchInput(val, field.Type)
			}
		}
		return coerced
	}
	return value
}

type searchHeightInput struct {
	GTE int64
	LTE int64
}

type searchEventInput struct {
	Type       string
	Attributes []struct {
		Key          string
		Eq           *string
		JSONStringEq *string
		Exists       bool
	}
}

func (input searchEventInput) wireAttributes() []eventsearch.AttributeFilter {
	attrs := make([]eventsearch.AttributeFilter, 0, len(input.Attributes))
	for _, attr := range input.Attributes {
		attrs = append(attrs, eventsearch.AttributeFilter(attr))
	}
	return attrs
}

// searchFieldArguments is the error-returning equivalent of Field.ArgumentMap.
// In particular, parsing a custom scalar can fail after GraphQL validation.
func searchFieldArguments(field *ast.Field, vars map[string]any) (map[string]any, error) {
	args := make(map[string]any)
	for _, def := range field.Definition.Arguments {
		value := def.DefaultValue
		if given := field.Arguments.ForName(def.Name); given != nil {
			if given.Value.Kind != ast.Variable {
				value = given.Value
			} else if v, ok := vars[given.Value.Raw]; ok {
				args[def.Name] = coerceSearchInput(v, def.Type)
				continue
			}
		}
		if value != nil {
			v, err := value.Value(vars)
			if err != nil {
				return nil, fmt.Errorf("GraphQL argument %s: %w", def.Name, err)
			}
			args[def.Name] = coerceSearchInput(v, def.Type)
		}
	}
	return args, nil
}

func executeSearchField(ctx context.Context, rpc rpcclient.Caller, field *ast.Field, vars map[string]any) (any, error) {
	args, err := searchFieldArguments(field, vars)
	if err != nil {
		return nil, err
	}
	where, err := json.Marshal(args["where"])
	if err != nil {
		return nil, err
	}
	var result map[string]any
	switch field.Name {
	case "transactions":
		result, err = querySearchTransactions(ctx, rpc, where, args)
	case "events":
		result, err = querySearchEvents(ctx, rpc, where, args)
	}
	if err != nil {
		return nil, err
	}
	return projectSearchResult(result, field.SelectionSet, field.Definition.Type.Name(), vars), nil
}

func querySearchTransactions(ctx context.Context, rpc rpcclient.Caller, where []byte, args map[string]any) (map[string]any, error) {
	var input struct {
		Height searchHeightInput
		Hash   string
		Events []searchEventInput
	}
	if err := json.Unmarshal(where, &input); err != nil {
		return nil, fmt.Errorf("invalid transaction filter: %w", err)
	}
	filter := kv.SearchFilter{MinHeight: input.Height.GTE, MaxHeight: input.Height.LTE, Hash: input.Hash}
	for _, event := range input.Events {
		filter.Events = append(filter.Events, kv.EventGroupFilter{Type: event.Type, Attributes: event.wireAttributes()})
	}
	params := map[string]any{"filter": filter}
	if err := copySearchIntParam(params, args, "first", "per_page"); err != nil {
		return nil, err
	}
	if err := copySearchIntParam(params, args, "page", "page"); err != nil {
		return nil, err
	}
	if order, ok := args["order"].(string); ok {
		params["order_by"] = strings.ToLower(order)
	}
	var result coretypes.ResultTxSearch
	if _, err := rpc.Call(ctx, "tx_search", params, &result); err != nil {
		return nil, err
	}

	nodes := make([]map[string]any, 0, len(result.Txs))
	for _, tx := range result.Txs {
		if tx == nil {
			return nil, errors.New("tx_search returned a null transaction")
		}
		events := make([]map[string]any, 0, len(tx.TxResult.Events))
		for _, event := range tx.TxResult.Events {
			events = append(events, map[string]any{"type": event.Type, "attributes": searchAttributes(event.Attributes)})
		}
		nodes = append(nodes, map[string]any{
			"hash": tx.Hash.String(), "height": tx.Height, "index": tx.Index, "code": tx.TxResult.Code,
			"gasWanted": tx.TxResult.GasWanted, "gasUsed": tx.TxResult.GasUsed, "events": events,
		})
	}
	return map[string]any{"nodes": nodes, "totalCount": result.TotalCount}, nil
}

func querySearchEvents(ctx context.Context, rpc rpcclient.Caller, where []byte, args map[string]any) (map[string]any, error) {
	var input struct {
		Height searchHeightInput
		Phases []string
		searchEventInput
	}
	if err := json.Unmarshal(where, &input); err != nil {
		return nil, fmt.Errorf("invalid event filter: %w", err)
	}
	filter := eventsearch.Filter{
		MinHeight: input.Height.GTE, MaxHeight: input.Height.LTE,
		Type: input.Type, Attributes: input.wireAttributes(),
	}
	for _, phase := range input.Phases {
		filter.Phases = append(filter.Phases, strings.ToLower(phase))
	}
	params := map[string]any{"filter": filter}
	if err := copySearchIntParam(params, args, "first", "limit"); err != nil {
		return nil, err
	}
	if cursor, ok := args["after"].(string); ok {
		params["cursor"] = cursor
	}
	var result eventsearch.Page
	if _, err := rpc.Call(ctx, "event_search", params, &result); err != nil {
		return nil, err
	}

	nodes := make([]map[string]any, 0, len(result.Events))
	for _, event := range result.Events {
		var hash any
		if event.TxHash != "" {
			hash = event.TxHash
		}
		nodes = append(nodes, map[string]any{
			"height": event.Height, "phase": strings.ToUpper(event.Phase), "txIndex": event.TxIndex,
			"txHash": hash, "eventIndex": event.EventIndex, "type": event.Type,
			"attributes": searchAttributes(event.Attributes),
		})
	}
	coverage := make([]map[string]any, 0, len(result.Coverage))
	for _, interval := range result.Coverage {
		coverage = append(coverage, map[string]any{"minHeight": interval.MinHeight, "maxHeight": interval.MaxHeight})
	}
	var nextCursor any
	if result.NextCursor != "" {
		nextCursor = result.NextCursor
	}
	return map[string]any{"nodes": nodes, "nextCursor": nextCursor, "coverage": coverage}, nil
}

func copySearchIntParam(params, args map[string]any, argument, parameter string) error {
	value := args[argument]
	if value == nil {
		return nil
	}
	// Enforce the GraphQL Int range even when variables arrive as JSON strings.
	number, err := strconv.ParseInt(fmt.Sprint(value), 10, 32)
	if err != nil || number < 1 {
		return fmt.Errorf("GraphQL argument %s must be a positive Int", argument)
	}
	params[parameter] = int(number)
	return nil
}

func searchAttributes(attrs []abci.EventAttribute) []map[string]any {
	out := make([]map[string]any, 0, len(attrs))
	for _, attr := range attrs {
		out = append(out, map[string]any{"key": attr.Key, "value": attr.Value, "index": attr.Index})
	}
	return out
}
