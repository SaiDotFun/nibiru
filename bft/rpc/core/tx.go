package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	abci "github.com/NibiruChain/nibiru/v2/bft/abci/types"
	cmtmath "github.com/NibiruChain/nibiru/v2/bft/libs/math"
	cmtquery "github.com/NibiruChain/nibiru/v2/bft/libs/pubsub/query"
	ctypes "github.com/NibiruChain/nibiru/v2/bft/rpc/core/types"
	rpctypes "github.com/NibiruChain/nibiru/v2/bft/rpc/jsonrpc/types"
	"github.com/NibiruChain/nibiru/v2/bft/state/txindex/kv"
	"github.com/NibiruChain/nibiru/v2/bft/state/txindex/null"
	"github.com/NibiruChain/nibiru/v2/bft/types"
)

// Tx allows you to query the transaction results. `nil` could mean the
// transaction is in the mempool, invalidated, or was not sent in the first
// place.
// More: https://docs.cometbft.com/v0.37/rpc/#/Info/tx
func Tx(ctx *rpctypes.Context, hash []byte, prove bool) (*ctypes.ResultTx, error) {
	// if index is disabled, return error
	if _, ok := env.TxIndexer.(*null.TxIndex); ok {
		return nil, fmt.Errorf("transaction indexing is disabled")
	}

	r, err := env.TxIndexer.Get(hash)
	if err != nil {
		return nil, err
	}

	if r == nil {
		return nil, fmt.Errorf("tx (%X) not found", hash)
	}

	var proof types.TxProof
	if prove {
		block := env.BlockStore.LoadBlock(r.Height)
		if block != nil {
			proof = block.Data.Txs.Proof(int(r.Index))
		}
	}

	return &ctypes.ResultTx{
		Hash:     hash,
		Height:   r.Height,
		Index:    r.Index,
		TxResult: r.Result,
		Tx:       r.Tx,
		Proof:    proof,
	}, nil
}

// TxSearch searches transaction results for predicates across indexed events in
// each transaction. It returns a page of results and the total count.
// More: https://docs.cometbft.com/v0.37/rpc/#/Info/tx_search
// Inequalities on the same attribute form one range for a single value.
func TxSearch(
	ctx *rpctypes.Context,
	query string,
	prove bool,
	pagePtr, perPagePtr *int,
	orderBy string,
) (*ctypes.ResultTxSearch, error) {
	return txSearchRPC(ctx, &query, prove, pagePtr, perPagePtr, orderBy, nil)
}

func txSearchRPC(
	ctx *rpctypes.Context,
	query *string,
	prove bool,
	pagePtr, perPagePtr *int,
	orderBy string,
	filter *kv.SearchFilter,
) (*ctypes.ResultTxSearch, error) {
	if _, ok := env.TxIndexer.(*null.TxIndex); ok {
		return nil, errors.New("transaction indexing is disabled")
	}
	if (query == nil) == (filter == nil) {
		return nil, errors.New("provide exactly one of query or filter")
	}

	results, err := matchingTransactions(ctx.Context(), query, filter)
	if err != nil {
		return nil, err
	}

	// sort results (must be done before pagination)
	switch orderBy {
	case "desc":
		sort.Slice(results, func(i, j int) bool {
			if results[i].Height == results[j].Height {
				return results[i].Index > results[j].Index
			}
			return results[i].Height > results[j].Height
		})
	case "asc", "":
		sort.Slice(results, func(i, j int) bool {
			if results[i].Height == results[j].Height {
				return results[i].Index < results[j].Index
			}
			return results[i].Height < results[j].Height
		})
	default:
		return nil, errors.New("expected order_by to be either `asc` or `desc` or empty")
	}

	// paginate results
	totalCount := len(results)
	perPage := validatePerPage(perPagePtr)

	page, err := validatePage(pagePtr, perPage, totalCount)
	if err != nil {
		return nil, err
	}

	skipCount := validateSkipCount(page, perPage)
	pageSize := cmtmath.MinInt(perPage, totalCount-skipCount)

	apiResults := make([]*ctypes.ResultTx, 0, pageSize)
	for i := skipCount; i < skipCount+pageSize; i++ {
		r := results[i]

		var proof types.TxProof
		if prove {
			block := env.BlockStore.LoadBlock(r.Height)
			if block != nil {
				proof = block.Data.Txs.Proof(int(r.Index))
			}
		}

		apiResults = append(apiResults, &ctypes.ResultTx{
			Hash:     types.Tx(r.Tx).Hash(),
			Height:   r.Height,
			Index:    r.Index,
			TxResult: r.Result,
			Tx:       r.Tx,
			Proof:    proof,
		})
	}

	return &ctypes.ResultTxSearch{Txs: apiResults, TotalCount: totalCount}, nil
}

func matchingTransactions(ctx context.Context, query *string, filter *kv.SearchFilter) ([]*abci.TxResult, error) {
	if filter != nil {
		encoded, err := json.Marshal(filter)
		if err != nil {
			return nil, err
		}
		if len(encoded) > 16*1024 {
			return nil, errors.New("maximum structured transaction filter length exceeded")
		}
		indexer, ok := env.TxIndexer.(interface {
			SearchFilter(context.Context, kv.SearchFilter) ([]*abci.TxResult, error)
		})
		if !ok {
			return nil, errors.New("structured transaction search is not supported by the configured transaction indexer")
		}
		searchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return indexer.SearchFilter(searchCtx, *filter)
	}

	if len(*query) > maxQueryLength {
		return nil, errors.New("maximum query length exceeded")
	}
	q, err := cmtquery.New(*query)
	if err != nil {
		return nil, err
	}
	if indexer, ok := env.TxIndexer.(interface {
		SearchByTransaction(context.Context, *cmtquery.Query) ([]*abci.TxResult, error)
	}); ok {
		return indexer.SearchByTransaction(ctx, q)
	}
	return env.TxIndexer.Search(ctx, q)
}
