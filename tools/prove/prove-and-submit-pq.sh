#!/usr/bin/env bash
# Rust / Plonky3 (av-zone) counterpart of prove-and-submit.sh.
#
#   SPEC=trip.txt ./prove-and-submit-pq.sh
#
# SPEC is the trip part of the av-zone-prove spec: zone_id, t_start, t_end,
# polygon lines and sample lines. Do NOT put binding_hex in it; this script
# derives it from the chain's `query proofs binding`.
#
# Why the transaction is built in a file: proofs are 268 KB to 515 KB, and one
# command-line argument is capped at 128 KiB on Linux, so
# `--proof "$(cat proof.b64)"` cannot work. We take the CLI's own unsigned
# tx JSON (so the message type and layout are never guessed), patch the two
# bytes fields with jq --rawfile, then sign and broadcast.
set -euo pipefail

BIN="${BIN:-lucidchaind}"
SC="${SC:-hospitality-platform-01}"
SEQ="${SEQ:-1}"
CLAIM="${CLAIM:-geofence.v1}"
SYSTEM="${SYSTEM:-plonky3-babybear-v1}"
FROM="${FROM:-validator}"
CHAIN_ID="${CHAIN_ID:-my-testnet-1}"
GAS="${GAS:-}"                   # leave empty to use the estimate below, or set an explicit limit
# Estimate inputs (check them against your chain; the fee is charged on the limit):
TX_SIZE_COST="${TX_SIZE_COST:-10}"   # auth param tx_size_cost_per_byte
STORE_COST="${STORE_COST:-30}"       # KV store write gas per byte
STORE_PROOF="${STORE_PROOF:-1}"      # 1 if the keeper stores the full proof, 0 if only a hash
VERIFY_GAS="${VERIFY_GAS:-1000000}"  # the proofs module's VerificationGas parameter
GAS_PRICES="${GAS_PRICES:-}"     # e.g. 0.025stake; the fee is fixed when the unsigned tx is built
OUT="${OUT:-pq-out}"
AVZONE_DIR="${AVZONE_DIR:-../av-zone}"
SPEC="${SPEC:?set SPEC to the trip spec file (no binding_hex line)}"
EXPECT_VK_ID="${EXPECT_VK_ID:-}" # optional: abort unless the prover reports this vk_id
read -r -a TX_FLAGS <<<"${TX_FLAGS:-}"   # extra flags, e.g. "--keyring-backend test --node tcp://..."

for tool in jq base64 od; do command -v "$tool" >/dev/null || { echo "missing tool: $tool" >&2; exit 1; }; done
[[ -f "$SPEC" ]] || { echo "SPEC file not found: $SPEC" >&2; exit 1; }
if grep -Eq '^[[:space:]]*binding_hex' "$SPEC"; then
  echo "SPEC must not contain binding_hex; it is derived from the chain" >&2; exit 1
fi
mkdir -p "$OUT"

# 1. The checkpoint binding, exactly as in the gnark flow.
B="$($BIN q proofs binding --sidechain-id "$SC" --checkpoint-sequence "$SEQ" --claim-id "$CLAIM" -o json)"
HI="$(jq -r .hi <<<"$B")"
LO="$(jq -r .lo <<<"$B")"
echo "binding hi=$HI lo=$LO"

