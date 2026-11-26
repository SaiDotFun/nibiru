package types

import sdk "github.com/NibiruChain/nibiru/v2/lib/cosmos-sdk/types"

func NewEventWeiChange(reason sdk.Attribute) sdk.Event { return sdk.NewEvent("wei_change", reason) }

func NewCoinSpentEvent() sdk.Event { return sdk.NewEvent("coin_spent") }

func newEvent() sdk.Event {
	return sdk.NewEvent("new",
		sdk.NewAttribute("reason", "a"),
		sdk.NewAttribute("reason", "b"), // want `duplicate event attribute key "reason"`
	)
}
