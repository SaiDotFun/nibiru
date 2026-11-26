# Nibiru Import of CosmWasm Std

This crate comes from `packages/std` in CosmWasm `v1.5.11`, commit
`c05011a7d0ed5e55dabeb16394a93a470e0490fb`. It retains the upstream source and
license. Nibiru pins the crate version and resolves CosmWasm's sibling
crates from crates.io. The root workspace and WasmVM FFI Cargo manifests
both patch `cosmwasm-std` to this directory.

Track later changes against the pinned upstream commit. Keep upstream
imports separate from Nibiru behavior changes so reviewers can compare
source and lockfile changes independently.
