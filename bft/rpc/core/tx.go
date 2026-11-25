package core

import (
	"context"
	"errors"
	"fmt"
	"sort"

	abci "github.com/NibiruChain/nibiru/v2/bft/abci/types"
	cmtmath "github.com/NibiruChain/nibiru/v2/bft/libs/math"
	cmtquery "github.com/NibiruChain/nibiru/v2/bft/libs/pubsub/query"
	ctypes "github.com/NibiruChain/nibiru/v2/bft/rpc/core/types"
	rpctypes "github.com/NibiruChain/nibiru/v2/bft/rpc/jsonrpc/types"
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

// TxSearch searches transaction results for predicates satisfied by the same
// indexed event occurrence. It returns a page of results and the total count.
// More: https://docs.cometbft.com/v0.37/rpc/#/Info/tx_search
func TxSearch(
	ctx *rpctypes.Context,
	query string,
	prove bool,
	pagePtr, perPagePtr *int,
	orderBy string,
) (*ctypes.ResultTxSearch, error) {
	return txSearch(ctx, query, prove, pagePtr, perPagePtr, orderBy, false)
}

// TxSearchByTransaction searches transaction results across indexed events
// within each transaction. It returns a page of results and the total count.
func TxSearchByTransaction(
	ctx *rpctypes.Context,
	query string,
	prove bool,
	pagePtr, perPagePtr *int,
	orderBy string,
) (*ctypes.ResultTxSearch, error) {
	return txSearch(ctx, query, prove, pagePtr, perPagePtr, orderBy, true)
}

type transactionScopeSearcher interface {
	SearchByTransaction(context.Context, *cmtquery.Query) ([]*abci.TxResult, error)
}

func txSearch(
	ctx *rpctypes.Context,
	query string,
	prove bool,
	pagePtr, perPagePtr *int,
	orderBy string,
	transactionScope bool,
) (*ctypes.ResultTxSearch, error) {
	// if index is disabled, return error
	if _, ok := env.TxIndexer.(*null.TxIndex); ok {
		return nil, errors.New("transaction indexing is disabled")
	} else if len(query) > maxQueryLength {
		return nil, errors.New("maximum query length exceeded")
	}

	q, err := cmtquery.New(query)
	if err != nil {
		return nil, err
	}

	search := env.TxIndexer.Search
	if transactionScope {
		indexer, ok := env.TxIndexer.(transactionScopeSearcher)
		if !ok {
			return nil, errors.New("transaction-wide event search is not supported by the configured transaction indexer")
		}
		search = indexer.SearchByTransaction
	}
	results, err := search(ctx.Context(), q)
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
