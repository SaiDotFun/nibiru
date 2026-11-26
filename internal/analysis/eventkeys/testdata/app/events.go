package app

import (
	abci "github.com/NibiruChain/nibiru/v2/bft/abci/types"
	renamed "github.com/NibiruChain/nibiru/v2/lib/cosmos-sdk/types"
	bank "github.com/NibiruChain/nibiru/v2/lib/cosmos-sdk/x/bank/types"
)

type eventAlias = renamed.Event

type lookalike struct{}

func (lookalike) NewEvent()         {}
func (lookalike) AppendAttributes() {}

func checked(key string, attrs []renamed.Attribute) {
	const otherAlias = "send" + "er"
	renamed.NewEventWithUniqueKeys("transfer",
		renamed.NewAttribute(renamed.AttributeKeySender, "a"),
		renamed.Attribute{Key: otherAlias, Value: "b"}, // want `duplicate event attribute key "sender"`
	)
	renamed.NewEventWithUniqueKeys("transfer", []renamed.Attribute{
		{otherAlias, "a"},
		1: {Key: "sender", Value: "b"}, // want `duplicate event attribute key "sender"`
	}...)
	renamed.NewEventWithUniqueKeys("transfer",
		renamed.Attribute{Value: "zero key"},
		renamed.Attribute{}, // want `duplicate event attribute key ""`
	)
	renamed.NewEventWithUniqueKeys("transfer", renamed.NewAttribute(key, "value"))
	renamed.NewEventWithUniqueKeys("transfer", attrs...)

	e := renamed.NewEventWithUniqueKeys("transfer", renamed.NewAttribute(otherAlias, "a")).ToEvent()
	_ = e.Attributes[0].Value
	lookalike{}.NewEvent()
	lookalike{}.AppendAttributes()
}

func raw(e renamed.Event, origin abci.Event) {
	renamed.NewEvent("transfer", // want "use sdk.NewEventWithUniqueKeys instead of NewEvent"
		renamed.NewAttribute(renamed.AttributeKeySender, "a"),
		renamed.NewAttribute("sender", "b"), // want `duplicate event attribute key "sender"`
	)
	bank.NewEventWeiChange(renamed.NewAttribute("reason", "a")) // want "use NewEventWeiChangeWithUniqueKeys"
	_ = renamed.NewEvent                                        // want "use sdk.NewEventWithUniqueKeys instead of NewEvent"
	e.AppendAttributes()                                        // want "instead of calling AppendAttributes"
	_ = renamed.Event.AppendAttributes                          // want "instead of calling AppendAttributes"
	_ = renamed.Event(origin)                                   // want "raw event conversions are reserved"
	_ = eventAlias{Type: "transfer"}                            // want "construct Nibiru-owned events"
	_ = []abci.Event{{Type: "transfer"}}                        // want "construct Nibiru-owned events"
	e.Attributes = origin.Attributes                            // want "instead of mutating raw event attributes"
	e.Attributes[0].Key = "new"                                 // want "instead of mutating raw event attributes"
	copy(e.Attributes[:], origin.Attributes)                    // want "instead of mutating raw event attributes"
	clear(e.Attributes)                                         // want "instead of mutating raw event attributes"
	_ = &e.Attributes[0]                                        // want "instead of mutating raw event attributes"
}

func mutateRawAttributes(origin *abci.Event, embedded struct{ renamed.Event }, other struct{ Attributes []string }) {
	origin.GetAttributes()[1].Key = origin.GetAttributes()[0].Key // want "instead of mutating raw event attributes"
	embedded.Attributes[1].Key = embedded.Attributes[0].Key       // want "instead of mutating raw event attributes"
	_ = origin.GetAttributes()[0].Key
	other.Attributes[0] = "unrelated"
}
