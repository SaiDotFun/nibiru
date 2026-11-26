package unresolved

import sdk "github.com/NibiruChain/nibiru/v2/lib/cosmos-sdk/types"

func assemble(key string, attr sdk.Attribute, attrs []sdk.Attribute) {
	sdk.NewEvent("event", sdk.NewAttribute(key, "value")) // want "cannot resolve event attribute key statically"
	sdk.NewEvent("event", attr)                           // want "cannot resolve event attribute key statically"
	sdk.NewEvent("event", attrs...)                       // want "cannot resolve event attribute key statically"
}
