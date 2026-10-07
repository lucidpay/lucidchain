# av-zone: `geofence.av_zone` core statement (v0, experimental)

A STARK (Plonky3 0.8.0, hash-based, no pairings or elliptic curves) proving that
a chunk of an autonomous vehicle's trip stayed inside a zone, without revealing
the trajectory.

## What a valid proof says

There exist hidden samples `(x, y, t)` such that:

1. every sample is inside, or on the boundary of, the public convex polygon;
2. times strictly increase and consecutive samples are at most **16 s** apart;
3. the first sample is within 15 s of `t_start` and the last within 15 s of
   `t_end`, so the whole window is covered.

## What it does NOT say (read this before deploying)

**The samples are not tied to any real data.** Anyone can prove this statement
about an invented trip. Binding the hidden samples to an attested trip log
(a Poseidon2 commitment, checked through Plonky3's multi-table lookups, with an
ML-DSA signature over the commitment) is the next stage and is **not** in this
version. Do not register this `vk_id` for anything of value until then. The
version string `av-zone.core.v0` is deliberately experimental.

Also not covered: whether the vehicle left the zone *between* samples (a 16 s
gap allows a brief excursion). Register zones shrunk inward by `v_max * 16 s`.

## Public values (35 field elements, canonical u32)

| Index | Meaning |
|---|---|
| 0..16 | checkpoint binding, 16 limbs of 16 bits (same convention as the Go adapter) |
| 16 | `zone_id` |
| 17, 18 | `t_start`, `t_end` (each < 2^30) |
| 19..35 | 8 polygon vertex slots `(x, y)`, coordinates < 2^14, counter-clockwise, unused trailing slots repeat the last vertex |

`Statement::from_values` validates all of it, including that the polygon is
convex, counter-clockwise and non-degenerate. The AIR is only sound for
statements that pass, so every verifier runs it first. The zone registry must
validate polygons too.

## Pinned parameters and measured security

| | |
|---|---|
| Field / challenge | BabyBear / degree-4 extension (~124 bits) |
| Commitments, Fiat-Shamir | Keccak-256, hiding (salted leaves, randomised trace) |
| FRI | blowup 8, 100 queries, 20-bit query PoW, 10-bit batch PoW |
| Trace height | 2^8 to 2^14 rows (256 minimum: the zero-knowledge budget needs `2*(4*2 + queries)` rows) |
| Proven security (Plonky3 estimator) | 102 bits at 256 rows, 103 at 2^14 |
| Conjectured security | 105 bits or more |
| Proof size | about 268 KB at 256 rows, 301 KB at 512, 334 KB at 1024 |

These are classical soundness estimates. A quantum-secure analysis of the
Fiat-Shamir transform (quantum random oracle model) has not been done and
needs a cryptographer. Generic quantum collision search against a 256-bit hash
costs roughly 2^85, which bounds the Merkle commitments.

`vk_id` = `b6bf25e504350b1a471abf6939a5e89ba2cf531e46ed058c7e60e4a99386e8c6`
(a golden test pins it; any constant change alters it).

## Build requirements

Plonky3 0.8.0 needs a Rust newer than 1.91. It was tested here on 1.91.1 using
`RUSTC_BOOTSTRAP=1 RUSTFLAGS="-Zcrate-attr=feature(maybe_uninit_slice)"`, a
workaround for a library API that stabilised later. Use a current stable
toolchain and drop the workaround. The prover must be given real OS entropy
(`make_config` takes 64 bytes); the verifier ignores it.

## Tests (15, all passing; also with debug assertions on)

* reference model vs AIR on honest trips of 256, 512 and 1024 samples;
* every public value is bound by the proof (binding limbs, zone id, window, polygon);
* 9 cheating traces (outside by 1 m, coordinate overflow and wrap-around,
  17 s coverage hole, duplicate and backwards timestamps, late start, early
  end). In a release build the prover emits a proof for each and the
  **verifier rejects every one** (`cargo run --release --example cheat`);
* 407 bit-flips of a valid proof: no panic, never accepted;
* truncated, trailing, oversized and non-canonical inputs are classified as
  `Malformed`;
* zero-knowledge randomness changes the proof, the same entropy reproduces it.

## Not done yet

1. Stage 2: trip-commitment binding (see above).
2. Wiring `proof::verify_bytes` into `pq-verifier-guest::run_verifier` and the
   Go allowlist.
3. Aggregation: a 270-330 KB proof per 256+ samples will not fit per-vehicle on
   chain. Recursion or batching is required.
4. A cross-CPU determinism test (Plonky3's Keccak uses architecture-specific
   packed code; it should match the generic path bit for bit, but prove it on
   every CPU class your validators use).
