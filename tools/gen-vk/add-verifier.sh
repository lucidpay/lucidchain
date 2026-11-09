#!/usr/bin/env bash
# add-verifier.sh: add (or replace) a verifier in genesis.json
# usage: ./add-verifier.sh [genesis.json] [vk.bin] [proof_system_id] [gas] [claim_id] [num_public_inputs]
#
# For plonky3-* proof systems the registration PINS the zone: public value 16 (zone_id) and
# values 19..34 (the 8 polygon vertex slots) become INPUT_SOURCE_FIXED_VALUE bindings, so a
# prover cannot choose its own zone. Set the zone with environment variables:
#   PQ_ZONE_ID   u32, default 42
#   PQ_POLYGON   3..8 vertices as "x y x y ...", counter-clockwise, each coordinate < 16384.
#                Padded to 8 slots by repeating the last vertex (same rule as av-zone-prove).
#                Default: the demo square "1000 1000 3000 1000 3000 3000 1000 3000".
#   num_public_inputs defaults to 43 for plonky3-* (7 otherwise).
set -euo pipefail

GENESIS="${1:-$HOME/.lucidchain/config/genesis.json}"
VK_FILE="${2:-vk.bin}"
ID="${3:-groth16-bn254}"
GAS="${4:-200000}"
CLAIM="${5:-geofence.v1}"
NUM_PUBLIC="${6:-}"
if [[ -z "$NUM_PUBLIC" ]]; then
  case "$ID" in
    plonky3*) NUM_PUBLIC=43 ;;   # 16 binding limbs + zone, window, 16 polygon, 8 trip digest
    *)        NUM_PUBLIC=7 ;;    # gnark geofence circuit: 2 binding limbs + 5 statement values
  esac
fi
PQ_ZONE_ID="${PQ_ZONE_ID:-42}"
PQ_POLYGON="${PQ_POLYGON:-1000 1000 3000 1000 3000 3000 1000 3000}"

command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }
[[ -f "$GENESIS" ]] || { echo "genesis not found: $GENESIS" >&2; exit 1; }
[[ -s "$VK_FILE" ]] || { echo "vk file missing or empty: $VK_FILE" >&2; exit 1; }
[[ -n "$CLAIM" ]] || { echo "claim_id must not be empty" >&2; exit 1; }

# ---- pinned zone for plonky3-* systems -------------------------------------------------
# u32 -> 4 canonical big-endian bytes -> base64 (qp.go compares these with SetBytes).
be4_b64() {
  local v=$1
  printf "$(printf '\\x%02x\\x%02x\\x%02x\\x%02x' $((v >> 24 & 255)) $((v >> 16 & 255)) $((v >> 8 & 255)) $((v & 255)))" | base64
}
PQ_LAYOUT='[]'
pq_pin() {
  PQ_LAYOUT="$(jq -c --argjson i "$1" --arg v "$(be4_b64 "$2")" \
    '. + [{index: $i, source: "INPUT_SOURCE_FIXED_VALUE", value: $v}]' <<<"$PQ_LAYOUT")"
}
case "$ID" in
  plonky3*)
    [[ "$NUM_PUBLIC" == "43" ]] || { echo "plonky3-* statements have 43 public values, got $NUM_PUBLIC" >&2; exit 1; }
    [[ "$PQ_ZONE_ID" =~ ^[0-9]+$ ]] && (( PQ_ZONE_ID < 2013265921 )) || { echo "PQ_ZONE_ID must be a u32 below 2013265921" >&2; exit 1; }
    read -r -a POLY <<<"$PQ_POLYGON"
    (( ${#POLY[@]} % 2 == 0 && ${#POLY[@]} >= 6 && ${#POLY[@]} <= 16 )) || { echo "PQ_POLYGON needs 3..8 vertices (x y pairs), got ${#POLY[@]} numbers" >&2; exit 1; }
    for c in "${POLY[@]}"; do
      [[ "$c" =~ ^[0-9]+$ ]] && (( c < 16384 )) || { echo "polygon coordinate '$c' must be an integer below 16384" >&2; exit 1; }
    done
    while (( ${#POLY[@]} < 16 )); do POLY+=("${POLY[-2]}" "${POLY[-1]}"); done
    pq_pin 16 "$PQ_ZONE_ID"
    for k in "${!POLY[@]}"; do pq_pin $((19 + k)) "${POLY[$k]}"; done
    ;;
esac

# base64 on one line (works with GNU and macOS base64)
VK_B64="$(base64 < "$VK_FILE" | tr -d '\n')"

BACKUP="$GENESIS.bak.$(date +%Y%m%d-%H%M%S)"
cp "$GENESIS" "$BACKUP"
TMP="$(mktemp)"

jq --arg id "$ID" --arg claim "$CLAIM" --arg vk "$VK_B64" --arg gas "$GAS" \
--argjson n "$NUM_PUBLIC" --argjson pq "$PQ_LAYOUT" '
  .genesis_time as $now
  # Records-root pinning only makes sense for BN254 proof systems; plonky3-* pins the zone.
  | (if ($id | endswith("-bn254")) then [
        {index: 2, source: "INPUT_SOURCE_CHECKPOINT_RECORDS_ROOT"},
        {index: 6, source: "INPUT_SOURCE_CHECKPOINT_RECORD_COUNT"}
     ] elif ($id | startswith("plonky3")) then $pq else [] end) as $layout
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
jq -r --arg id "$ID" --arg c "$CLAIM" '.app_state.proofs.verifiers[] | select(.proof_system_id==$id and .claim_id==$c) | "  num_public_inputs=\(.num_public_inputs) pinned_inputs=\(.input_layout|length)"' "$GENESIS"

# Optional: fail now if the file is not valid (older builds: validate-genesis).
lucidchaind genesis validate "$GENESIS" 2>/dev/null || lucidchaind genesis validate