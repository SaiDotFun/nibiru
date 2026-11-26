package types

type EventAttribute struct {
	Key   string
	Value string
}

type Event struct {
	Type       string
	Attributes []EventAttribute
}

func (event *Event) GetAttributes() []EventAttribute {
	return event.Attributes
}
