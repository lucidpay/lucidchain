# Technical Specification: `x/sidechain`, `x/checkpoint`, `x/attestor`, `x/proofs` Modules

**Draft v0.3**

> Companion to the project white paper. Covers all four modules across Tier 1 (`x/sidechain`, `x/checkpoint`), Tier 2 (`x/attestor`), and Tier 3 (`x/proofs`).

---

## 1. Scope

This spec defines the state, messages, queries, and invariants for four native modules:

- **`x/sidechain`** — registers sidechains, holds their bonds, tracks status and tier.
- **`x/checkpoint`** — accepts and finalizes signed checkpoints from registered, active sidechains.
- **`x/attestor`** — registers qualified attestors and schemas, accepts signed attestations about specific checkpoints, and handles disputes.
- **`x/proofs`** — registers pluggable validity-proof verifiers and accepts/verifies proofs about specific checkpoints.

All four are standard Cosmos SDK modules (`AppModule` interface), built with Protobuf-defined types, a `KVStore`-backed keeper, and standard `BaseApp` routing. No smart contract VM is involved. `x/attestor` and `x/proofs` both depend on `x/checkpoint` (via keeper interface) to confirm a referenced checkpoint exists, but `x/checkpoint` has no dependency on either — the assurance tiers are strictly additive layers on top of the Tier 1 anchor, never a replacement for it.

## 2. Module: `x/sidechain`

### 2.1 Purpose

The registry of record. Every sidechain that wants to submit checkpoints must first register here, post a bond, and declare an assurance tier. `x/checkpoint` consults this module (via keeper interface, not IBC or external call) to check that a submitter is registered, active, and using an authorized signer key.

### 2.2 State layout

| Key | Value | Description |
|---|---|---|
| `Sidechain/value/{id}` | `Sidechain` | Primary record, keyed by sidechain ID |
| `Sidechain/byOperator/{operator}/{id}` | `[]byte{}` | Secondary index for operator → sidechains lookup |
| `Sidechain/byTier/{tier}/{id}` | `[]byte{}` | Secondary index for tier-based queries |
| `Params/value` | `Params` | Module parameters |

Full proto definitions: see `proto/sidechain/sidechain.proto`, `tx.proto`, `query.proto`, `genesis.proto`.

### 2.3 Messages

| Message | Effect |
|---|---|
| `MsgRegisterSidechain` | Creates a `Sidechain` record in `PENDING` status; escrows bond from operator's account into the module account; moves to `ACTIVE` once bond is confirmed |
| `MsgUpdateSigners` | Replaces the signer key set and threshold; operator-only |
| `MsgRequestTierUpgrade` | Escrows additional bond and updates `tier`; for Tier 2 this also requires at least one available attestor for the sidechain's declared domain (checked once `x/attestor` exists) |
| `MsgInitiateExit` | Sets status to a pending-exit state; starts the `exit_cooldown_seconds` timer; bond released to operator after cooldown via `EndBlocker` |
| `MsgUpdateParams` | Governance-gated parameter update |

### 2.4 Keeper interface (consumed by `x/checkpoint`)

```go
type SidechainKeeper interface {
    GetSidechain(ctx sdk.Context, id string) (types.Sidechain, bool)
    IsActive(ctx sdk.Context, id string) bool
    IsAuthorizedSigner(ctx sdk.Context, id string, pubkey string) bool
    SignatureThreshold(ctx sdk.Context, id string) uint32
    RecordCheckpointAccepted(ctx sdk.Context, id string, sequence uint64, checkpointHash []byte) error
    SlashBond(ctx sdk.Context, id string, reason string, amount math.Int) error
}
```

`x/checkpoint` depends only on this interface, not on `x/sidechain`'s internal state layout. This keeps the modules loosely coupled and makes `x/sidechain` easier to extend later (e.g. adding delegated operators) without touching checkpoint logic.

### 2.5 Invariants

