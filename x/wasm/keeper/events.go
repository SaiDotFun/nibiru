package keeper

import (
	"fmt"
	"strings"

	abci "github.com/NibiruChain/nibiru/v2/bft/abci/types"
	"github.com/NibiruChain/nibiru/v2/lib/wasmvm/wvm"

	sdkioerrors "cosmossdk.io/errors"

	sdk "github.com/NibiruChain/nibiru/v2/lib/cosmos-sdk/types"

	"github.com/NibiruChain/nibiru/v2/x/wasm/types"
)

// newWasmModuleEvent creates with wasm module event for interacting with the given contract. Adds custom attributes
// to this event.
func newWasmModuleEvent(customAttributes []wvm.EventAttribute, contractAddr sdk.AccAddress) (sdk.Events, error) {
	event, err := newContractEvent(types.WasmModuleEventType, customAttributes, contractAddr)
	if err != nil {
		return nil, err
	}

	// each wasm invocation always returns one sdk.Event
	return sdk.Events{event}, nil
}

const eventTypeMinLength = 2

// newCustomEvents converts wasmvm events from a contract response to sdk type events
func newCustomEvents(evts wvm.Events, contractAddr sdk.AccAddress) (sdk.Events, error) {
	events := make(sdk.Events, 0, len(evts))
	for _, e := range evts {
		typ := strings.TrimSpace(e.Type)
		if len(typ) <= eventTypeMinLength {
			return nil, sdkioerrors.Wrap(types.ErrInvalidEvent, fmt.Sprintf("Event type too short: '%s'", typ))
		}
		event, err := newContractEvent(types.CustomContractEventPrefix+typ, e.Attributes, contractAddr)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, nil
}

// newContractEvent preserves the ordered attributes that deployed contracts
// can observe in a submessage reply.
func newContractEvent(eventType string, customAttributes []wvm.EventAttribute, contractAddr sdk.AccAddress) (sdk.Event, error) {
	attrs := make([]abci.EventAttribute, 0, len(customAttributes)+1)
	attrs = append(attrs, abci.EventAttribute{Key: types.AttributeKeyContractAddr, Value: contractAddr.String()})
	// append attributes from wasm to the sdk.Event
	for _, l := range customAttributes {
		// ensure key and value are non-empty (and trim what is there)
		key := strings.TrimSpace(l.Key)
		if len(key) == 0 {
			return sdk.Event{}, sdkioerrors.Wrap(types.ErrInvalidEvent, fmt.Sprintf("Empty attribute key. Value: %s", l.Value))
		}
		value := strings.TrimSpace(l.Value)
		// TODO: check if this is legal in the SDK - if it is, we can remove this check
		if len(value) == 0 {
			return sdk.Event{}, sdkioerrors.Wrap(types.ErrInvalidEvent, fmt.Sprintf("Empty attribute value. Key: %s", key))
		}
		// and reserve all _* keys for our use (not contract)
		if strings.HasPrefix(key, types.AttributeReservedPrefix) {
			return sdk.Event{}, sdkioerrors.Wrap(types.ErrInvalidEvent, fmt.Sprintf("Attribute key starts with reserved prefix %s: '%s'", types.AttributeReservedPrefix, key))
		}
		attrs = append(attrs, abci.EventAttribute{Key: key, Value: value})
	}
	return sdk.Event{Type: eventType, Attributes: attrs}, nil
}
