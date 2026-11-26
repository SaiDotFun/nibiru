package core

import (
	"context"
	"errors"
	"time"

	rpctypes "github.com/NibiruChain/nibiru/v2/bft/rpc/jsonrpc/types"
	"github.com/NibiruChain/nibiru/v2/bft/state/eventsearch"
)

func eventSearchRPC(ctx *rpctypes.Context, filter *eventsearch.Filter, cursor string, limit int) (eventsearch.ObjectPage, error) {
	page, err := EventSearch(ctx, filter, cursor, limit)
	if err != nil {
		return eventsearch.ObjectPage{}, err
	}
	events := make([]eventsearch.ObjectEvent, len(page.Events))
	for i, e := range page.Events {
		events[i] = eventsearch.ObjectEvent{
			Height: e.Height, Phase: e.Phase, TxIndex: e.TxIndex, TxHash: e.TxHash, EventIndex: e.EventIndex,
			Event: map[string]eventsearch.OrderedAttributes{e.Type: eventsearch.AttributeObject(e.Attributes)},
		}
	}
	return eventsearch.ObjectPage{Events: events, NextCursor: page.NextCursor, Coverage: page.Coverage}, nil
}

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
