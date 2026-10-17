#!/usr/bin/env bash
# Signs and submits an attestation for the latest checkpoint of a sidechain.
#
# WARNING: this exports the signer's PRIVATE KEY in plaintext
# (keys export --unarmored-hex --unsafe). Local testnets only.
#
# Needs: lucidchaind, openssl, python3 (standard library only), realpath.
#
# Files (defaults live in ~/tmp_workarea/json, like the other zkgeo scripts):
#   CLAIM_FILE=$JSON_DIR/claim.json   claim that is signed and submitted
#   SIGS_FILE=$JSON_DIR/sigs.json     signatures output
set -euo pipefail

BIN="${BIN:-lucidchaind}"
CHAIN_ID="${CHAIN_ID:-my-testnet-1}"
OPERATOR_KEY="${OPERATOR_KEY:-attestor-operator}"   # pays fees; must be the attestor's operator
SIGNER_KEY="${SIGNER_KEY:-attestorkey2}"            # signs the attestation; must be in signer_keys
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
JSON_DIR="$(realpath -m "${JSON_DIR:-$HOME/tmp_workarea/json}")"
CLAIM_FILE="$(realpath -m "${CLAIM_FILE:-$JSON_DIR/claim.json}")"
SIGS_FILE="$(realpath -m "${SIGS_FILE:-$JSON_DIR/sigs.json}")"
mkdir -p "$JSON_DIR" "$(dirname "$CLAIM_FILE")" "$(dirname "$SIGS_FILE")"

for c in "$BIN" openssl python3 realpath; do
  command -v "$c" >/dev/null || { echo "missing: $c"; exit 1; }
done

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
  CP_JSON="$("$BIN" query checkpoint latest-checkpoint "$SIDECHAIN_ID" -o json)"
  read -r CP_SEQ CP_HASH < <(printf '%s' "$CP_JSON" | python3 -c '
import sys, json, base64
c = json.load(sys.stdin)["checkpoint"]
print(c["lc_sequence"], base64.b64decode(c["checkpoint_hash"]).hex())')
  SEQ="${SEQ:-$CP_SEQ}"
  CHECKPOINT_HASH_HEX="${CHECKPOINT_HASH_HEX:-$CP_HASH}"
fi
echo "Checkpoint: sidechain=$SIDECHAIN_ID seq=$SEQ hash=$CHECKPOINT_HASH_HEX"

# --- sign bytes ---------------------------------------------------------------
SIGN_HEX="$("$BIN" tx attestor sign-bytes "$SCHEMA_ID" "$ATTESTOR_ID" "$SIDECHAIN_ID" "$SEQ" \
  --claim-file "$CLAIM_FILE" --checkpoint-hash "$CHECKPOINT_HASH_HEX" \
  --chain-id "$CHAIN_ID" | tr -d '[:space:]')"
[ -n "$SIGN_HEX" ] || { echo "sign-bytes returned nothing"; exit 1; }
python3 -c 'import sys;sys.stdout.buffer.write(bytes.fromhex(sys.argv[1]))' "$SIGN_HEX" > "$WORK/msg.bin"

# --- export the private key (hex) and wrap it as a secp256k1 PEM ---------------
# The export asks "Continue? [y/N]" on stderr; the answer is piped in.
K1="$(printf 'y\n' | "$BIN" keys export "$SIGNER_KEY" --unarmored-hex --unsafe 2>/dev/null | tr -d '[:space:]')"
[[ "$K1" =~ ^[0-9a-fA-F]{64}$ ]] || { echo "could not export a 32-byte hex key for '$SIGNER_KEY'"; exit 1; }

python3 - "$K1" > "$WORK/key.pem" <<'EOF'
import sys, base64
k = bytes.fromhex(sys.argv[1])
der = bytes.fromhex("302e0201010420") + k + bytes.fromhex("a00706052b8104000a")
print("-----BEGIN EC PRIVATE KEY-----")
print(base64.b64encode(der).decode())
print("-----END EC PRIVATE KEY-----")
EOF
unset K1

# --- sanity check: does this key match the registered signer key? --------------
PUB_B64="$(openssl ec -in "$WORK/key.pem" -pubout -conv_form compressed -outform DER 2>/dev/null | tail -c 33 | base64)"
if "$BIN" query attestor attestor "$ATTESTOR_ID" | grep -qF "$PUB_B64"; then
  echo "Signer key matches the attestor's registered key."
else
  echo "WARNING: $SIGNER_KEY's pubkey ($PUB_B64) is not in $ATTESTOR_ID's signer_keys; the chain will reject the signature."
fi

# --- sign: SHA-256(msg), ECDSA, DER -> raw 64-byte r||s with low s -------------
SIG_HEX="$(openssl dgst -sha256 -sign "$WORK/key.pem" "$WORK/msg.bin" | python3 -c '
import sys
n = 0xFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEBAAEDCE6AF48A03BBFD25E8CD0364141
d = sys.stdin.buffer.read()
assert d[0] == 0x30
i = 2 if d[1] < 0x80 else 2 + (d[1] & 0x7F)
assert d[i] == 2; l = d[i+1]; r = int.from_bytes(d[i+2:i+2+l], "big"); i += 2 + l
assert d[i] == 2; l = d[i+1]; s = int.from_bytes(d[i+2:i+2+l], "big")
if s > n // 2:
    s = n - s
print((r.to_bytes(32, "big") + s.to_bytes(32, "big")).hex())')"

printf '[{"signer_index": %s, "signature": "%s"}]\n' "$SIGNER_INDEX" "$SIG_HEX" > "$SIGS_FILE"
echo "Wrote $SIGS_FILE"

if [ "$NO_SUBMIT" = "1" ]; then
  exit 0
fi

# --- submit -----------------------------------------------------------------------
"$BIN" tx attestor submit-attestation "$SCHEMA_ID" "$ATTESTOR_ID" "$SIDECHAIN_ID" "$SEQ" \
  --claim-file "$CLAIM_FILE" --signatures-file "$SIGS_FILE" \
  --from "$OPERATOR_KEY" --chain-id "$CHAIN_ID" \
  --gas auto --gas-adjustment 1.5 --gas-prices "$GAS_PRICES" --yes

echo
echo "Verify with: $BIN query attestor checkpoint-attestations $SIDECHAIN_ID $SEQ"