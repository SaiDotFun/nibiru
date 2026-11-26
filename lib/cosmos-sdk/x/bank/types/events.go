package types

import (
	"strings"

	sdk "github.com/NibiruChain/nibiru/v2/lib/cosmos-sdk/types"
)

// bank module event types
const (
	EventTypeTransfer = "transfer"

	AttributeKeyRecipient = "recipient"
	AttributeKeySender    = sdk.AttributeKeySender

	// supply and balance tracking events name and attributes
	EventTypeCoinSpent    = "coin_spent"
	EventTypeCoinReceived = "coin_received"
	EventTypeCoinMint     = "coinbase" // NOTE(fdymylja): using mint clashes with mint module event
	EventTypeCoinBurn     = "burn"

	AttributeKeySpender  = "spender"
	AttributeKeyReceiver = "receiver"
	AttributeKeyMinter   = "minter"
	AttributeKeyBurner   = "burner"

	EventTypeWeiChange          = "wei_change"
	AttributeKeyWeiChangeAddrs  = "wei_change_addrs"
	AttributeKeyWeiChangeReason = "wei_change_reason"
)

// WeiChangeReason identifies the action that changed a set of wei balances.
type WeiChangeReason string

const (
	WeiChangeSendCoins        WeiChangeReason = "bank.SendCoins"
	WeiChangeInputOutputCoins WeiChangeReason = "bank.InputOutputCoins"
	WeiChangeUndelegateCoins  WeiChangeReason = "bank.UndelegateCoins"
	WeiChangeMintCoins        WeiChangeReason = "bank.MintCoins"
	WeiChangeBurnCoins        WeiChangeReason = "bank.BurnCoins"
	WeiChangeDelegateCoins    WeiChangeReason = "bank.DelegateCoins"
	WeiChangeAddWei           WeiChangeReason = "evm.AddWei"
	WeiChangeSubWei           WeiChangeReason = "evm.SubWei"
)

func weiChangeReasonAttr(reason WeiChangeReason) sdk.Attribute {
	return sdk.NewAttribute(AttributeKeyWeiChangeReason, string(reason))
}

// These attributes support existing callers of NewEventWeiChange.
var (
	WeiChangeReason_SendCoins        = weiChangeReasonAttr(WeiChangeSendCoins)
	WeiChangeReason_InputOutputCoins = weiChangeReasonAttr(WeiChangeInputOutputCoins)
	WeiChangeReason_UndelegateCoins  = weiChangeReasonAttr(WeiChangeUndelegateCoins)
	WeiChangeReason_MintCoins        = weiChangeReasonAttr(WeiChangeMintCoins)
	WeiChangeReason_BurnCoins        = weiChangeReasonAttr(WeiChangeBurnCoins)
	WeiChangeReason_DelegateCoins    = weiChangeReasonAttr(WeiChangeDelegateCoins)
	WeiChangeReason_AddWei           = weiChangeReasonAttr(WeiChangeAddWei)
	WeiChangeReason_SubWei           = weiChangeReasonAttr(WeiChangeSubWei)
)

func WeiChangeAddrsString(addrs ...string) string {
	if len(addrs) == 0 {
		return ""
	} else if len(addrs) == 1 {
		return addrs[0]
	}
	return strings.Join(addrs, ", ")
}

// NewCoinSpentEvent constructs a new coin spent sdk.Event
func NewCoinSpentEvent(spender sdk.AccAddress, amount sdk.Coins) sdk.Event {
	return sdk.NewEvent(
		EventTypeCoinSpent,
		sdk.NewAttribute(AttributeKeySpender, spender.String()),
		sdk.NewAttribute(sdk.AttributeKeyAmount, amount.String()),
	)
}

// NewCoinReceivedEvent constructs a new coin received sdk.Event
func NewCoinReceivedEvent(receiver sdk.AccAddress, amount sdk.Coins) sdk.Event {
	return sdk.NewEvent(
		EventTypeCoinReceived,
		sdk.NewAttribute(AttributeKeyReceiver, receiver.String()),
		sdk.NewAttribute(sdk.AttributeKeyAmount, amount.String()),
	)
}

// NewCoinMintEvent construct a new coin minted sdk.Event
func NewCoinMintEvent(minter sdk.AccAddress, amount sdk.Coins) sdk.Event {
	return sdk.NewEvent(
		EventTypeCoinMint,
		sdk.NewAttribute(AttributeKeyMinter, minter.String()),
		sdk.NewAttribute(sdk.AttributeKeyAmount, amount.String()),
	)
}

// NewCoinBurnEvent constructs a new coin burned sdk.Event
func NewCoinBurnEvent(burner sdk.AccAddress, amount sdk.Coins) sdk.Event {
	return sdk.NewEvent(
		EventTypeCoinBurn,
		sdk.NewAttribute(AttributeKeyBurner, burner.String()),
		sdk.NewAttribute(sdk.AttributeKeyAmount, amount.String()),
	)
}

// NewEventWeiChange constructs a "wei_change" event.
func NewEventWeiChange(reason sdk.Attribute, addrs ...string) sdk.Event {
	return sdk.NewEvent(
		EventTypeWeiChange,
		reason,
		sdk.NewAttribute(
			AttributeKeyWeiChangeAddrs,
			WeiChangeAddrsString(addrs...),
		),
	)
}

// NewEventWeiChangeWithUniqueKeys constructs a wei change event from a reason value.
func NewEventWeiChangeWithUniqueKeys(reason WeiChangeReason, addrs ...string) sdk.EventWithUniqueKeys {
	return sdk.NewEventWithUniqueKeys(
		EventTypeWeiChange,
		weiChangeReasonAttr(reason),
		sdk.NewAttribute(AttributeKeyWeiChangeAddrs, WeiChangeAddrsString(addrs...)),
	)
}
