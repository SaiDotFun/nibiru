package tx

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	coretypes "github.com/NibiruChain/nibiru/v2/bft/rpc/core/types"
	"github.com/NibiruChain/nibiru/v2/lib/cosmos-sdk/client"
	txtypes "github.com/NibiruChain/nibiru/v2/lib/cosmos-sdk/types/tx"
)

type txSearchStub struct {
	client.TendermintRPC
	queries []string
}

func (s *txSearchStub) TxSearch(_ context.Context, query string, _ bool, _, _ *int, _ string) (*coretypes.ResultTxSearch, error) {
	s.queries = append(s.queries, query)
	return &coretypes.ResultTxSearch{}, nil
}

func TestGetTxsEventValidatesEachPredicate(t *testing.T) {
	const typed = `eth.evm.v1.EventEthereumTx.eth_hash = '"0x1234"'`
	const height = `tx.height=7`
	for _, test := range []struct {
		name   string
		events []string
		query  string
	}{
		{"legacy filters", []string{`message.action='/eth.evm.v1.MsgEthereumTx'`, height}, `message.action='/eth.evm.v1.MsgEthereumTx' AND ` + height},
		{"dotted typed event", []string{typed, height}, typed + " AND " + height},
		{"exists", []string{`eth.evm.v1.EventEthereumTx.eth_hash EXISTS`}, `eth.evm.v1.EventEthereumTx.eth_hash EXISTS`},
		{"contains", []string{`eth.evm.v1.EventEthereumTx.eth_hash CONTAINS '0x1234'`}, `eth.evm.v1.EventEthereumTx.eth_hash CONTAINS '0x1234'`},
		{"AND inside an operand", []string{`message.action='label AND label'`}, `message.action='label AND label'`},
		{"multiple predicates in one item", []string{typed + " AND " + height}, ""},
		{"malformed predicate", []string{`message.action='missing quote`}, ""},
		{"empty item", []string{""}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			node := new(txSearchStub)
			service := NewTxServer(client.Context{}.WithClient(node), nil, nil)
			_, err := service.GetTxsEvent(t.Context(), &txtypes.GetTxsEventRequest{Events: test.events})
			if test.query == "" {
				require.Equal(t, codes.InvalidArgument, status.Code(err))
				require.Empty(t, node.queries)
				return
			}
			require.NoError(t, err)
			require.Equal(t, []string{test.query}, node.queries)
		})
	}
}