# hi and lo are big-endian integers (Go: SetBytes), each up to 128 bits. The
# prover wants one 32-byte big-endian value = hi (16 bytes) || lo (16 bytes).
to_hex16() {
  local h
  h="$(printf '%s' "$1" | base64 -d | od -An -vtx1 | tr -d ' \n')"
  if (( ${#h} > 32 )); then echo "binding half is longer than 16 bytes" >&2; exit 1; fi
  printf '%32s' "$h" | tr ' ' 0
}
BINDING_HEX="$(to_hex16 "$HI")$(to_hex16 "$LO")"
echo "binding_hex=$BINDING_HEX"

{ echo "binding_hex $BINDING_HEX"; cat "$SPEC"; } >"$OUT/spec.txt"

# 2. Prove. The prover checks its own proof before writing it (see the patch
#    to av-zone-prove.rs), the analogue of gnark's local Verify.
if [[ -n "${PROVER:-}" ]]; then
  read -r -a PROVE_CMD <<<"$PROVER"
else
  PROVE_CMD=(cargo run --release --manifest-path "$AVZONE_DIR/Cargo.toml" --bin av-zone-prove --)
fi
PROVE_LINE="$("${PROVE_CMD[@]}" "$OUT/spec.txt" "$OUT" | tail -n1)"
echo "$PROVE_LINE"
if [[ -n "$EXPECT_VK_ID" && "$PROVE_LINE" != "vk_id=$EXPECT_VK_ID "* ]]; then
  echo "prover vk_id does not match EXPECT_VK_ID ($EXPECT_VK_ID)" >&2; exit 1
fi

PROOF_BYTES="$(wc -c <"$OUT/proof.bin" | tr -d ' ')"
if (( PROOF_BYTES > 524288 )); then echo "proof is $PROOF_BYTES bytes, over the 524288 cap" >&2; exit 1; fi

if [[ -z "$GAS" ]]; then
  PUB_BYTES="$(wc -c <"$OUT/pub.bin" | tr -d ' ')"
  TX_BYTES=$((PROOF_BYTES + PUB_BYTES + 700))          # proof + public inputs + tx envelope
  STORED=$(( STORE_PROOF == 1 ? PROOF_BYTES : 0 ))
  GAS=$(( TX_SIZE_COST * TX_BYTES + STORE_COST * STORED + VERIFY_GAS + 300000 ))
  echo "estimated gas limit: $GAS (tx ~${TX_BYTES} B x ${TX_SIZE_COST}, stored ${STORED} B x ${STORE_COST}, verify ${VERIFY_GAS}, +300000)"
fi

# base64 without line breaks and without a trailing newline
b64() { base64 <"$1" | tr -d '\n'; }
b64 "$OUT/pub.bin"   >"$OUT/public.b64"
b64 "$OUT/proof.bin" >"$OUT/proof.b64"

# 3. Unsigned tx from the CLI (placeholder proof), then patch the real bytes in.
$BIN tx proofs submit-proof \
  --sidechain-id "$SC" \
  --checkpoint-sequence "$SEQ" \
  --proof-system-id "$SYSTEM" \
  --claim-id "$CLAIM" \
  --public-inputs "$(cat "$OUT/public.b64")" \
  --proof "AA==" \
  --from "$FROM" --chain-id "$CHAIN_ID" \
  --gas "$GAS" ${GAS_PRICES:+--gas-prices "$GAS_PRICES"} \
  --generate-only "${TX_FLAGS[@]}" >"$OUT/unsigned.tmp.json"

jq -e '.body.messages[0] | has("proof") and has("public_inputs")' "$OUT/unsigned.tmp.json" >/dev/null || {
  echo "unexpected message shape; adjust the jq field names below to match $OUT/unsigned.tmp.json" >&2; exit 1; }

jq --rawfile pub "$OUT/public.b64" --rawfile proof "$OUT/proof.b64" \
  '.body.messages[0].public_inputs = $pub | .body.messages[0].proof = $proof' \
  "$OUT/unsigned.tmp.json" >"$OUT/unsigned.json"

# 4. Sign and broadcast.
$BIN tx sign "$OUT/unsigned.json" --from "$FROM" --chain-id "$CHAIN_ID" \
  --output-document "$OUT/signed.json" "${TX_FLAGS[@]}"
RESP="$($BIN tx broadcast "$OUT/signed.json" --broadcast-mode sync -o json "${TX_FLAGS[@]}")"
jq '{code, raw_log, txhash}' <<<"$RESP"
TXHASH="$(jq -r .txhash <<<"$RESP")"
printf '%s' "$TXHASH" >"$OUT/txhash"
echo "txhash=$TXHASH  (saved to $OUT/txhash)"
# sync mode only reports mempool admission (CheckTx): a non-zero code here is a rejection
[[ "$(jq -r .code <<<"$RESP")" == "0" ]] || exit 1

# Wait for the block and report the real result (this is where the verifier runs).
for _ in $(seq 1 30); do
  if R="$($BIN q tx "$TXHASH" -o json 2>/dev/null)"; then
    jq '{height, code, raw_log, gas_wanted, gas_used}' <<<"$R"
    [[ "$(jq -r .code <<<"$R")" == "0" ]] && exit 0 || exit 1
  fi
  sleep 1
done
echo "tx not found after 30 s; check later with: $BIN q tx $TXHASH" >&2
exit 2