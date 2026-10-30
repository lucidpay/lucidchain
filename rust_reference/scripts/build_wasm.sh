#!/usr/bin/env bash
# Usage: source scripts/build_wasm.sh            (exports PQ_WASM_SHA256 into your shell)
#    or: ./scripts/build_wasm.sh                 (updates the Go file; the env var dies with the script)
# Set PQ_VERIFY=1 to also rebuild from scratch in a temp dir and require identical bytes.

_pq_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"   # rust_reference/

(
  # Subshell: set -e and RUSTFLAGS cannot leak into the caller's shell.
  set -euo pipefail
  root="$_pq_root"
  go_dir="$root/../x/proofs/verifiers"
  src="$root/target/wasm32-wasip1/wasm/pq_verifier_guest.wasm"
  dst="$go_dir/wasm/embed/pq_verifier.wasm"
  gofile="$go_dir/pq_wire.go"

  [ -f "$gofile" ] || { echo "missing $gofile" >&2; exit 1; }
  grep -qE '^const pqWasmPinnedImageHex = "[0-9a-f]{64}"' "$gofile" \
    || { echo "const pqWasmPinnedImageHex not found in $gofile" >&2; exit 1; }

  flags="--remap-path-prefix=$root=/build \
--remap-path-prefix=${CARGO_HOME:-$HOME/.cargo}=/cargo \
--remap-path-prefix=$(rustc --print sysroot)=/rustc-sysroot"

  build() {  # $1 = target dir
    ( cd "$root" && RUSTFLAGS="$flags" CARGO_TARGET_DIR="$1" \
        cargo build -p pq-verifier-guest --target wasm32-wasip1 --profile wasm --locked )
  }

  build "$root/target"
  mkdir -p "$(dirname "$dst")"
  cp "$src" "$dst"
  sha="$(sha256sum "$dst" | cut -d' ' -f1)"

  if [ "${PQ_VERIFY:-0}" = 1 ]; then
    tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
    build "$tmp"
    sha2="$(sha256sum "$tmp/wasm32-wasip1/wasm/pq_verifier_guest.wasm" | cut -d' ' -f1)"
    [ "$sha" = "$sha2" ] || { echo "NOT REPRODUCIBLE: $sha vs $sha2" >&2; exit 1; }
    echo "reproducible build confirmed"
  fi

  sed -i -E "s/^(const pqWasmPinnedImageHex = \")[0-9a-f]{64}(\")/\1${sha}\2/" "$gofile"
  grep -q "\"${sha}\"" "$gofile" || { echo "update of $gofile failed" >&2; exit 1; }
  echo "updated $gofile"
)
_rc=$?

if [ $_rc -eq 0 ]; then
  PQ_WASM_SHA256="$(sha256sum "$_pq_root/../x/proofs/verifiers/wasm/embed/pq_verifier.wasm" | cut -d' ' -f1)"
  export PQ_WASM_SHA256
  echo "PQ_WASM_SHA256=$PQ_WASM_SHA256"
fi
unset _pq_root
return $_rc 2>/dev/null || exit $_rc