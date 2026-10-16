#!/usr/bin/env bash
# submit-checkpoint-from-batch.sh
#
# Builds, signs and submits a checkpoint whose zk_anchor (records root, tree
# depth) and record_count come from a batch.json, so the batch can then be
# proven against it with prove-and-submit-geo.sh.
#
# Required environment:
#   K1, K2          signer private keys, hex (signer indexes 0 and 1); K3 optional (index 2)
# Optional (defaults shown):
#   BATCH=$PWD/batch.json   RECORDS_DEPTH=5 (must match the circuit)
#   SC=hospitality-platform-01   SEQ=<latest+1>   STATE_ROOT=<sha256 of the batch, DEV ONLY>
#   POINTER=ipfs://test-checkpoint-<SEQ>   SIGNER_SET_VERSION=1
#   FROM=validator   CHAIN_ID=my-testnet-1   GAS_PRICES=0.025stake
#   SIGN=$HOME/tools/signcheckpoint/signcheckpoint   BIN=lucidchaind
#   DRY_RUN=1       print the submit command and stop
set -euo pipefail

fail() { echo "error: $*" >&2; exit 1; }

BIN="${BIN:-lucidchaind}"
SC="${SC:-hospitality-platform-01}"
CHAIN_ID="${CHAIN_ID:-my-testnet-1}"
FROM="${FROM:-validator}"
BATCH="${BATCH:-$PWD/batch.json}"
RECORDS_DEPTH="${RECORDS_DEPTH:-5}"
SIGNER_SET_VERSION="${SIGNER_SET_VERSION:-1}"
GAS_PRICES="${GAS_PRICES:-0.025stake}"
SIGN="${SIGN:-$HOME/tools/signcheckpoint/signcheckpoint}"
TX_TIMEOUT="${TX_TIMEOUT:-60}"
DRY_RUN="${DRY_RUN:-0}"

: "${K1:?set K1 (signer 0 private key, hex)}"
: "${K2:?set K2 (signer 1 private key, hex)}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(realpath -m "${ROOT:-$SCRIPT_DIR/../..}")"
BATCH="$(realpath -m "$BATCH")"

for tool in jq go base64 xxd awk sha256sum "$BIN"; do
  command -v "$tool" >/dev/null || fail "missing required tool: $tool"
done
[[ -f "$BATCH" ]] || fail "batch file not found: $BATCH"
[[ -f "$ROOT/cmd/batchroot/main.go" ]] || fail "cmd/batchroot not found under ROOT=$ROOT"
if [[ ! -x "$SIGN" ]]; then
  echo "building signcheckpoint -> $SIGN"
  mkdir -p "$(dirname "$SIGN")"
  (cd "$ROOT" && go build -o "$SIGN" ./tools/signcheckpoint)
fi

# 1. Anchor values from the batch (same RecordLeaf/BuildTree as the prover).
BR="$(cd "$ROOT" && go run ./cmd/batchroot -batch "$BATCH" -records-depth "$RECORDS_DEPTH")"
COUNT="$(awk '/^record_count:/{print $2}' <<<"$BR")"
ROOT_HEX="$(awk '/records_root \(hex\)/{print $NF}' <<<"$BR")"
ROOT_B64="$(awk '/records_root \(b64\)/{print $NF}' <<<"$BR")"
[[ "$COUNT" =~ ^[0-9]+$ ]] || fail "could not read record_count from batchroot output"
[[ "$ROOT_HEX" =~ ^[0-9a-fA-F]{64}$ ]] || fail "could not read a 32-byte records root from batchroot output"
[[ -n "$ROOT_B64" ]] || fail "could not read the base64 records root"
echo "batch: count=$COUNT depth=$RECORDS_DEPTH root=$ROOT_HEX"

# 2. Previous checkpoint: sequence and hash (field names are assumptions; see error text).
PREV_B64=""
LATEST=0
if J="$($BIN q checkpoint latest-checkpoint "$SC" -o json 2>/dev/null)" && jq -e '.checkpoint' >/dev/null 2>&1 <<<"$J"; then
  LATEST="$(jq -r '.checkpoint.lc_sequence // .checkpoint.sequence // empty' <<<"$J")"
  PREV_B64="$(jq -r '.checkpoint.checkpoint_hash // empty' <<<"$J")"
  [[ "$LATEST" =~ ^[0-9]+$ && -n "$PREV_B64" ]] ||
    fail "cannot read sequence/hash from 'q checkpoint latest-checkpoint'; inspect its JSON and adjust the jq paths"
fi
SEQ="${SEQ:-$((LATEST + 1))}"
if [[ "$SEQ" == "1" ]]; then PREV_B64=""; fi
if [[ "$SEQ" != "1" && -z "$PREV_B64" ]]; then fail "sequence $SEQ needs a previous checkpoint hash"; fi
PREV_HEX=""
if [[ -n "$PREV_B64" ]]; then PREV_HEX="$(printf '%s' "$PREV_B64" | base64 -d | xxd -p -c 256)"; fi

# 3. State root: required in real use; the default is a dev placeholder.
if [[ -z "${STATE_ROOT:-}" ]]; then
  STATE_ROOT="$(sha256sum "$BATCH" | cut -d' ' -f1)"
  echo "warning: STATE_ROOT not set; using sha256 of the batch (dev only)" >&2
