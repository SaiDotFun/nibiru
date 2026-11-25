package core

import (
	"context"
	"errors"
	"time"

	rpctypes "github.com/NibiruChain/nibiru/v2/bft/rpc/jsonrpc/types"
	"github.com/NibiruChain/nibiru/v2/bft/state/eventsearch"
)

// EventSearch returns individual events from indexed ABCI block results.
func EventSearch(ctx *rpctypes.Context, filter *eventsearch.Filter, cursor string, limit int) (eventsearch.Page, error) {
	if filter == nil {
		return eventsearch.Page{}, errors.New("event_search requires a filter")
	}
	if env.EventSearch == nil {
		return eventsearch.Page{}, errors.New("event search indexing is disabled")
	}
	searchCtx, cancel := context.WithTimeout(ctx.Context(), 5*time.Second)
	defer cancel()
	return env.EventSearch.Search(searchCtx, *filter, cursor, limit)
}
