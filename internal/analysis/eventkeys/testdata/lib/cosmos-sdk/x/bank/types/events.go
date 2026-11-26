package types

import sdk "github.com/NibiruChain/nibiru/v2/lib/cosmos-sdk/types"

func NewEventWeiChange(reason sdk.Attribute) sdk.Event { return sdk.NewEvent("wei_change", reason) }

func NewCoinSpentEvent() sdk.Event { return sdk.NewEvent("coin_spent") }

func NewEventWeiChangeWithUniqueKeys(reason string) sdk.EventWithUniqueKeys {
	return sdk.NewEventWithUniqueKeys("wei_change", sdk.NewAttribute("reason", reason))
}

func newEvent() sdk.Event {
	return sdk.NewEvent("new") // want "use sdk.NewEventWithUniqueKeys instead of NewEvent"
}
