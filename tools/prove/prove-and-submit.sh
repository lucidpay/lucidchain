#!/usr/bin/env bash
set -euo pipefail

BIN="${BIN:-lucidchaind}"
SC="${SC:-hospitality-platform-01}"
SEQ="${SEQ:-1}"
CLAIM="${CLAIM:-geofence.v1}"
SYSTEM="${SYSTEM:-groth16-bn254}"
KEYS="${KEYS:-../gen-vk}"
FROM="${FROM:-validator}"
CHAIN_ID="${CHAIN_ID:-my-testnet-1}"

B="$($BIN q proofs binding --sidechain-id "$SC" --checkpoint-sequence "$SEQ" --claim-id "$CLAIM" -o json)"
HI="$(jq -r .hi <<<"$B")"
LO="$(jq -r .lo <<<"$B")"
echo "binding hi=$HI lo=$LO"

go run main.go -pk "$KEYS/pk.bin" -vk "$KEYS/vk.bin" -hi "$HI" -lo "$LO" -out "$KEYS"

$BIN tx proofs submit-proof \
  --sidechain-id "$SC" \
  --checkpoint-sequence "$SEQ" \
  --proof-system-id "$SYSTEM" \
  --claim-id "$CLAIM" \
  --public-inputs "$(cat "$KEYS/public.b64")" \
  --proof "$(cat "$KEYS/proof.b64")" \
  --from "$FROM" --chain-id "$CHAIN_ID" \
  --gas auto --gas-adjustment 1.5 -y