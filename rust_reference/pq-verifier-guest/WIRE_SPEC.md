# Verifier guest ABI, version 2 (in-process, wazero)

Single source of truth for the Go host (`x/proofs/verifiers/pq*.go`) and the
Rust guest (`pq-verifier-guest`, `src/wasm.rs`). Change this file first, then
both sides. Bump the frame version for any incompatible change.

There is no network or VM transport. The host runs one guest instance per
request and discards it afterwards.

## Guest module

Imports: none (no WASI, no host functions). Exports: `memory`, `alloc`,
`verify_frame`. No `_start`, no `_initialize`. The host rejects the module at
startup if the shape differs, and if its sha256 differs from the pinned hash.

Call sequence for one request:

1. `alloc(frame_len: usize) -> ptr`. Returns 0 if `frame_len` is 0 or exceeds
   the largest valid frame. 0 is a host-side fault.
2. Host writes the frame at `ptr`.
3. `verify_frame(ptr, frame_len) -> u32`.
4. Host discards the instance.

## Request frame (all integers little-endian)

| Offset       | Size        | Field           | Rule                                  |
|--------------|-------------|-----------------|---------------------------------------|
| 0            | 1           | `version`       | must be `2`                           |
| 1            | 32          | `vk_id`         | must be the guest's compiled-in id    |
| 33           | 4           | `pub_len`       | `<= 260` (`4 + 4*64`)                 |
| 37           | 4           | `proof_len`     | `<= 524288`                           |
| 41           | `pub_len`   | `public_inputs` | canonical encoding, below             |
| 41+`pub_len` | `proof_len` | `proof`         | opaque to the framing layer           |

`frame_len` must equal `41 + pub_len + proof_len` exactly.

### Public inputs (canonical encoding)

`n: u32`, then exactly `n` field elements as `u32`, `16 <= n <= 64`, each
`< 2013265921`. No trailing bytes. The first 16 elements are the checkpoint
binding as 16-bit limbs, least significant first. The statement for the
current `vk_id` requires `n == 35`; any other `n` is MALFORMED.

## Status

| `status`      | Meaning   | Host treatment               |
|---------------|-----------|------------------------------|
| `0`           | VALID     | proof accepted               |
| `1`           | INVALID   | deterministic rejection      |
| `2`           | MALFORMED | deterministic rejection      |
| `3`           | REFUSED   | **verifier fault**           |
| anything else | undefined | **verifier fault**           |

## Who decides what

| Condition                                                     | Decided by | Outcome            |
|---------------------------------------------------------------|------------|--------------------|
| `vk_id` not in allowlist, bad key length                      | host       | rejection (CheckTx)|
| `proof` empty or over cap, `pub` not canonical                | host       | rejection (CheckTx)|
| `n != 35`, element `>= modulus`, bad statement values         | guest      | MALFORMED          |
| proof does not decode canonically, shape outside pinned range | guest      | MALFORMED          |
| well-formed proof that fails verification                     | guest      | INVALID            |
| bad frame version, length mismatch, unknown `vk_id` in guest  | guest      | status 3 (fault)   |
| trap, out of memory, execution timeout, instantiate failure   | host       | fault              |

The host MUST run every host-side check before the keeper records anything, so
that attacker-chosen input can only produce a rejection, never a fault. A
fault halts the node.

## Faults

The host never turns a fault into a verdict. Timeouts apply to execution only,
never to time spent waiting for a free instance. After any trap or timeout the
instance is already discarded (one instance per request).

## Constants that must match on both sides

| Constant          | Value        | Go                         | Rust                              |
|-------------------|--------------|----------------------------|-----------------------------------|
| Frame version     | 2            | `wireVersion`              | `WIRE_VERSION`                    |
| Field modulus     | 2013265921   | `babyBearModulus`          | `BABYBEAR_MODULUS`                |
| Binding limbs     | 16 x 16 bits | `bindingLimbs`, `limbBits` | `BINDING_LIMBS`                   |
| Max public values | 64           | `maxPQPublicValues`        | `MAX_PUBLIC_VALUES`               |
| Max proof bytes   | 524288       | `PQWireMaxProofBytes`      | `MAX_PROOF_BYTES`                 |
| `vk_id` length    | 32           | `VKIDLen`                  | `VKID_LEN`                        |
| Status codes      | 0 / 1 / 2    | `StatusValid`...           | `STATUS_VALID`...                 |
| `vk_id` allowlist | identical    | `PQAllowedVKIDs`           | `av_zone::config::vk_id()`        |

Rust asserts the shared constants against `av-zone` at compile time. Go and
Rust agree through shared test vectors (frame bytes, binding limb split).
