# Event Attribute Keys and Wasm Compatibility

- Status: Implementation for a chain upgrade following [PR #2972][pr]
- Code scope: `lib/cosmos-sdk/types`, `x/wasm/keeper`, and IBC event forwarding
- Source of truth: the [SDK constructor][sdk], [Wasm response adapter][adapter],
  and [submessage dispatcher][dispatch]

## Decision

Require one value per attribute key in newly emitted Nibiru events and in
Wasm submessage replies. Separate events can share the same type and keys.

`sdk.NewEvent` keeps a key in its first position and uses its last value.
`EmitEvent` appends the event as supplied. The ABCI output step applies the
same key rule to raw forwarded events as it sets the index flags. The Go
analyzer flags duplicate constant keys and direct bypasses in its scoped
packages. The constructor handles keys computed at runtime.

| Source | Event Construction | Behavior |
| --- | --- | --- |
| Native Go module | `sdk.NewEvent` | One value per key, with deterministic order |
| Wasm contract response | Keeper validation, then `sdk.NewEvent` | Keys and values are trimmed and validated before duplicate keys collapse |
| IBC error forwarding | Prefix keys, then `sdk.NewEvent` | One value per prefixed key |

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
equal after trimming. The pairs `(" label ", "first")` and
`("label", "second")` yield one `label=second`. The keeper adds
`_contract_address`. Every input pair must pass validation, including
a value that a later pair would replace. Event gas uses the original contract
response. [Response conversion tests][adapter-test]

If contract A calls contract B as a Wasm submessage and requests a reply, the
dispatcher includes B's eligible events in `SubMsgResponse.Events` on success.
The dispatcher removes `message` events and sorts reply attributes by key.
A now sees only `label=second`. The reply converter applies the same rule
when a message handler passes through a raw event. [Reply dispatch test][dispatch-test]

## Contract Schemas and Upgrade

For new or revised event schemas, use one value per key. Use a named
collection field or separate events when a schema needs multiple values.
Use `sdk.NewEvent` or the typed SDK emitter for events that the Go keeper
constructs itself.

The Go Cosmos SDK, Wasm keeper, and WasmVM runtime live in the Nibiru
repository. The Rust contract library `cosmwasm-std` is a dependency. The Go
keeper enforces the convention for previously deployed bytecode as well as
new builds. Activate via a coordinated chain upgrade because a contract
that reads repeated reply attributes can change its state or return a
different result. [WasmVM layout][wasmvm] · [CosmWasm event type][std]

ABCI event attributes remain an ordered list. The search CLI and the
`event_search` RPC show the first stored value for a repeated historical key.
Filters in `event_search` can still match any stored value. The projection
leaves stored attributes and cursors intact. [Search command][search]

Each `event_search` result row contains an `event` object keyed by event type,
for example `{"transfer":{"recipient":"bob","sender":"alice"}}`. Members
appear in emitted attribute order in the JSON text. Decoding into a map may
reorder them when a client renders the object again. The search CLI requires
a node that returns this object shape. Use block or transaction results for
the complete ordered attribute list and index flags. [RPC handler][rpc]

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
[rpc]: ../bft/rpc/core/event_search.go
