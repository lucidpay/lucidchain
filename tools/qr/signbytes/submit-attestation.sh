#!/usr/bin/env bash
# Signs (ML-DSA-65) and submits an attestation for the latest checkpoint of a sidechain.
#
# WARNING: this exports the signer's PRIVATE KEY in plaintext
# (keys export --unarmored-hex --unsafe). Local testnets only.
#
# Needs: lucidchaind, python3 (standard library only), realpath, and the
# mldsasign helper. Build the helper once from the repository folder:
#   go build -o ~/tools/qr/mldsasign/mldsasign ./tools/qr/mldsasign
#
# Files (defaults live in ~/tmp_workarea_qr/json):
#   CLAIM_FILE=$JSON_DIR/claim.json   claim that is signed and submitted
#   SIGS_FILE=$JSON_DIR/sigs.json     signatures output
set -euo pipefail

BIN="${BIN:-lucidchaind}"
MLDSA_SIGN="${MLDSA_SIGN:-$HOME/tools/qr/mldsasign/mldsasign}"
CHAIN_ID="${CHAIN_ID:-my-testnet-1}"
NODE_HOME="${NODE_HOME:-${QR_HOME:-$HOME/.lucidchain-qr}}"   # QR node home (never the classical ~/.lucidchain)
OPERATOR_KEY="${OPERATOR_KEY:-qr_attestor-operator}"   # pays fees; must be the attestor's operator
SIGNER_KEY="${SIGNER_KEY:-qr_attestorkey2}"            # signs the attestation; must be in signer_keys (ML-DSA-65)
SCHEMA_ID="${SCHEMA_ID:-kyc-v1}"
ATTESTOR_ID="${ATTESTOR_ID:-acme}"
SIDECHAIN_ID="${SIDECHAIN_ID:-hospitality-platform-01}"
SIGNER_INDEX="${SIGNER_INDEX:-0}"          # position in the attestor's signer_keys
GAS_PRICES="${GAS_PRICES:-0stake}"
NO_SUBMIT="${NO_SUBMIT:-0}"                # 1 = only produce the signatures file
# Optional overrides; fetched from the latest checkpoint when unset.
SEQ="${SEQ:-}"
CHECKPOINT_HASH_HEX="${CHECKPOINT_HASH_HEX:-}"

# Resolve paths against the caller's directory (absolute paths, no surprises).
JSON_DIR="$(realpath -m "${JSON_DIR:-$HOME/tmp_workarea_qr/json}")"
CLAIM_FILE="$(realpath -m "${CLAIM_FILE:-$JSON_DIR/claim.json}")"
SIGS_FILE="$(realpath -m "${SIGS_FILE:-$JSON_DIR/sigs.json}")"
mkdir -p "$JSON_DIR" "$(dirname "$CLAIM_FILE")" "$(dirname "$SIGS_FILE")"

# Scripts do not inherit shell functions, so always pass the QR home explicitly.
lcd() { "$BIN" --home "$NODE_HOME" "$@"; }

for c in "$BIN" python3 realpath; do
  command -v "$c" >/dev/null || { echo "missing: $c"; exit 1; }
done
[ -x "$MLDSA_SIGN" ] || { echo "missing: $MLDSA_SIGN (build it: go build -o ~/tools/qr/mldsasign/mldsasign ./tools/qr/mldsasign)"; exit 1; }

umask 077
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"; unset K1' EXIT

echo "claim=$CLAIM_FILE sigs=$SIGS_FILE"

# --- claim file: exact bytes are signed and submitted ------------------------
if [ ! -f "$CLAIM_FILE" ]; then
  printf '%s' '{"revenue":"1250000","period":"2026-09"}' > "$CLAIM_FILE"
  echo "Created sample $CLAIM_FILE"
fi

