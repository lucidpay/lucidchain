# av-zone: `geofence.av_zone` core statement (Stage 2, experimental)

A STARK (Plonky3 0.8.0, hash-based, no pairings or elliptic curves) proving that
a chunk of an autonomous vehicle's trip stayed inside a zone, without revealing
the trajectory, and that the hidden trajectory is the one a public digest
commits to.

Version string: `av-zone.stage2.v1`.

## What a valid proof says

There exist hidden samples `(x, y, t)` and a hidden salt such that:

1. every sample is inside, or on the boundary of, the public convex polygon;
2. times strictly increase and consecutive samples are at most **16 s** apart;
3. the first sample is within 15 s of `t_start` and the last within 15 s of
   `t_end`, so the whole window is covered;
4. the samples, hashed under the salt with the Poseidon2 chain in `commit.rs`,
   equal the public `trip_digest`. The AIR chains the permutations over the
   same `x, y, t` cells the geometry constraints use, so the trip proven inside
   the zone is, by construction, the trip whose digest is public.

## What it does NOT say (read this before deploying)

**The digest is only as real as whatever signs it.** A proof shows the hidden
samples hash to `trip_digest`; it does not show those samples came from a
vehicle. That is the attestor's job: a device or attestor must sign
`trip_digest` (ideally with the trip length and window), and the chain must
check that the signed digest equals public values `35..43` byte for byte.
`ComputeBinding` must also commit to the digest, zone and window, otherwise a
valid proof and its attestation can be replayed under another account. **None
of this is implemented in the Rust crate, and the Go side is not done yet.** Do
not register this `vk_id` for anything of value until it is.

Not covered: whether the vehicle left the zone *between* samples (a 16 s gap
allows a brief excursion). Register zones shrunk inward by `v_max * 16 s`.

Also not covered: the salt hides the trip from anyone who does not hold it. The
device and the prover app (whoever builds the proof) hold it and can still test
candidate trips against the digest.

## Public values (43 field elements, canonical u32)

| Index | Meaning |
|---|---|
| 0..16 | checkpoint binding, 16 limbs of 16 bits (same convention as the Go adapter) |
| 16 | `zone_id` |
| 17, 18 | `t_start`, `t_end` (each below `MAX_TIME`, which is the field modulus minus 2^19, so the time chain cannot wrap the field) |
| 19..35 | 8 polygon vertex slots `(x, y)`, coordinates < 2^14, counter-clockwise, unused trailing slots repeat the last vertex |
| 35..43 | `trip_digest`: 8 Poseidon2 elements (first 8 of the final sponge state) |

`Statement::from_values` validates all of it, including that the polygon is
convex, counter-clockwise and non-degenerate. The AIR is only sound for
statements that pass, so every verifier runs it first. The zone registry must
validate polygons too.

The salt is **not** a public value. The public layout, and therefore the wire
format and the Go public-input encoding, is unchanged by the salt.

## Trip commitment

A Poseidon2 sponge (BabyBear, width 16, rate 8, capacity 8), one permutation per
sample, Plonky3's default width-16 round constants:

```
state_0     = rate part 0; capacity[0] = CHAIN_DOMAIN (0x41565a31); capacity[1..8] = salt
              (salt[i] is loaded into state[8 + 1 + i])
state_{i+1} = Poseidon2(state_i + (x_i, y_i, t_i, 0, 0, 0, 0, 0))   added to the rate part
digest      = state_n[0..8]
```

`commit.rs` is the reference. Any other implementation (device firmware, Go
adapter) must reproduce it bit for bit. The tests pin two known-answer vectors:
`zero_salt_reproduces_the_unsalted_known_answer` (the all-zero salt reproduces
the original unsalted chain) and `salted_known_answer_vector` (salt
`[1, 2, 3, 4, 5, 6, 7]`, 16 samples; the exact setup is in the test's doc
comment). The commitment is part of the `vk_id`.

### The salt

* **Why.** The samples have low entropy (14-bit coordinates, a known time
  window, road-constrained routes). Without a salt anyone holding a candidate
  trajectory can hash it and compare against the public digest. Hiding FRI
  protects the proof, not a public input.
* **Size.** 7 field elements, about 217 bits.
* **Generation.** The device draws each element from a CSPRNG by **rejection
  sampling** below the modulus 2013265921. Never reduce. `av-zone-prove` and
  `prove_trip` refuse a non-canonical salt.
* **Use a fresh salt per trip chunk.** Reusing one lets whoever learns a
  single (trip, salt) pair test candidates against other digests made with it.
* **Handling.** Private witness. It is unconstrained in the AIR, never part of
  a statement, and `av-zone-prove` never writes it to disk. The device must
  hand it to the prover and keep it out of logs and attestations.
* **Digest width.** 8 elements is about 248 bits: roughly 124 bits of classical
  collision resistance, and less against quantum collision search. Widen
  `DIGEST_ELEMS` if the target is a clean 128 bits. That changes the public
  value count and the `vk_id`.

## Proving a trip

```
av-zone-prove <spec.txt> <out_dir> [--insecure-test-entropy-hex <128 hex>]
```

Spec (one item per line, `#` comments allowed):

