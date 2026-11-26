package keeper

import (
	abci "github.com/NibiruChain/nibiru/v2/bft/abci/types"
	sdk "github.com/NibiruChain/nibiru/v2/lib/cosmos-sdk/types"
)

func newWasmModuleEvent(attrs []sdk.Attribute) sdk.Event {
	event := sdk.NewEvent("wasm", attrs...)
	event.AppendAttributes() // want "instead of calling AppendAttributes"
	return event
}

func newContractEvent(eventType string, attrs []abci.EventAttribute) sdk.Event {
	return sdk.Event{Type: eventType, Attributes: attrs} // want "construct Nibiru-owned events with sdk.NewEvent"
}

type SDKMessageHandler struct{}

func (SDKMessageHandler) DispatchMsg(ev abci.Event) sdk.Event { return sdk.Event(ev) }

func unrelated(ev abci.Event) sdk.Event {
	return sdk.Event(ev) // want "raw event conversions are reserved"
}

func otherConstructor(attrs []abci.EventAttribute) sdk.Event {
	return sdk.Event{Type: "wasm", Attributes: attrs} // want "construct Nibiru-owned events with sdk.NewEvent"
}
