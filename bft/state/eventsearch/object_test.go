package eventsearch

import (
	"encoding/json"
	"testing"

	abci "github.com/NibiruChain/nibiru/v2/bft/abci/types"
	"github.com/stretchr/testify/require"
)

func TestOrderedAttributeObject(t *testing.T) {
	attrs := AttributeObject([]abci.EventAttribute{
		{Key: "z\"", Value: "first\x01"},
		{Key: "a", Value: ""},
		{Key: "z\"", Value: "discarded"},
	})
	data, err := json.Marshal(attrs)
	require.NoError(t, err)
	require.Equal(t, `{"z\"":"first\u0001","a":""}`, string(data))

	var decoded OrderedAttributes
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, attrs, decoded)
	require.NoError(t, json.Unmarshal([]byte(`{}`), &decoded))
	data, err = json.Marshal(decoded)
	require.NoError(t, err)
	require.Equal(t, `{}`, string(data))
}
