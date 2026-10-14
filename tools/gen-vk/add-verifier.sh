#!/usr/bin/env bash
# add-verifier.sh: add (or replace) a verifier in genesis.json
# usage: ./add-verifier.sh [genesis.json] [vk.bin] [proof_system_id] [gas] [claim_id] [num_public_inputs]
set -euo pipefail

GENESIS="${1:-$HOME/.lucidchain/config/genesis.json}"
VK_FILE="${2:-vk.bin}"
ID="${3:-groth16-bn254}"
GAS="${4:-200000}"
CLAIM="${5:-geofence.v1}"
NUM_PUBLIC="${6:-7}"   # geofence circuit: 2 binding limbs + 5 statement values

command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }
[[ -f "$GENESIS" ]] || { echo "genesis not found: $GENESIS" >&2; exit 1; }
[[ -s "$VK_FILE" ]] || { echo "vk file missing or empty: $VK_FILE" >&2; exit 1; }
[[ -n "$CLAIM" ]] || { echo "claim_id must not be empty" >&2; exit 1; }

# base64 on one line (works with GNU and macOS base64)
VK_B64="$(base64 < "$VK_FILE" | tr -d '\n')"

BACKUP="$GENESIS.bak.$(date +%Y%m%d-%H%M%S)"
cp "$GENESIS" "$BACKUP"
TMP="$(mktemp)"

jq --arg id "$ID" --arg claim "$CLAIM" --arg vk "$VK_B64" --arg gas "$GAS" \
   --argjson n "$NUM_PUBLIC" '
  .genesis_time as $now
  # Records-root pinning only makes sense for BN254 proof systems.
  | (if ($id | endswith("-bn254")) then [
        {index: 2, source: "INPUT_SOURCE_CHECKPOINT_RECORDS_ROOT"},
        {index: 6, source: "INPUT_SOURCE_CHECKPOINT_RECORD_COUNT"}
     ] else [] end) as $layout
  | .app_state.proofs.verifiers =
      ((.app_state.proofs.verifiers // [])
        # drop the same pair, and stale entries for this system with no claim
        | map(select(.proof_system_id != $id
                     or ((.claim_id // "") != $claim and (.claim_id // "") != ""))))
      + [{
          proof_system_id:   $id,
          claim_id:          $claim,
          description:       ("dev " + $id),
          verification_key:  $vk,
          status:            "VERIFIER_STATUS_ACTIVE",
          version:           1,
          registered_at:     $now,
          verification_gas:  $gas,
          num_public_inputs: $n,
          input_layout:      $layout
        }]
' "$GENESIS" > "$TMP"

mv "$TMP" "$GENESIS"
echo "verifier '$ID'/'$CLAIM' written to $GENESIS (backup: $BACKUP)"

# Optional: fail now if the file is not valid (older builds: validate-genesis).
lucidchaind genesis validate "$GENESIS" 2>/dev/null || lucidchaind genesis validate