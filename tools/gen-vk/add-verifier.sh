#!/usr/bin/env bash
# add-verifier.sh: add (or replace) a verifier in genesis.json
# usage: ./add-verifier.sh [genesis.json] [vk.bin] [proof_system_id] [gas]
set -euo pipefail

GENESIS="${1:-$HOME/.lucidchain/config/genesis.json}"
VK_FILE="${2:-vk.bin}"
ID="${3:-groth16-bn254}"
GAS="${4:-200000}"

command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }
[[ -f "$GENESIS" ]] || { echo "genesis not found: $GENESIS" >&2; exit 1; }
[[ -f "$VK_FILE" ]] || { echo "vk file not found: $VK_FILE" >&2; exit 1; }

# base64 on one line (works with GNU and macOS base64)
VK_B64="$(base64 < "$VK_FILE" | tr -d '\n')"

cp "$GENESIS" "$GENESIS.bak"
TMP="$(mktemp)"

jq --arg id "$ID" --arg vk "$VK_B64" --arg gas "$GAS" '
  .app_state.proofs.verifiers =
    ((.app_state.proofs.verifiers // []) | map(select(.proof_system_id != $id)))
    + [{
        proof_system_id:  $id,
        verification_key: $vk,
        verification_gas: $gas
      }]
' "$GENESIS" > "$TMP"

mv "$TMP" "$GENESIS"
echo "verifier '$ID' written to $GENESIS (backup: $GENESIS.bak)"