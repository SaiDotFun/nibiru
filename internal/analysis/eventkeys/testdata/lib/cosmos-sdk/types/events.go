package types

import abci "github.com/NibiruChain/nibiru/v2/bft/abci/types"

const AttributeKeySender = "sender"

type Attribute struct {
	Key   string
	Value string
}

type Event abci.Event

type EventWithUniqueKeys struct{}

func NewEvent(string, ...Attribute) Event { return Event{} }

func NewEventWithUniqueKeys(string, ...Attribute) EventWithUniqueKeys { return EventWithUniqueKeys{} }

func NewAttribute(key, value string) Attribute { return Attribute{Key: key, Value: value} }

func (Event) AppendAttributes(...Attribute) Event { return Event{} }

func (EventWithUniqueKeys) ToEvent() Event { return Event{} }