fi
[[ "$STATE_ROOT" =~ ^[0-9a-fA-F]{64}$ ]] || fail "STATE_ROOT must be 32 bytes of hex (64 digits, no 0x)"
STATE_ROOT_B64="$(printf '%s' "$STATE_ROOT" | xxd -r -p | base64 | tr -d '\n')"
POINTER="${POINTER:-ipfs://test-checkpoint-$SEQ}"

echo "checkpoint: sc=$SC seq=$SEQ previous=${PREV_HEX:-<none>} pointer=$POINTER"

# 4. Sign. The tool prints --signatures / --zk-anchor lines on stdout, diagnostics on stderr.
KEY_ARGS=(--key "0=$K1" --key "1=$K2")
if [[ -n "${K3:-}" ]]; then KEY_ARGS+=(--key "2=$K3"); fi
NKEYS=$(( ${#KEY_ARGS[@]} / 2 ))

ERRFILE="$(mktemp)"; trap 'rm -f "$ERRFILE"' EXIT
if ! SIGOUT="$("$SIGN" \
    --chain-id "$CHAIN_ID" --sidechain-id "$SC" --lc-sequence "$SEQ" \
    --state-root "$STATE_ROOT" --previous-hash "$PREV_HEX" \
    --record-count "$COUNT" --data-pointer "$POINTER" \
    --signer-set-version "$SIGNER_SET_VERSION" \
    --zk-records-root "$ROOT_HEX" --zk-tree-depth "$RECORDS_DEPTH" \
    "${KEY_ARGS[@]}" 2>"$ERRFILE")"; then
  cat "$ERRFILE" >&2; fail "signcheckpoint failed"
fi

SIGS=()
while IFS= read -r line; do
  [[ "$line" == --signatures* ]] || continue
  SIGS+=(--signatures "$(grep -o '{.*}' <<<"$line")")
done <<<"$SIGOUT"
(( ${#SIGS[@]} == 2 * NKEYS )) || { cat "$ERRFILE" >&2; fail "expected $NKEYS signatures, parsed $(( ${#SIGS[@]} / 2 ))"; }
ANCHOR="$(grep '^--zk-anchor' <<<"$SIGOUT" | grep -o '{.*}' || true)"
[[ -n "$ANCHOR" ]] || fail "signcheckpoint did not print a --zk-anchor"

# 5. Submit.
CMD=("$BIN" tx checkpoint submit-checkpoint
  "$SC" "$SEQ" "$STATE_ROOT_B64" "$PREV_B64" "$COUNT" "$POINTER" "$SIGNER_SET_VERSION"
  "${SIGS[@]}" --zk-anchor "$ANCHOR"
  --from "$FROM" --chain-id "$CHAIN_ID"
  --gas auto --gas-adjustment 1.5 --gas-prices "$GAS_PRICES" -y -o json)

if [[ "$DRY_RUN" == "1" ]]; then
  printf '%q ' "${CMD[@]}"; echo
  exit 0
fi

RESP="$("${CMD[@]}")"
echo "$RESP"
CODE="$(jq -r '.code // 0' <<<"$RESP")"
[[ "$CODE" == "0" ]] || fail "rejected at broadcast: code=$CODE: $(jq -r '.raw_log // ""' <<<"$RESP")"
TX="$(jq -r '.txhash // empty' <<<"$RESP")"
[[ "$TX" =~ ^[0-9A-Fa-f]{64}$ ]] || fail "no txhash in response"

echo "waiting for tx $TX (up to ${TX_TIMEOUT}s)..."
deadline=$((SECONDS + TX_TIMEOUT))
RESULT=""
while (( SECONDS < deadline )); do
  if RESULT="$($BIN q tx "$TX" -o json 2>/dev/null)"; then break; fi
  RESULT=""; sleep 2
done
[[ -n "$RESULT" ]] || fail "tx $TX not found after ${TX_TIMEOUT}s"
CODE="$(jq -r '.code // 0' <<<"$RESULT")"
[[ "$CODE" == "0" ]] || fail "tx failed: code=$CODE: $(jq -r '.raw_log // ""' <<<"$RESULT")"

# 6. Confirm the stored anchor equals what the batch produces.
CP="$($BIN q checkpoint latest-checkpoint "$SC" -o json)"
GOT_ROOT="$(jq -r '.checkpoint.zk_anchor.records_root // empty' <<<"$CP")"
GOT_DEPTH="$(jq -r '.checkpoint.zk_anchor.records_tree_depth // empty' <<<"$CP")"
GOT_COUNT="$(jq -r '.checkpoint.record_count // empty' <<<"$CP")"
if [[ "$GOT_ROOT" == "$ROOT_B64" && "$GOT_DEPTH" == "$RECORDS_DEPTH" && "$GOT_COUNT" == "$COUNT" ]]; then
  echo "checkpoint $SEQ stored: root, depth and count match the batch"
else
  fail "stored anchor differs: root=$GOT_ROOT depth=$GOT_DEPTH count=$GOT_COUNT"
fi
echo "next: SEQ=$SEQ SCHEME=groth16 BATCH=$BATCH tools/zkgeo/prove-and-submit-geo.sh"