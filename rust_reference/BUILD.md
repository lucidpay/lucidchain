# Canonical build (reproducible)

Toolchain: rustc 1.99.0 stable + target wasm32-wasip1 (pin it in rust-toolchain.toml).
Run from the workspace root (rust_reference/), with the committed Cargo.lock:

    RUSTFLAGS="--remap-path-prefix=$HOME/.cargo=/cargo --remap-path-prefix=$(rustc --print sysroot)=/rust --remap-path-prefix=$PWD=/src" \
    cargo build --release --locked --target wasm32-wasip1 -p pq-verifier-guest --no-default-features --lib

sha256(pq_verifier.wasm) = <REGENERATE: rebuild with the command above after the Stage 2 salt patch>
(pin as PQWasmPinnedImage)

The previous value, e044af832818f80a1162bb842f3cbd59986aa36fdff802ad7a6c49dbcbb9bed0,
is the pre-Stage-2 image and is now stale: the guest links the av-zone verifier,
whose AIR and vk_id changed. Verify the new build is byte-identical across two
different checkout directories before pinning it.

Do NOT name Go files *_wasm.go / *_wasm_test.go (implicit GOARCH=wasm constraint).

## Release checklist after any change to av-zone (AIR, constants, FRI shape, Plonky3)

1. Bump `VERSION` in av-zone/src/config.rs if any constraint changed (the vk_id
   hashes the descriptor, not the constraint logic).
2. `cargo test --workspace` and `cargo test --workspace --release`. Both must
   pass: in release the verifier itself must reject the cheating traces.
3. Update `GOLDEN_VK_ID` in av-zone/tests/av_zone.rs (run `vk_id_is_stable` with
   `--nocapture`) and, if the commitment changed, the known-answer vectors in
   av-zone/src/commit.rs.
4. Regenerate the embedded self-check proofs with
   pq-verifier-guest/examples/gen_fixtures.rs (assumed:
   `cargo run --release -p pq-verifier-guest --example gen_fixtures`; check its
   output directory) and re-run pq-verifier-guest/tests/serve_frame.rs. Fixtures
   use a fixed TEST salt and test entropy: never use them for real trips.
5. Rebuild the wasm image with the canonical command, then update
   `PQWasmPinnedImage`.
6. Update the vk_id allowlist compiled into the Go binary. It must equal the id
   printed by av-zone-prove and pinned in `GOLDEN_VK_ID`. Current Stage 2 id:
   b81d579743df1e93a5f3a28f7b47392c2b075fab50dd3018024c62263bd9dbb8