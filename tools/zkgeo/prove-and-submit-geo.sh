#!/usr/bin/env bash
# prove-and-submit-geo.sh: build a geofence proof for a checkpoint and submit it.
#
# Usage (all optional, shown with defaults):
#   SCHEME=groth16 KEYS=$HOME/tmp_workarea/keys-geo-groth16-bn254 \
#   BATCH=./batch.json OUT=./out SEQ=1 CLAIM=geofence.v1 \
#   tools/zkgeo/prove-and-submit-geo.sh
set -euo pipefail

BIN="${BIN:-lucidchaind}"
SC="${SC:-hospitality-platform-01}"
SEQ="${SEQ:-1}"
CLAIM="${CLAIM:-geofence.v1}"
SCHEME="${SCHEME:-plonk}"                      # plonk | groth16
FROM="${FROM:-validator}"
CHAIN_ID="${CHAIN_ID:-my-testnet-1}"
CIRCUIT="${CIRCUIT:--capacity 8 -records-depth 5 -area-depth 4}"   # must match setup
TX_TIMEOUT="${TX_TIMEOUT:-60}"                 # seconds to wait for the tx

case "$SCHEME" in
  plonk|groth16) ;;
  *) echo "SCHEME must be plonk or groth16, got '$SCHEME'" >&2; exit 1 ;;
esac
SYSTEM="${SYSTEM:-${SCHEME}-bn254}"
if [[ "$SYSTEM" != "${SCHEME}-bn254" ]]; then
  echo "SYSTEM '$SYSTEM' does not match SCHEME '$SCHEME' (expected ${SCHEME}-bn254)" >&2
  exit 1
fi

# Resolve user-supplied paths against the caller's directory BEFORE changing directory.
KEYS="$(realpath -m "${KEYS:-$HOME/tmp_workarea/keys-geo-${SYSTEM}}")"
BATCH="$(realpath -m "${BATCH:-$HOME/tmp_workarea/json/batch.json}")"
OUT="$(realpath -m "${OUT:-$KEYS}")"

# `go run ./cmd/zkgeo` must run from the repo root (this script lives in tools/zkgeo/).
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(realpath -m "${ROOT:-$SCRIPT_DIR/../..}")"

for tool in jq go base64 od "$BIN"; do
  command -v "$tool" >/dev/null || { echo "missing required tool: $tool" >&2; exit 1; }
done
[[ -f "$ROOT/cmd/zkgeo/main.go" ]] || { echo "cmd/zkgeo not found under ROOT=$ROOT" >&2; exit 1; }
[[ -s "$KEYS/pk.bin" ]] || { echo "missing proving key: $KEYS/pk.bin" >&2; exit 1; }
[[ -s "$KEYS/vk.bin" ]] || { echo "missing verifying key: $KEYS/vk.bin" >&2; exit 1; }
[[ -f "$BATCH" ]]       || { echo "batch file not found: $BATCH" >&2; exit 1; }
mkdir -p "$OUT"

echo "scheme=$SCHEME system=$SYSTEM claim=$CLAIM sidechain=$SC seq=$SEQ"
echo "keys=$KEYS batch=$BATCH out=$OUT"

b64hex() { printf '0x%s' "$(printf '%s' "$1" | base64 -d | od -An -tx1 | tr -d ' \n')"; }

B="$($BIN q proofs binding --sidechain-id "$SC" --checkpoint-sequence "$SEQ" --claim-id "$CLAIM" -o json)"
HI="$(b64hex "$(jq -r .hi <<<"$B")")"
LO="$(b64hex "$(jq -r .lo <<<"$B")")"
for v in "$HI" "$LO"; do
  [[ ${#v} -eq 34 ]] || { echo "unexpected binding limb '$v' (want 0x + 32 hex digits)" >&2; exit 1; }
done
echo "binding hi=$HI lo=$LO"

cd "$ROOT"
# shellcheck disable=SC2086  # CIRCUIT is intentionally word-split into flags
go run ./cmd/zkgeo prove -scheme "$SCHEME" $CIRCUIT \
  -pk "$KEYS/pk.bin" -vk "$KEYS/vk.bin" \
  -hi "$HI" -lo "$LO" -batch "$BATCH" -out "$OUT"

[[ -s "$OUT/proof.b64" && -s "$OUT/public.b64" ]] || {
  echo "prover did not write proof.b64 / public.b64 in $OUT" >&2; exit 1; }

RESP="$($BIN tx proofs submit-proof \
  --sidechain-id "$SC" --checkpoint-sequence "$SEQ" \
  --proof-system-id "$SYSTEM" --claim-id "$CLAIM" \
  --public-inputs "$(cat "$OUT/public.b64")" \
  --proof "$(cat "$OUT/proof.b64")" \
  --from "$FROM" --chain-id "$CHAIN_ID" \
  --gas auto --gas-adjustment 1.5 -y -o json)"
echo "$RESP"

# Rejected at CheckTx (e.g. verifier not found, binding mismatch): the code is non-zero here.
CHECK_CODE="$(jq -r '.code // 0' <<<"$RESP")"
if [[ "$CHECK_CODE" != "0" ]]; then
  echo "tx rejected at broadcast: code=$CHECK_CODE: $(jq -r '.raw_log // ""' <<<"$RESP")" >&2
  exit 1
fi

TX="$(jq -r '.txhash // empty' <<<"$RESP")"
[[ "$TX" =~ ^[0-9A-Fa-f]{64}$ ]] || { echo "no txhash in response" >&2; exit 1; }

echo "waiting for tx $TX (up to ${TX_TIMEOUT}s)..."
deadline=$((SECONDS + TX_TIMEOUT))
RESULT=""
while (( SECONDS < deadline )); do
  if RESULT="$($BIN q tx "$TX" -o json 2>/dev/null)"; then break; fi
  RESULT=""
  sleep 2
done
[[ -n "$RESULT" ]] || { echo "tx $TX not found after ${TX_TIMEOUT}s" >&2; exit 1; }

CODE="$(jq -r '.code // 0' <<<"$RESULT")"
if [[ "$CODE" != "0" ]]; then
  echo "tx failed: code=$CODE: $(jq -r '.raw_log // ""' <<<"$RESULT")" >&2
  exit 1
fi
echo "proof submitted: tx=$TX"
jq '{height, txhash, code, gas_used, gas_wanted}' <<<"$RESULT"