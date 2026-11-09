#!/usr/bin/env bash
# Writes a valid demo trip spec (no binding_hex) to stdout, including a fresh random
# salt_hex line (the prover requires one).
#   ./make-demo-spec.sh [n_samples=256] [t_start=1000000] > trip.txt
#   SALT_HEX=<56 hex> ./make-demo-spec.sh ...      reproducible fixtures only
# The salt is private witness. This demo salt is random but lands in trip.txt, so treat
# that file as secret. A REAL salt comes from the device's CSPRNG and is never committed,
# logged or attested.
# Same trip as gen_fixtures: a square zone [1000,3000]^2, samples 3 s apart.
# n must be a power of two in [256, 1024] (the pinned trace heights, 2^8 to 2^10).
set -euo pipefail
n="${1:-256}"
t0="${2:-1000000}"
if (( n < 256 || n > 1024 || (n & (n - 1)) != 0 )); then
  echo "n must be a power of two in [256, 1024], got $n" >&2; exit 1
fi

# 7 u32 words, each rejection-sampled below the BabyBear modulus (never reduced), written as
# 56 hex chars of little-endian words: the format av-zone-prove's `salt_hex` expects.
P=2013265921
gen_salt() {
  local out="" v i
  for i in 1 2 3 4 5 6 7; do
    while :; do
      v="$(od -An -N4 -tu4 /dev/urandom | tr -d ' \n')"
      (( v < P )) && break
    done
    out+="$(printf '%02x%02x%02x%02x' $((v & 255)) $(((v >> 8) & 255)) $(((v >> 16) & 255)) $(((v >> 24) & 255)))"
  done
  printf '%s' "$out"
}
SALT_HEX="${SALT_HEX:-$(gen_salt)}"
if ! [[ "$SALT_HEX" =~ ^[0-9a-f]{56}$ ]]; then
  echo "SALT_HEX must be 56 lowercase hex characters (7 little-endian u32 words)" >&2; exit 1
fi
echo "salt_hex $SALT_HEX"
echo "zone_id 42"
echo "t_start $t0"
echo "t_end $((t0 + 2 + 3 * (n - 1) + 5))"
echo "polygon 1000 1000"
echo "polygon 3000 1000"
echo "polygon 3000 3000"
echo "polygon 1000 3000"
awk -v n="$n" -v t0="$t0" 'BEGIN { for (i = 0; i < n; i++) printf "sample %d %d %d\n", 1500 + (i * 7) % 1000, 1500 + (i * 13) % 1000, t0 + 2 + 3 * i }'