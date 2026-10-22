# Canonical build (reproducible)

Toolchain: rustc 1.99.0 stable + target wasm32-unknown-unknown (pin it in rust-toolchain.toml).
Run from the workspace root (rust/), with the committed Cargo.lock:

    RUSTFLAGS="--remap-path-prefix=$HOME/.cargo=/cargo --remap-path-prefix=$(rustc --print sysroot)=/rust --remap-path-prefix=$PWD=/src" \
    cargo build --release --locked --target wasm32-unknown-unknown -p pq-verifier-guest --no-default-features --lib

sha256(pq_verifier.wasm) = e044af832818f80a1162bb842f3cbd59986aa36fdff802ad7a6c49dbcbb9bed0   (pin as PQWasmPinnedImage)
Verified byte-identical across two different checkout directories.
Do NOT name Go files *_wasm.go / *_wasm_test.go (implicit GOARCH=wasm constraint).