- A `Sidechain.id` is immutable and unique once registered.
- `status` transitions follow a fixed state machine: `PENDING → ACTIVE → {SUSPENDED, SLASHED, EXITED}`; `SUSPENDED → ACTIVE` is allowed after remediation; `SLASHED` and `EXITED` are terminal.
- Bond amount held in the module account must always equal the sum of all sidechains' current `bond_amount` fields (checked by a standard SDK invariant function, run periodically in testing and optionally in production).
- `signature_threshold` must be ≤ `len(checkpoint_signer_pubkeys)` and ≥ 1.

### 2.6 EndBlocker responsibilities

- Check each `ACTIVE` sidechain's `last_checkpoint_at` against `checkpoint_interval_seconds` + `missed_checkpoint_grace_seconds`; move to `SUSPENDED` if exceeded, and emit an event.
- Release bonds for sidechains whose exit cooldown has elapsed.

## 3. Module: `x/checkpoint`

### 3.1 Purpose

Accepts one message type — a signed checkpoint — validates it against the sidechain's registration, and finalizes it into an append-only, per-sidechain sequence. This is the module that actually implements "anchoring."

### 3.2 State layout

| Key | Value | Description |
|---|---|---|
| `Checkpoint/value/{sidechain_id}/{sequence}` | `Checkpoint` | Primary record |
| `Checkpoint/latest/{sidechain_id}` | `uint64` (sequence) | Pointer to the latest accepted sequence, for O(1) latest-checkpoint lookups |
| `Params/value` | `Params` | Module parameters |

### 3.3 Message handling: `MsgSubmitCheckpoint`

Handler logic, in order (any failure aborts the whole message — no partial state changes):

1. **Resolve sidechain.** Call `sidechainKeeper.GetSidechain(sidechain_id)`. Fail if not found.
2. **Check status.** Fail unless sidechain status is `ACTIVE`.
3. **Check sequencing.** `sequence` must equal `Checkpoint/latest/{sidechain_id]} + 1` (or `1` if no prior checkpoint). Reject gaps and replays.
4. **Check chaining.** `previous_checkpoint_hash` must equal the hash of the checkpoint at `sequence - 1` (or be empty for `sequence == 1`).
5. **Verify signatures.** For each entry in `signatures`, confirm `pubkey` is in the sidechain's `checkpoint_signer_pubkeys` and the signature is valid over the canonical signing payload (§4.3). Count valid, non-duplicate signatures and confirm the count meets `signature_threshold`.
6. **Check batch limits.** `record_count ≤ Params.max_records_per_checkpoint`; `len(data_pointer) ≤ Params.max_data_pointer_bytes`.
7. **Compute fee.** `fee = base_fee + per_record_fee * record_count`. Deduct from `submitter`'s account (standard SDK fee/bank flow — may be covered via `x/feegrant` if the submitter is a delegated gateway).
8. **Persist.** Write the `Checkpoint` at `{sidechain_id, sequence}`, update the latest-sequence pointer, and call `sidechainKeeper.RecordCheckpointAccepted(...)` to update the sidechain's `last_checkpoint_height`, `last_checkpoint_hash`, and `last_checkpoint_at`.
9. **Emit event.** `EventCheckpointAccepted{sidechain_id, sequence, state_root, checkpoint_hash, finalized_height}` — this is what off-chain indexers, explorers, and IBC light clients (later) will consume.

### 3.4 Canonical checkpoint hash

```
checkpoint_hash = SHA-256(
    sidechain_id            || 0x00 ||
    uint64_be(sequence)      || 0x00 ||
    state_root                       ||
    previous_checkpoint_hash
)
```

Byte-exact encoding (field separators, endianness) is fixed here because it's consensus-critical: every validator must compute the same hash independently. `0x00` separators are used between variable-length string fields to avoid ambiguity (e.g. `"ab"+"c"` vs `"a"+"bc"`).

### 3.5 Merkle inclusion proofs (off-chain)

The on-chain `state_root` is a Merkle root over a batch of off-chain records, computed by the sidechain (or its edge aggregators, for high-volume IoT sources). The module does not verify individual records — only the root. A relying party who wants to prove a specific record was included:

1. Obtains the original record and its Merkle path from the sidechain operator (or its data store per `data_pointer`).
2. Recomputes the root by hashing up the path.
3. Compares against the on-chain `state_root` for the relevant `{sidechain_id, sequence}` via a standard `Checkpoint` query.