| Key | Value |
|---|---|
| `binding_hex` | 64 hex: the 32-byte `x/proofs/types.Binding` (`ComputeBinding` output) |
| `salt_hex` | **required**, 56 hex: 7 u32 little-endian words, each below the modulus |
| `zone_id`, `t_start`, `t_end` | u32 |
| `polygon <x> <y>` | 3 to 8 lines, counter-clockwise; padded by repeating the last |
| `sample <x> <y> <t>` | count must be a power of two in [256, 1024] |

It writes `proof.bin` and `pub.bin` (canonical public inputs: u32 LE count, then
the u32 LE values: the exact bytes Go's `PQPublicInputs` makes), self-checks the
proof, and prints the `vk_id`. Zero-knowledge randomness comes from
`/dev/urandom` unless a test entropy is given, which prints a warning and
disables zero-knowledge.

`prove_trip(st, samples, salt, entropy)` is the library entry point. The salt
and the proof entropy are two different secrets: the salt is the device's, the
entropy is the prover's.

## Pinned parameters and measured security

| | |
|---|---|
| Field / challenge | BabyBear / degree-4 extension (~124 bits) |
| Commitments, Fiat-Shamir | Keccak-256, hiding (salted leaves, randomised trace) |
| FRI | blowup 8, 100 queries, 20-bit query PoW, 10-bit batch PoW |
| Trace height | 2^8 to 2^10 rows. 256 is the minimum (the zero-knowledge budget needs `2*(4*2 + queries)` rows); 1024 keeps the largest proof under `MAX_PROOF_BYTES` (512 KB) |
| Trace width | 569 columns (x, y, t; 14+14 coordinate bits, 8x29 edge bits, 4+4 time bits, one Poseidon2 permutation of 298 columns) |

Current figures. Earlier revisions of this README quoted numbers measured
before Stage 2 and before the trace height range changed; they are stale.

* Proven and conjectured security at every accepted height:
  `cargo test -p av-zone --test av_zone pinned_parameters_meet_the_security_target -- --nocapture`.
  The test requires at least 100 proven bits.
* Proof sizes at 256, 512 and 1024 rows:
  `cargo test -p av-zone --test av_zone honest_proofs_verify_at_several_sizes -- --nocapture`
  and `cargo run --release -p av-zone --example measure`.

These are classical soundness estimates. A quantum-secure analysis of the
Fiat-Shamir transform (quantum random oracle model) has not been done and
needs a cryptographer. Generic quantum collision search against a 256-bit hash
costs roughly 2^85, which bounds the Merkle commitments.

`vk_id` = `b81d579743df1e93a5f3a28f7b47392c2b075fab50dd3018024c62263bd9dbb8`

A golden test pins it. The id hashes `vk_descriptor()`, which folds in every
numeric constant, **not** the AIR's constraint logic. Bump `VERSION` in
`config.rs` whenever a constraint changes. The id must equal the allowlist
compiled into the Go binary.

## Build requirements

Plonky3 0.8.0 needs a current stable Rust; `BUILD.md` pins the toolchain for
the canonical wasm build. The old `RUSTC_BOOTSTRAP` workaround is no longer
needed. The prover must be given real OS entropy (`make_config` takes 64
bytes); the verifier ignores it.

## Tests

Run `cargo test -p av-zone` and also `cargo test -p av-zone --release`. In
debug builds the prover panics on an unsatisfied constraint, so negative tests
mostly show the prover refusing; in release the prover emits a proof and the
**verifier** must reject it. Use both.

* honest proofs of 256, 512 and 1024 samples, and the largest proof fitting the
  size cap;
* every public value is bound by the proof (binding limbs, zone id, window,
  polygon, each digest element);
* cheating traces: outside by 1 m, coordinate overflow and wrap-around, 17 s
  coverage hole, duplicate and backwards timestamps, late start, early end;
* **trip commitment:** the last-row output equals the public digest; every
  sample substitution that keeps geometry valid is rejected; corrupted hash
  cells are rejected; and **chain isolation**: a trace whose Poseidon2 block is a
  valid sponge run over a different trip (with that trip's digest public) is
  rejected for a change in any one of `x`, `y`, `t` at the first row, a middle
  row or the last row, including the case of a real out-of-zone trip hashed
  behind a fake in-zone one;
* **salt:** every salt element changes the digest, a proof is bound to its own
  digest, the prover refuses a mismatched or non-canonical salt, and a trace
  hashed under another salt is rejected;
* known-answer vectors for the commitment, and the golden `vk_id`;
* bit-flips of a valid proof: no panic, never accepted;
* truncated, trailing, oversized and non-canonical inputs are `Malformed`;
* zero-knowledge randomness changes the proof, the same entropy reproduces it.

`pq-verifier-guest` has the wire-framing tests (`tests/serve_frame.rs`),
including the embedded self-check proofs and the per-public-value mutation
table. The embedded fixtures are generated with the salted prover, so they must
be regenerated whenever the `vk_id` changes.

## Not done yet

1. Aggregation: a proof per 256+ samples will not fit per-vehicle on chain.
   Recursion or batching is required. Re-measure proof sizes first: the trace is
   now 569 columns wide.
2. A cross-CPU determinism test (Plonky3's Keccak uses architecture-specific
   packed code; it should match the generic path bit for bit, but prove it on
   every CPU class your validators use). A golden fingerprint of a
   fixed-entropy proof would serve as the prover-side check and would also
   catch AIR edits that forget to bump `VERSION`.