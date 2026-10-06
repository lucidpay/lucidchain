# Verifier wire protocol, version 1

Single source of truth for the Go adapter (`x/proofs/verifiers/pq.go`) and the
Rust guest (`pq-verifier-guest`). Change this file first, then both sides, and
bump `VERSION` for any incompatible change.

Transport: vsock, guest port 5001, host CID 2. A connection is persistent and
carries requests one at a time, in order. All integers are little-endian.

## Request

|--------------|--------------|-----------------|------------------------------------------|
| Offset       | Size         | Field           | Rule                                     |
|--------------|--------------|-----------------|------------------------------------------|
| 0            | 1            | `version`       | must be `1`                              |
| 1            | 32           | `vk_id`         | must be in the guest's compiled-in table |
| 33           | 4            | `pub_len`       | `<= 260` (`4 + 4*64`)                    |
| 37           | 4            | `proof_len`     | `<= 524288` (512 KiB)                    |
| 41           | `pub_len`    | `public_inputs` | canonical encoding, below                |
| 41+`pub_len` | `proof_len`  | `proof`         | opaque to the framing layer              |
|--------------|--------------|-----------------|------------------------------------------|

Lengths are checked against their caps before any buffer is allocated.

### Public inputs (canonical encoding)

`n: u32` followed by exactly `n` field elements as `u32`, where
`16 <= n <= 64` and every element is `< 2013265921` (BabyBear modulus,
2^31 - 2^27 + 1). No trailing bytes. The first 16 elements are the checkpoint
binding: the 256-bit value `hi << 128 | lo` split into 16-bit limbs, least
significant first.

## Response

|--------|------|--------------|
| Offset | Size | Field        |
|--------|------|--------------|
| 0      | 1    | `status`     |
| 1      | 32   | `image_hash` |
|--------|------|--------------|

|---------------|-------------|-------------------------|
| `status`      | Meaning     | Host treatment          |
|---------------|-------------|-------------------------|
| `0`           | VALID       | proof accepted          |
| `1`           | INVALID     | deterministic rejection |
| `2`           | MALFORMED   | deterministic rejection |
| anything else | not defined | **verifier fault**      |
|---------------|-------------|-------------------------|

`image_hash` must equal the hash pinned in the Go binary, otherwise the host
raises a verifier fault.

## Faults: no answer is an answer

The guest never turns an infrastructure problem into a status byte. On a bad
version, a length above its cap, a short read, a timeout, an unknown `vk_id`,
a missing verifier implementation, or any panic, the guest **closes the
connection without writing a response** (or the process aborts). The host
treats a closed connection, a transport error, a timeout, a wrong
`image_hash`, or an undefined `status` as `types.ErrVerifierFault`, and the
keeper must halt the node rather than record a verdict.

## Constants that must match on both sides

|-------------------|----------------|-----------------------------|---------------------|
| Constant          | Value          | Go                          | Rust                |
|-------------------|----------------|-----------------------------|---------------------|
| Field modulus     | 2013265921     | `babyBearModulus`           | `BABYBEAR_MODULUS`  |
| Binding limbs     | 16 x 16 bits   | `bindingLimbs`, `limbBits`  | `BINDING_LIMBS`     |
| Max public values | 64             | `maxPQPublicValues`         | `MAX_PUBLIC_VALUES` |
| Max proof bytes   | 524288         | `NewPQ(..., maxProofBytes)` | `MAX_PROOF_BYTES`   |
| `vk_id` length    | 32             | `VKIDLen`                   | `VKID_LEN`          |
| Status codes      | 0 / 1 / 2      | `StatusValid`...            | `STATUS_VALID`...   |
| `vk_id` allowlist | identical sets | `NewPQ(..., vkIDs, ...)`    | `KNOWN_VK_IDS`      |
|-------------------|----------------|-----------------------------|---------------------|

The two `vk_id` allowlists must be identical. A `vk_id` the Go side accepts but
the guest does not is a fault (deployment error), not a rejected proof.