# --- checkpoint ---------------------------------------------------------------
if [ -z "$SEQ" ] || [ -z "$CHECKPOINT_HASH_HEX" ]; then
  CP_JSON="$(lcd query checkpoint latest-checkpoint "$SIDECHAIN_ID" -o json)"
  read -r CP_SEQ CP_HASH < <(printf '%s' "$CP_JSON" | python3 -c '
import sys, json, base64
c = json.load(sys.stdin)["checkpoint"]
print(c["lc_sequence"], base64.b64decode(c["checkpoint_hash"]).hex())')
  SEQ="${SEQ:-$CP_SEQ}"
  CHECKPOINT_HASH_HEX="${CHECKPOINT_HASH_HEX:-$CP_HASH}"
fi
echo "Checkpoint: sidechain=$SIDECHAIN_ID seq=$SEQ hash=$CHECKPOINT_HASH_HEX"

# --- sign bytes ---------------------------------------------------------------
SIGN_HEX="$(lcd tx attestor sign-bytes "$SCHEMA_ID" "$ATTESTOR_ID" "$SIDECHAIN_ID" "$SEQ" \
  --claim-file "$CLAIM_FILE" --checkpoint-hash "$CHECKPOINT_HASH_HEX" \
  --chain-id "$CHAIN_ID" | tr -d '[:space:]')"
[ -n "$SIGN_HEX" ] || { echo "sign-bytes returned nothing"; exit 1; }
python3 -c 'import sys;sys.stdout.buffer.write(bytes.fromhex(sys.argv[1]))' "$SIGN_HEX" > "$WORK/msg.bin"

# --- export the private key (hex) ----------------------------------------------
# For an ML-DSA-65 key this is the 4032-byte packed private key (8064 hex chars).
# The export asks "Continue? [y/N]" on stderr; the answer is piped in.
K1="$(printf 'y\n' | lcd keys export "$SIGNER_KEY" --unarmored-hex --unsafe 2>/dev/null | tr -d '[:space:]')"
[[ "$K1" =~ ^[0-9a-fA-F]{8064}$ ]] || { echo "could not export an ML-DSA-65 private key (8064 hex chars) for '$SIGNER_KEY'. Was it created with --key-type ml_dsa_65?"; exit 1; }

# --- sign: ML-DSA-65 over the raw sign bytes (key goes in via stdin, not argv) ---
SIGN_OUT="$(printf '%s' "$K1" | "$MLDSA_SIGN" --msg-file "$WORK/msg.bin")"
unset K1

read -r PUB_B64 SIG_HEX < <(printf '%s' "$SIGN_OUT" | python3 -c '
import sys, json
o = json.load(sys.stdin)
print(o["pubkey_b64"], o["signature_hex"])')

# --- sanity check: does this key match the registered signer key? --------------
# JSON output avoids YAML line-wrapping of the ~2.6 KB base64 public key.
if lcd query attestor attestor "$ATTESTOR_ID" -o json | grep -qF "$PUB_B64"; then
  echo "Signer key matches the attestor's registered key."
else
  echo "WARNING: $SIGNER_KEY's ML-DSA pubkey is not in $ATTESTOR_ID's signer_keys; the chain will reject the signature."
fi

printf '[{"signer_index": %s, "signature": "%s"}]\n' "$SIGNER_INDEX" "$SIG_HEX" > "$SIGS_FILE"
echo "Wrote $SIGS_FILE"

if [ "$NO_SUBMIT" = "1" ]; then
  exit 0
fi

# --- submit -----------------------------------------------------------------------
lcd tx attestor submit-attestation "$SCHEMA_ID" "$ATTESTOR_ID" "$SIDECHAIN_ID" "$SEQ" \
  --claim-file "$CLAIM_FILE" --signatures-file "$SIGS_FILE" \
  --from "$OPERATOR_KEY" --chain-id "$CHAIN_ID" \
  --gas auto --gas-adjustment 1.5 --gas-prices "$GAS_PRICES" --yes

echo
echo "Verify with: $BIN query attestor checkpoint-attestations $SIDECHAIN_ID $SEQ"
