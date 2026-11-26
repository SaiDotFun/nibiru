# Event Attribute Keys and Wasm Compatibility

- Status: Proposed follow-up to [PR #2972][pr]
- Code scope: `lib/cosmos-sdk/types`, `x/wasm/keeper`, and IBC event forwarding
- Source of truth: the [SDK constructor][sdk], [Wasm response adapter][adapter],
  and [submessage dispatcher][dispatch]

## Decision

Require one value per attribute key when Nibiru code constructs an event
with `sdk.NewEvent`. Preserve the validated attribute list when forwarding
events supplied by a contract or another module. Separate events can share
the same type and the same keys.

`sdk.NewEvent` keeps a key in the position where it first appeared and uses
its last value. `EmitEvent` appends the event as supplied. Protobuf-typed
SDK events already construct attributes from an object with one value per
field. The Go analyzer checks duplicate constant keys and direct bypasses
in its scoped packages. The constructor handles keys computed at runtime.

| Source | Event Construction | Compatibility |
| --- | --- | --- |
| Native Go module | `sdk.NewEvent` | One value per key, with deterministic order |
| Wasm contract response | Keeper adapter `newContractEvent` | Existing trimming, validation, and repeated values |
| IBC error forwarding | Copy and prefix the supplied attributes | Existing values and order, including repetitions |

## A Code Upload and a Contract Reply

`store_code` is an event about a code upload. The Go keeper stores the
bytecode, assigns a code ID, and emits `code_checksum` followed by `code_id`.
The previous code called `AppendAttributes` for each required capability and
discarded the returned event. The emitted event already had just the two
fields. Removing the discarded call preserves the output and keeps `code_id`
last for scripts that read it by position. [Code-upload test][store-test]

An executing contract supplies a list of key-value pairs. The keeper trims
leading and trailing whitespace before validating each key and value. A
contract can supply the same key twice directly, or two spellings can become
equal after trimming. For example, `(" label ", "first")` followed by
`("label", "second")` becomes `("label", "first")` followed by
`("label", "second")`. The keeper adds `_contract_address` and preserves
both values. [Response conversion tests][adapter-test]

When contract A calls contract B as a submessage and requests a reply, the
dispatcher includes B's eligible events in `SubMsgResponse.Events` on success.
For a Wasm submessage, the dispatcher removes `message` events and sorts
attributes stably by key. The two `label` entries keep their relative order.
A can read the first value, both values, or their count and use that result
to update state. Changing the converter to use `sdk.NewEvent` would give A
only `label=second`. [Reply dispatch test][dispatch-test]

## Follow-up

- Use `sdk.NewEvent` or the typed SDK emitter for new events the Wasm keeper
  owns. Keep the contract response adapter for lists supplied by guests.
- For new or revised Nibiru-owned contract event schemas, define one value
  per key in the producer and test the emitted attributes. Use a named
  collection field or separate events when a new schema needs multiple
  values. Keep existing event schemas compatible with their readers.
- Add a keeper integration test in which one contract returns two values for
  a key and another reads them in `reply`. Compare the reply input and
  emitted attributes with literal expectations. Keep the `store_code` and
  IBC forwarding regressions as separate checks.

The Go Cosmos SDK, Wasm keeper, and WasmVM runtime are in the Nibiru
repository. The Rust contract library `cosmwasm-std` is a dependency. Changes
to its builders reach contracts compiled against the changed crate. Changing
the Go adapter reaches already-deployed contracts, so any future change to
the reply semantics belongs in an explicit chain upgrade with contract
compatibility tests. [WasmVM layout][wasmvm] · [CosmWasm event type][std]

ABCI event attributes remain an ordered list. The event search CLI's compact
object shows the first stored value for a repeated key. That projection does
not change what the node stores or what a contract receives. Historical
records can still carry repeated keys. [Search command][search]

[pr]: https://github.com/SaiDotFun/nibiru/pull/2972
[sdk]: ../lib/cosmos-sdk/types/events.go
[adapter]: ../x/wasm/keeper/events.go
[dispatch]: ../x/wasm/keeper/msg_dispatcher.go
[store-test]: ../x/wasm/keeper/keeper_unit_test.go
[adapter-test]: ../x/wasm/keeper/events_unit_test.go
[dispatch-test]: ../x/wasm/keeper/msg_dispatcher_unit_test.go
[wasmvm]: ../lib/wasmvm/README.md
[std]: https://github.com/CosmWasm/cosmwasm/blob/v1.5.11/packages/std/src/results/events.rs
[search]: ../x/cli/impl/query_search.go
