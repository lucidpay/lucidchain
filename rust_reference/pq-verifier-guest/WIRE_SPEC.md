# Verifier guest ABI, version 2 (in-process, wazero)

Single source of truth for the Go host (`x/proofs/verifiers/pq*.go`) and the
Rust guest (`pq-verifier-guest`, `src/wasm.rs`). Change this file first, then
both sides. Bump the frame version for any incompatible change to the frame.

There is no network or VM transport. The host runs one guest instance per
request and discards it afterwards.

## Guest module

Built for `wasm32-wasip1` with the `wasm` profile (see "Build and pinning").
The module has exactly these imports, all from `wasi_snapshot_preview1`, and
no others:

| Import              | Signature                     | Host behaviour                                          |
|---------------------|-------------------------------|---------------------------------------------------------|
| `environ_sizes_get` | `(i32, i32) -> i32`           | writes 0 to both pointers, returns 0 (empty env)        |
| `environ_get`       | `(i32, i32) -> i32`           | returns 0, writes nothing                               |
| `fd_write`          | `(i32, i32, i32, i32) -> i32` | returns 8 (`EBADF`); all output is discarded            |
| `proc_exit`         | `(i32) -> ()`                 | closes the module; the call fails, so it is a **fault** |

These are capability-free stubs, not WASI. The host provides no arguments,
environment variables, file descriptors, preopened directories, clock or
randomness. The guest must never depend on any of them.

Exports, exactly these three:

| Export         | Signature           |
|----------------|---------------------|
| `memory`       | the only exported memory |
| `alloc`        | `(i32) -> i32`      |
| `verify_frame` | `(i32, i32) -> i32` |

On wasm32 `usize` is 32 bits, so parameters written as `usize` in the Rust
source are `i32`/`u32` at the ABI. There is no `_start` and no `_initialize`;
the host instantiates with no start functions and never calls either.

The host rejects the module at startup if the import set, any signature, or
the export set differs from the above, and if its sha256 differs from the
pinned hash. A new import is therefore a spec change, not a build detail.

Limits per instance (host configuration, part of the node's consensus
assumptions):

* memory capped at 256 pages (16 MiB); exceeding the cap is a fault;
* one instance per request;
* at most `MaxInstances` instances at once.

Call sequence for one request:

1. `alloc(frame_len: u32) -> ptr: u32`. Returns 0 if `frame_len` is 0 or
   exceeds the largest valid frame. 0 is a host-side fault.
2. Host writes the frame at `ptr`.
3. `verify_frame(ptr: u32, frame_len: u32) -> u32`.
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

Two notes on the table:

* The `vk_id` is checked twice on purpose (defense in depth): the host against
  its allowlist, and the guest against its compiled-in id. Do not remove either.
* The guest-side frame errors (bad version, length mismatch, unknown `vk_id`)
  are unreachable if the host does its job. Reaching them in production
  indicates a host or deployment bug, which is why they are faults and never
  verdicts.

## Faults

The host never turns a fault into a verdict. Faults are:

* any trap;
* a call to `proc_exit`, with any exit code including 0;
* exceeding the memory cap;
* execution timeout;
* instantiate failure;
* `alloc` returning 0;
* a guest memory write out of range;
* any status above 2.

Timeouts apply to execution only, never to time spent waiting for a free
instance. After any trap or timeout the instance is already discarded (one
instance per request).

## Build and pinning

* Target `wasm32-wasip1`, Cargo profile `wasm` (defined in the workspace root:
  `panic = "abort"`, LTO, `overflow-checks = true`), built with `--locked`.
* The toolchain is pinned in `rust-toolchain.toml` and `Cargo.lock` is
  committed. Either one changing can change the wasm bytes.
* `scripts/build_wasm.sh` builds, copies the artifact to
  `x/proofs/verifiers/wasm/embed/pq_verifier.wasm`, and updates
  `pqWasmPinnedImageHex` in `pq_wire.go`. The embedded file, the constant, the
  toolchain file and the lock file are committed together.
* The host runs `PQSelfCheck` at startup on every allowlisted `vk_id` and
  refuses to start if any known answer gives a different status.

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
| Imports allowlist | 4 stubs      | `guestImports`             | n/a (determined by the build)     |
| Memory limit      | 256 pages    | `PQWasmConfig.MemoryPages` | n/a                               |
| Wasm image hash   | pinned       | `pqWasmPinnedImageHex`     | n/a (output of `build_wasm.sh`)   |

Rust asserts the shared constants against `av-zone` at compile time. Go and
Rust agree through shared test vectors (frame bytes, binding limb split).