This proof mechanism is entirely off-chain and language/library-agnostic — the module only needs to guarantee that `state_root` is correct and immutable once finalized.

### 3.6 Invariants

- `sequence` values for a given `sidechain_id` are strictly increasing with no gaps, starting at 1.
- Each stored `Checkpoint.previous_checkpoint_hash` matches the actual hash of the prior checkpoint (self-consistency of the chain can be verified by replaying all checkpoints for a sidechain).
- A `Checkpoint` is immutable once written — no message in this module updates or deletes an existing checkpoint.

### 3.7 Fee design notes

Batching is what keeps this workable for high-volume sources (e.g. IoT). The `per_record_fee` deliberately doesn't scale linearly with true per-event cost — it's a fraction of it, since the marginal cost to the chain is closely tied to `O(1)` per checkpoint (one state write, one signature check set) regardless of how many off-chain records the root represents. Governance should tune `max_records_per_checkpoint` alongside block gas/size limits so a single checkpoint tx can't be used to smuggle disproportionate off-chain claims relative to what's actually been verified (the batch root, not its contents).

## 4. Module: `x/attestor`

### 4.1 Purpose

Implements Tier 2. Separate, staked attestors — certification bodies, auditors, regulators — publish signed claims about a specific `{sidechain_id, checkpoint_sequence}`, under a governance-approved schema that fixes exactly what the claim does and does not assert. Because falsity of a real-world claim is usually not objectively checkable on-chain, the module includes a bonded dispute process rather than automatic slashing.

### 4.2 State layout

| Key | Value | Description |
|---|---|---|
| `Attestor/value/{id}` | `Attestor` | Primary attestor record |
| `Attestor/byDomain/{domain}/{id}` | `[]byte{}` | Secondary index |
| `Schema/value/{schema_id}` | `AttestationSchema` | Governance-published schema |
| `Attestation/value/{id}` | `Attestation` | Primary attestation record |
| `Attestation/byCheckpoint/{sidechain_id}/{sequence}/{attestation_id}` | `[]byte{}` | Secondary index for "all attestations about this checkpoint" |
| `Dispute/value/{id}` | `Dispute` | Primary dispute record |
| `Params/value` | `Params` | Module parameters |

### 4.3 Messages

