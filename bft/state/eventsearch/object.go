package eventsearch

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	abci "github.com/NibiruChain/nibiru/v2/bft/abci/types"
)

// ObjectPage is the RPC projection of an event search page.
type ObjectPage struct {
	Events     []ObjectEvent `json:"events"`
	NextCursor string        `json:"next_cursor,omitempty"`
	Coverage   []HeightRange `json:"coverage"`
}

// ObjectEvent contains a single event keyed by type.
type ObjectEvent struct {
	Height     int64                        `json:"height"`
	Phase      string                       `json:"phase"`
	TxIndex    *uint32                      `json:"tx_index,omitempty"`
	TxHash     string                       `json:"tx_hash,omitempty"`
	EventIndex uint32                       `json:"event_index"`
	Event      map[string]OrderedAttributes `json:"event"`
}

// OrderedAttributes preserves the member order of an event attribute object.
type OrderedAttributes []abci.EventAttribute

// AttributeObject keeps the first stored value for each key. The index and
// search filters continue to use the full original list.
func AttributeObject(attrs []abci.EventAttribute) OrderedAttributes {
	result := make(OrderedAttributes, 0, len(attrs))
	seen := make(map[string]struct{}, len(attrs))
	for _, attr := range attrs {
		if _, exists := seen[attr.Key]; !exists {
			seen[attr.Key] = struct{}{}
			result = append(result, attr)
		}
	}
	return result
}

func (attrs OrderedAttributes) MarshalJSON() ([]byte, error) {
	data := []byte{'{'}
	for i, attr := range attrs {
		if i > 0 {
			data = append(data, ',')
		}
		// Marshaling strings supplies JSON escapes even for control characters.
		key, _ := json.Marshal(attr.Key)
		value, _ := json.Marshal(attr.Value)
		data = append(data, key...)
		data = append(data, ':')
		data = append(data, value...)
	}
	return append(data, '}'), nil
}

func (attrs *OrderedAttributes) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	start, err := decoder.Token()
	if err != nil {
		return err
	}
	if start != json.Delim('{') {
		return errors.New("event attributes must be a JSON object")
	}
	result := make(OrderedAttributes, 0)
	seen := make(map[string]struct{})
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		key := token.(string) // The decoder requires string keys in an object.
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate event attribute key %q", key)
		}
		seen[key] = struct{}{}
		token, err = decoder.Token()
		if err != nil {
			return err
		}
		value, ok := token.(string)
		if !ok {
			return fmt.Errorf("event attribute %q must be a JSON string", key)
		}
		result = append(result, abci.EventAttribute{Key: key, Value: value})
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	*attrs = result
	return nil
}
