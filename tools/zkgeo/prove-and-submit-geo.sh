#!/usr/bin/env bash
set -euo pipefail

BIN="${BIN:-lucidchaind}"
SC="${SC:-hospitality-platform-01}"
SEQ="${SEQ:-1}"
CLAIM="${CLAIM:-geofence.v2}"   # geofence.v1 already has a proof on the groth16 verifier
SYSTEM="plonk-bn254"
KEYS="${KEYS:-../../keys-geo}"
FROM="${FROM:-validator}"
CHAIN_ID="${CHAIN_ID:-my-testnet-1}"
CIRCUIT="-capacity 8 -records-depth 5 -area-depth 4"   # must match setup

b64hex() { printf '0x%s' "$(printf '%s' "$1" | base64 -d | od -An -tx1 | tr -d ' \n')"; }

B="$($BIN q proofs binding --sidechain-id "$SC" --checkpoint-sequence "$SEQ" --claim-id "$CLAIM" -o json)"
HI="$(b64hex "$(jq -r .hi <<<"$B")")"
LO="$(b64hex "$(jq -r .lo <<<"$B")")"
echo "binding hi=$HI lo=$LO"

go run ./cmd/zkgeo prove -scheme plonk $CIRCUIT \
  -pk "$KEYS/pk.bin" -vk "$KEYS/vk.bin" \
  -hi "$HI" -lo "$LO" -batch batch.json -out "$KEYS"

OUT="$($BIN tx proofs submit-proof \
  --sidechain-id "$SC" --checkpoint-sequence "$SEQ" \
  --proof-system-id "$SYSTEM" --claim-id "$CLAIM" \
  --public-inputs "$(cat "$KEYS/public.b64")" \
  --proof "$(cat "$KEYS/proof.b64")" \
  --from "$FROM" --chain-id "$CHAIN_ID" \
  --gas auto --gas-adjustment 1.5 -y)"
echo "$OUT"

TX="$(grep -oE 'txhash"?: *"?[0-9A-Fa-f]{64}' <<<"$OUT" | grep -oE '[0-9A-Fa-f]{64}' | tail -1)"
echo "waiting for block..."; sleep 7
$BIN q tx "$TX"
