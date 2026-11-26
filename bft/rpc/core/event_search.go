package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	cmtjson "github.com/NibiruChain/nibiru/v2/bft/libs/json"
	rpctypes "github.com/NibiruChain/nibiru/v2/bft/rpc/jsonrpc/types"
	"github.com/NibiruChain/nibiru/v2/bft/state/eventsearch"
)

type objectSearchEvent struct {
	Height     int64                        `json:"height"`
	Phase      string                       `json:"phase"`
	TxIndex    *uint32                      `json:"tx_index,omitempty"`
	TxHash     string                       `json:"tx_hash,omitempty"`
	EventIndex uint32                       `json:"event_index"`
	Event      map[string]map[string]string `json:"event"`
}

type objectEventPage struct {
	Events     []objectSearchEvent       `json:"events"`
	NextCursor string                    `json:"next_cursor,omitempty"`
	Coverage   []eventsearch.HeightRange `json:"coverage"`
}

func eventSearchRPC(ctx *rpctypes.Context, filter *eventsearch.Filter, cursor string, limit int, format string) (json.RawMessage, error) {
	if format != "" && format != "list" && format != "object" {
		return nil, fmt.Errorf("unknown event_format %q, expected list or object", format)
	}
	page, err := EventSearch(ctx, filter, cursor, limit)
	if err != nil {
		return nil, err
	}
	if format != "object" {
		return cmtjson.Marshal(page)
	}
	events := make([]objectSearchEvent, len(page.Events))
	for i, e := range page.Events {
		events[i] = objectSearchEvent{
			Height: e.Height, Phase: e.Phase, TxIndex: e.TxIndex, TxHash: e.TxHash, EventIndex: e.EventIndex,
			Event: map[string]map[string]string{e.Type: eventsearch.AttributeObject(e.Attributes)},
		}
	}
	return cmtjson.Marshal(objectEventPage{Events: events, NextCursor: page.NextCursor, Coverage: page.Coverage})
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
