package compat

import sdk "github.com/NibiruChain/nibiru/v2/lib/cosmos-sdk/types"

func preserve(attrs []sdk.Attribute) sdk.Event { return sdk.NewEvent("event", attrs...) }
