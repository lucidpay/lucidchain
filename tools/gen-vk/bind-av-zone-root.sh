#!/usr/bin/env bash
# Binds the av-zone trip digest (public inputs 35..42) to the checkpoint's
# POSEIDON2_BABYBEAR zk_anchor.records_root, limb by limb, by adding eight
# INPUT_SOURCE_CHECKPOINT_RECORDS_ROOT_BABYBEAR_LIMB entries to the registered
# input_layout. Idempotent: existing limb entries are replaced, and it refuses to
# run if another entry already pins an index in the digest range.
#
#   bind-av-zone-root.sh genesis  <genesis.json>   # patch a genesis file in place (before first start)
#   bind-av-zone-root.sh proposal <out.json>       # write a gov proposal for a running chain
#                                                  # (needs lucidchaind + GOV_ADDRESS; bumps the verifier version)
#
# Env: SYS (plonky3-babybear-v1)  CLAIM (av-zone)  FIRST_INDEX (35 = PQPVDigest in pq_layout.go)
#      VERIFIERS_PATH (app_state.proofs.verifiers)  DEPOSIT (10000000stake)
set -euo pipefail

SYS="${SYS:-plonky3-babybear-v1}"
CLAIM="${CLAIM:-av-zone}"
FIRST_INDEX="${FIRST_INDEX:-35}"
VERIFIERS_PATH="${VERIFIERS_PATH:-app_state.proofs.verifiers}"
DEPOSIT="${DEPOSIT:-10000000stake}"
SRC="INPUT_SOURCE_CHECKPOINT_RECORDS_ROOT_BABYBEAR_LIMB"

read -r -d '' DEFS <<'JQ' || true
def limbs: [range(0;8) as $i | {index: ($first + $i), source: $src, limb: $i}];
def merged($layout):
  ($layout // []) | map(select(.source != $src)) as $keep
  | if any($keep[]; .index >= $first and .index < ($first + 8))
    then error("input_layout already pins an index in the digest range \($first)..\($first + 7)")
    else ($keep + limbs | sort_by(.index)) end;
JQ

mode="${1:-}"; out="${2:-}"
[ -n "$mode" ] && [ -n "$out" ] || { sed -n '2,13p' "$0"; exit 2; }
COMMON=(--arg sys "$SYS" --arg claim "$CLAIM" --arg src "$SRC" --argjson first "$FIRST_INDEX")

case "$mode" in
genesis)
  [ -f "$out" ] || { echo "no such file: $out" >&2; exit 1; }
  tmp="$(mktemp)"
  jq "${COMMON[@]}" --arg vp "$VERIFIERS_PATH" "$DEFS"'
    ($vp | split(".")) as $p
    | (getpath($p) // error("no verifiers array at \($vp): set VERIFIERS_PATH")) as $vs
    | ([$vs[] | select(.proof_system_id == $sys and .claim_id == $claim)] | length) as $n
    | if $n != 1 then error("expected exactly one verifier \($sys)/\($claim), found \($n)") else . end
    | setpath($p; $vs | map(if .proof_system_id == $sys and .claim_id == $claim
                            then .input_layout = merged(.input_layout) else . end))
  ' "$out" > "$tmp"
  mv "$tmp" "$out"
  echo "bound $SYS/$CLAIM inputs $FIRST_INDEX..$((FIRST_INDEX + 7)) to the checkpoint records_root limbs in $out"
  ;;
proposal)
  : "${GOV_ADDRESS:?set GOV_ADDRESS (lucidchaind query auth module-account gov)}"
  cur="$(lucidchaind query proofs verifiers -o json | jq -c --arg sys "$SYS" --arg claim "$CLAIM" '
    [.verifiers[] | select(.proof_system_id == $sys and .claim_id == $claim)]
    | if length != 1 then error("expected exactly one verifier \($sys)/\($claim)") else .[0] end')"
  jq -n "${COMMON[@]}" --argjson v "$cur" --arg gov "$GOV_ADDRESS" --arg dep "$DEPOSIT" "$DEFS"'
    if $v.verification_key == null then error("query output has no verification_key") else . end
    | { messages: [{
          "@type": "/lucidchain.proofs.v1.MsgRegisterVerifier",
          authority: $gov,
          proof_system_id: $v.proof_system_id,
          description: ($v.description // ""),
          verification_key: $v.verification_key,
          verification_gas: $v.verification_gas,
          claim_id: $v.claim_id,
          input_layout: merged($v.input_layout) }],
        metadata: "", deposit: $dep,
        title: "Bind \($sys)/\($claim) trip digest to the checkpoint anchor",
        summary: "Pin public inputs \($first)..\($first + 7) to the POSEIDON2_BABYBEAR zk_anchor.records_root limbs." }
  ' > "$out"
  echo "wrote $out; submit it with: lucidchaind tx gov submit-proposal $out --from validator --chain-id <id> ..."
  ;;
*) sed -n '2,13p' "$0"; exit 2 ;;
esac
