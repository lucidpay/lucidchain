#!/usr/bin/env bash
# Writes a valid demo trip spec (no binding_hex) to stdout.
#   ./make-demo-spec.sh [n_samples=256] [t_start=1000000] > trip.txt
# Same trip as gen_fixtures: a square zone [1000,3000]^2, samples 3 s apart.
# n must be a power of two in [256, 16384].
set -euo pipefail
n="${1:-256}"
t0="${2:-1000000}"
if (( n < 256 || n > 16384 || (n & (n - 1)) != 0 )); then
  echo "n must be a power of two in [256, 16384], got $n" >&2; exit 1
fi
echo "zone_id 42"
echo "t_start $t0"
echo "t_end $((t0 + 2 + 3 * (n - 1) + 5))"
echo "polygon 1000 1000"
echo "polygon 3000 1000"
echo "polygon 3000 3000"
echo "polygon 1000 3000"
awk -v n="$n" -v t0="$t0" 'BEGIN { for (i = 0; i < n; i++) printf "sample %d %d %d\n", 1500 + (i * 7) % 1000, 1500 + (i * 13) % 1000, t0 + 2 + 3 * i }'
