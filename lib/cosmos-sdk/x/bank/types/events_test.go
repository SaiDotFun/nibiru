package types

import (
	"testing"

	"github.com/stretchr/testify/require"

	abci "github.com/NibiruChain/nibiru/v2/bft/abci/types"
	sdk "github.com/NibiruChain/nibiru/v2/lib/cosmos-sdk/types"
)

func TestWeiChangeEventWithUniqueKeysMatchesLegacyWire(t *testing.T) {
	for _, test := range []struct {
		reason WeiChangeReason
		legacy sdk.Attribute
	}{
		{WeiChangeSendCoins, WeiChangeReason_SendCoins},
		{WeiChangeInputOutputCoins, WeiChangeReason_InputOutputCoins},
		{WeiChangeUndelegateCoins, WeiChangeReason_UndelegateCoins},
		{WeiChangeMintCoins, WeiChangeReason_MintCoins},
		{WeiChangeBurnCoins, WeiChangeReason_BurnCoins},
		{WeiChangeDelegateCoins, WeiChangeReason_DelegateCoins},
		{WeiChangeAddWei, WeiChangeReason_AddWei},
		{WeiChangeSubWei, WeiChangeReason_SubWei},
	} {
		t.Run(string(test.reason), func(t *testing.T) {
			for _, addrs := range [][]string{nil, {"first"}, {"first", "second"}} {
				legacy := abci.Event(NewEventWeiChange(test.legacy, addrs...))
				unique := abci.Event(NewEventWeiChangeWithUniqueKeys(test.reason, addrs...).ToEvent())
				want, err := legacy.Marshal()
				require.NoError(t, err)
				got, err := unique.Marshal()
				require.NoError(t, err)
				require.Equal(t, want, got)
			}
		})
	}
}