| Message | Effect |
|---|---|
| `MsgRegisterAttestor` | Creates an `Attestor` in `PENDING`, escrows bond, activates once confirmed |
| `MsgUpdateAttestorSigners` | Rotates signer keys/threshold; operator-only |
| `MsgInitiateAttestorExit` | Voluntary deregistration, standard cooldown pattern (mirrors `x/sidechain`) |
| `MsgSubmitAttestation` | Validates schema authorization, checkpoint existence, and signatures; writes an `Attestation` in `ACTIVE` status |
| `MsgRaiseDispute` | Any account can challenge an `ACTIVE` attestation within its schema's `dispute_window_seconds`, posting a bond; moves attestation to `DISPUTED` |
| `MsgResolveDispute` | Called by `dispute_resolver_authority` (initially the gov module); sets `upheld`/`OVERTURNED` or dismisses; on overturn, may trigger `SlashBond` on the attestor and returns the challenger's bond with a reward; on dismissal, the challenger's bond is forfeited to discourage spurious disputes |
| `MsgCreateAttestationSchema` / `MsgRetireAttestationSchema` | Governance-only; schemas are immutable once created (retire, don't edit, to preserve the historical meaning of past attestations) |
| `MsgUpdateParams` | Governance-gated parameter update |

### 4.4 `MsgSubmitAttestation` handler logic

1. **Resolve attestor.** Fail if not found or not `ACTIVE`.
2. **Check schema authorization.** `schema_id` must be in the attestor's `authorized_schema_ids`, and the schema must be `active`.
3. **Resolve checkpoint.** Call into `x/checkpoint`'s keeper to confirm `{sidechain_id, checkpoint_sequence}` exists. Fail if not found — an attestation can never reference a nonexistent or future checkpoint.
4. **Verify signatures** against the attestor's registered keys and threshold, same pattern as `x/checkpoint` §3.3 step 5.
5. **Check payload size** against `max_claim_payload_bytes`.
6. **Compute expiry** as `issued_at + schema.validity_period_seconds`.
7. **Persist** the `Attestation` with a deterministic ID (`hash(schema_id, sidechain_id, sequence, attestor_id)`), preventing the same attestor from double-publishing under the same schema for the same checkpoint.
8. **Emit event.** `EventAttestationPublished{...}`.

### 4.5 Keeper interface (consumed by higher-level queries / future modules)

```go
type AttestorKeeper interface {
    GetAttestor(ctx sdk.Context, id string) (types.Attestor, bool)
    IsActive(ctx sdk.Context, id string) bool
    GetActiveAttestationsForCheckpoint(ctx sdk.Context, sidechainID string, sequence uint64) []types.Attestation
    SlashBond(ctx sdk.Context, attestorID string, reason string, amount math.Int) error
}
```

### 4.6 Design notes specific to this module

- **Schema immutability is deliberate.** Once published, a schema's `claim_description` and `exclusions` are fixed. This means a relying party reading an old attestation always knows exactly what was and wasn't claimed at the time, even if the schema is later retired in favor of a v2.
- **Dispute resolution starts centralized (gov module) by design**, not as an oversight. A dedicated decentralized arbitration mechanism is a larger design problem (juror selection, incentive compatibility, evidence handling) that deserves its own spec once there's real dispute volume to learn from — bolting on a placeholder mechanism now would likely need to be redesigned anyway.
- **Reputation score is explicitly informational.** It must never gate consensus-critical logic (e.g. signature verification), only off-chain UX like sorting attestors in an explorer. This avoids a whole category of gaming/manipulation attacks on a supposedly "trust-scored" on-chain number.
- **Conflict-of-interest enforcement** (an attestor may not be the sidechain's own operator) is checked at `MsgRegisterAttestor` time by comparing `operator` addresses, and re-checked at `MsgSubmitAttestation` time against the target sidechain's registered `operator`.

### 4.7 Open item: HSM-backed signing for attestor keys

Attestors registered under this module are expected to be institutional parties — certification bodies, auditors, regulators — who will very likely require hardware-backed key custody as a condition of participating credibly, rather than accepting file- or OS-keychain-based key storage for their `signer_pubkeys`. This is distinct from validator consensus-key signing and needs its own answer.

**Current state (as of this draft):**

- The Cosmos SDK's standard application-level keyring (`os`, `file`, `kwallet`, `pass`, `test` backends) has no native PKCS#11/HSM backend. An attestor operator wanting HSM-backed signing today would need a custom integration: a PKCS#11 client library producing raw signatures on the HSM, fed into the SDK's offline/multi-step transaction signing flow.
- **`Cosmos-KMS`** (`github.com/cosmos/kms`), the SDK's new remote-signing service (SDK v0.55+, which this project targets), supports PKCS#11 and AWS KMS backends today, but its *confirmed* scope is CometBFT consensus signing (votes, proposals, vote extensions) — i.e. validator block-signing, not application-level message signing.
- Cosmos-KMS's stated longer-term direction, per its own repository description, is to become a general remote-signing service covering **IBC relaying and attestation** signing in addition to consensus signing, via an optional `grpc` `SignerService` interface alongside its privval interface. This is a closer conceptual match to what `x/attestor` needs, but as of this draft its documentation is described as shipping with a separate "interoperability release," and it should not be assumed production-ready or fully specified yet.

**Implication for this module:** `MsgRegisterAttestor` and `MsgSubmitAttestation` are deliberately signature-scheme-agnostic in this spec — they validate a signature against a registered pubkey without caring how or where that signature was produced. This means no protocol changes are required if attestor operators adopt HSM-backed signing later, whether via a custom PKCS#11 integration or via Cosmos-KMS's remote-signing service once it covers this use case. The dependency is entirely operational (how an attestor's operator chooses to run their signing infrastructure), not on-chain.

**Action:** revisit this before onboarding the first real (non-test) attestor, particularly for regulator-operated attestors where HSM custody may be a hard requirement rather than a preference. Track `cosmos/kms`'s `grpc` `SignerService` maturity as the preferred path if it stabilizes in time; otherwise plan for a custom PKCS#11 signing integration in the attestor operator's off-chain submission tooling.

## 5. Module: `x/proofs`

### 5.1 Purpose

Implements Tier 3. Unlike `x/attestor`, this module doesn't rely on anyone's honesty — it cryptographically verifies a validity proof on-chain against a governance-registered verifier, synchronously, in the message handler. A proof is either checked and correct, or the transaction fails; there is no dispute window because there is nothing to dispute.

### 5.2 State layout

| Key | Value | Description |
|---|---|---|
| `Verifier/value/{proof_system_id}` | `VerifierRegistration` | Registered proof-system verifier |
| `Proof/value/{id}` | `ProofRecord` | Persisted, verified proof |
| `Proof/byCheckpoint/{sidechain_id}/{sequence}/{proof_id}` | `[]byte{}` | Secondary index |
| `Params/value` | `Params` | Module parameters |

### 5.3 Verifier implementations are Go code, not on-chain data

`VerifierRegistration.verification_key` is opaque on-chain state — the actual cryptographic verification (pairing checks, FRI, whatever the proof system requires) is implemented as compiled Go code registered against a `proof_system_id` at app-wiring time:

```go
type ProofVerifier interface {
    Verify(verificationKey, publicInputs, proof []byte) (bool, error)
}

// App-level registration, e.g. in app.go:
proofsKeeper.RegisterVerifierImpl("groth16-bn254", groth16.NewVerifier())
proofsKeeper.RegisterVerifierImpl("risc0-v1", risc0.NewVerifier())
```

Governance can only register/deprecate the on-chain `VerifierRegistration` **metadata** for a `proof_system_id` that already has a corresponding Go implementation compiled into the running binary — governance cannot make the chain accept a genuinely new proof system without a coordinated software upgrade. This is a deliberate safety property: arbitrary verification logic is never accepted as untrusted on-chain data.

### 5.4 `MsgSubmitProof` handler logic

1. **Resolve checkpoint.** Confirm `{sidechain_id, checkpoint_sequence}` exists via `x/checkpoint`'s keeper.
2. **Resolve verifier.** Fail if `proof_system_id` is not registered or is `DEPRECATED`.
3. **Check size limits** on `proof` and `public_inputs`.
4. **Check public-input binding.** The registered verifier's `Verify` call is passed `public_inputs`; the module additionally requires that the checkpoint's `state_root` is derivable from (or explicitly included in) `public_inputs`, per a per-proof-system encoding convention, so a valid proof can't be replayed against an unrelated checkpoint.
5. **Call the verifier.** `ok, err := verifierImpl.Verify(reg.verification_key, public_inputs, proof)`. On `false` or error, **abort the transaction** — nothing is written, and the submitter still pays the base tx fee (gas), which discourages spamming invalid proofs.
6. **Compute and deduct fee** (`base_verification_fee`, floor only — see Params notes).
7. **Persist** the `ProofRecord` with `verified = true` and emit `EventProofVerified{...}`.

### 5.5 Keeper interface

```go
type ProofsKeeper interface {
    GetVerifier(ctx sdk.Context, proofSystemID string) (types.VerifierRegistration, bool)
    GetProofsForCheckpoint(ctx sdk.Context, sidechainID string, sequence uint64) []types.ProofRecord
    HasVerifiedClaim(ctx sdk.Context, sidechainID string, sequence uint64, claimID string) bool
}
```

### 5.6 Design notes specific to this module

- **Verification cost varies enormously by proof system** (a Groth16 pairing check is cheap; a large STARK can be expensive). `base_verification_fee` is deliberately a floor in this draft; a follow-up revision should add a per-`proof_system_id` fee multiplier once real gas-cost benchmarks exist for the specific verifiers chosen.
- **This module never blocks on Tier 1.** A checkpoint is finalized by `x/checkpoint` regardless of whether any proof is ever submitted for it. Proofs (and attestations) are additive evidence layered on top of an already-finalized anchor, which keeps the core anchoring path simple and keeps Tier 2/3 entirely optional per sidechain.
- **`claim_id` scoping mirrors `x/attestor`'s `schema_id`** deliberately, so the two tiers have a consistent mental model: a checkpoint can accumulate multiple narrow, named claims over time (from attestors, from proofs, or both) rather than one all-or-nothing verdict.

## 6. Cross-module interaction diagram

```
                    Sidechain operator's
                    checkpoint submitter
                            │
                            │  MsgSubmitCheckpoint
                            ▼
                  ┌────────────────────┐        GetSidechain() etc.     ┌────────────────────┐
                  │   x/checkpoint      │ ─────────────────────────────►│   x/sidechain       │
                  │  - validates         │◄───────────────────────────── │  - registry, bonds  │
                  │  - sequences         │                                │  - status/tier      │
                  │  - stores commitment │                                └────────────────────┘
                  └────────────────────┘
                       ▲            ▲
     GetCheckpoint()   │            │   GetCheckpoint()
                        │            │
        ┌───────────────┘            └───────────────┐
        │                                             │
┌───────────────┐                            ┌────────────────┐
│  x/attestor     │                            │  x/proofs        │
│  - registry      │                            │  - verifier       │
│  - schemas       │                            │    registry       │
│  - attestations  │                            │  - proof records  │
│  - disputes      │                            │    (sync-verified)│
└───────────────┘                            └────────────────┘
        │                                             │
        │ EventAttestationPublished                    │ EventProofVerified
        ▼                                             ▼
              Indexers / explorers / relying parties
              (a relying party queries x/checkpoint for the anchor,
               then x/attestor and/or x/proofs for whatever additional
               assurance the sidechain's declared tier provides)
```

## 7. Testing plan

- **Unit tests** per keeper method (registration edge cases, sequencing gaps, signature threshold logic, bond math).
- **Simulation tests** using the SDK's simulation framework to fuzz message sequences and check invariants hold under random operation.
- **Integration tests** on a local multi-validator testnet: register a sidechain, submit a realistic batch of checkpoints, confirm finality timing and fee deduction.
- **Load tests** specifically for the checkpoint path at high frequency/large `record_count`, since this is the module most exposed to smart-city-scale volume later.

## 8. Explicit non-goals for this draft

- No validation of off-chain record contents by any module — `x/checkpoint` stores a root, `x/attestor` stores a claim about a checkpoint, `x/proofs` cryptographically verifies a claim about a checkpoint; none of them inspect raw sidechain data.
- No IBC light client wiring yet — `EventCheckpointAccepted` (and the attestor/proof events) are designed to make that addition straightforward later.
- No decentralized dispute arbitration — `x/attestor` disputes resolve through the gov module as an interim authority; a dedicated arbitration mechanism is future work (§4.6).
- No concrete proof-system verifier implementations are chosen yet — `x/proofs` defines the on-chain registration and message flow, but selecting and integrating actual Go verifier libraries (e.g. for Groth16, Plonk, or a zkVM like RISC Zero) is a separate engineering task, likely scoped per target use case (geofence compliance, AML thresholds, etc.).
- No slashing conditions beyond bond math are finalized for `x/sidechain` — the exact fault conditions for `SlashBond` (e.g. two conflicting checkpoints at the same sequence, signed by the registered keys) need a dedicated fraud-detection spec before mainnet.
- No per-proof-system fee differentiation in `x/proofs` — `base_verification_fee` is a placeholder floor pending real gas benchmarks.
- No HSM/hardware-backed signing solution is chosen for attestor keys — see §4.7. The on-chain protocol doesn't need this decision made now, but it should be resolved operationally before onboarding a real (non-test) attestor.

---

*Next steps: keeper pseudocode → Go implementation for all four modules, CLI command definitions, REST/gRPC gateway config, a dedicated fraud/slashing conditions spec, selection of initial attestation schemas (hospitality revenue reporting is the leading candidate), selection of the first proof system to integrate for `x/proofs`, and resolution of attestor key-custody/HSM strategy (§4.7) ahead of first real attestor onboarding.*
