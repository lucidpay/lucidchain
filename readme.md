# LucidChain: A Public Assurance Layer for Private Chains

**Summary**
Lucidchain is a public, quantum-resistant, proof-of-stake (PoS) blockchain that acts as a neutral assurance layer for private sidechains. Sidechains keep their data and execution private and publish compact cryptographic commitments to our public chain. In return they receive a tamper-evident, independently timestamped record and, at higher tiers, signed attestations from qualified third parties.
Quantum resistance currently covers the chain's signatures (ML-DSA-65). Tier 3 proof verification uses gnark over BN254 and is not yet quantum-resistant (see §5.7).
The chain is deliberately minimal: no general-purpose smart contract VM, only a small set of purpose-built native modules.

# Technical Specification: `x/sidechain`, `x/checkpoint`, `x/attestor`, `x/proofs` Modules

**Draft v0.4**

> Companion to the project white paper. Covers all four modules across Tier 1 (`x/sidechain`, `x/checkpoint`), Tier 2 (`x/attestor`), and Tier 3 (`x/proofs`).
>
> Sections 2–4 describe the implemented behaviour of `x/sidechain`, `x/checkpoint` and `x/attestor`. §5 (`x/proofs`) describes the implemented behaviour too, together with the current verifier choice (gnark) and the planned microVM direction (§5.7).

---

## 1. Scope

This spec defines the state, messages, queries, and invariants for four native modules:

- **`x/sidechain`** — registers sidechains, holds their bonds, tracks status and tier.
- **`x/checkpoint`** — accepts and finalizes signed checkpoints from registered, active sidechains.
- **`x/attestor`** — registers qualified attestors and schemas, accepts signed attestations about specific checkpoints, and handles disputes.
- **`x/proofs`** — registers pluggable validity-proof verifiers and accepts/verifies proofs about specific checkpoints.

All four are standard Cosmos SDK modules (`AppModule` interface), built with Protobuf-defined types, a `KVStore`-backed keeper, and standard `BaseApp` routing. No smart contract VM is involved. `x/checkpoint` depends on `x/sidechain`. `x/attestor` depends on `x/checkpoint` (to confirm a referenced checkpoint exists and to bind its hash into the signatures) and on `x/sidechain` (tier and conflict-of-interest checks). `x/proofs` depends on `x/checkpoint`. `x/sidechain` depends on none of the other three, and `x/checkpoint` depends on neither `x/attestor` nor `x/proofs` — the assurance tiers are strictly additive layers on top of the Tier 1 anchor, never a replacement for it.

## 2. Module: `x/sidechain`

### 2.1 Purpose

The registry of record. Every sidechain that wants to submit checkpoints must first register here, post a bond, and declare an assurance tier. `x/checkpoint` consults this module (via keeper interface, not IBC or external call) to check that a sidechain exists, is `ACTIVE`, and which signer keys and threshold apply. `x/attestor` reads it for the tier and conflict-of-interest checks.

### 2.2 State layout

| Collection | Key → value | Description |
|---|---|---|
| `Sidechains` | `id` → `Sidechain` | Primary record, keyed by sidechain ID |
| `ExitQueue` | `(bond_return_at as unix seconds, id)` | `EXITED` sidechains awaiting their bond return, ordered by due time |
| `Params` | — → `Params` | Module parameters |

There are no secondary indexes. The `Sidechains` query filters by tier or status by scanning the map; an index, if added later, should be maintained in the single place that writes sidechain records.

Proto definitions: `proto/lucidchain/sidechain/v1/{sidechain,params,tx,query,genesis}.proto`.

**`Sidechain` record**

| Field | Meaning |
|---|---|
| `id`, `name`, `metadata_uri` | Identity and off-chain metadata document |
| `operator` | Bech32 address of the registration owner; the only account that can run operator actions |
| `signer_keys`, `signature_threshold` | Checkpoint signer public keys (SDK `Any`; order is significant, because a signature refers to a position in this list) and the number of distinct signers required per checkpoint |
| `signer_set_version` | Starts at 1; incremented on every change to `signer_keys` or `signature_threshold`. Checkpoints record the version they were signed under |
| `tier`, `status` | Assurance tier (§2.7) and lifecycle status (§2.5) |
| `bond` | Bond currently escrowed, as a `Coin` |
| `checkpoint_interval_seconds` | Expected interval between checkpoints |
| `last_checkpoint_sequence`, `last_checkpoint_height`, `last_checkpoint_hash`, `last_checkpoint_at` | Owned by this module, updated only by `x/checkpoint` through `RecordCheckpoint`; empty until the first checkpoint is accepted |
| `registered_at`, `activated_at` | Registration time, and when the sidechain first became `ACTIVE` (the baseline for a missed-checkpoint rule before its first checkpoint) |
| `exit_requested_at`, `bond_return_at` | Set by `MsgInitiateSidechainExit` |
| `slashed_at`, `slash_reason` | Set by the most recent slash |

**`Params`** (all set by governance through `MsgUpdateParams`):

| Param | Meaning |
|---|---|
| `min_bond_by_tier` | One minimum bond per tier; duplicates and `UNSPECIFIED` are rejected by validation |
| `missed_checkpoint_grace_seconds` | Grace period for the missed-checkpoint liveness rule (§2.6) |
| `exit_cooldown_seconds` | Delay between an exit request and the bond return |
| `max_signer_keys` | Maximum signer keys per sidechain. ML-DSA-65 public keys are 1,952 bytes each, so this bounds per-sidechain state size |
| `allowed_pubkey_type_urls` | Public-key type URLs accepted as checkpoint signers (for example `/cosmos.crypto.mldsa65.PubKey`). For a quantum-resistant deployment set this to ML-DSA-65 only; adding classical types re-opens the sidechain to quantum forgery of its checkpoint signatures |
| `auto_activate` | When true, registration creates the sidechain `ACTIVE` immediately (permissionless registration, the bond is the only gate). When false, it stays `PENDING` until governance activates it |

### 2.3 Messages

| Message | Signer | Effect |
|---|---|---|
| `MsgRegisterSidechain` | operator | Validates id, name, metadata URI, checkpoint interval, tier and signer set; the bond must use the denom of the tier's minimum bond and be at least that amount; the id must be unused. Escrows the bond into the module account and stores the record with `signer_set_version` 1. Status is `PENDING`, or `ACTIVE` when `auto_activate` is set. Nothing is stored or escrowed if any check fails |
| `MsgActivateSidechain` | authority | `PENDING` → `ACTIVE`. Optional `expected_signer_set_version`: when non-zero, activation fails unless it equals the sidechain's current `signer_set_version`, so a governance proposal activates the signer set it reviewed and not one rotated in between |
| `MsgUpdateSidechainSigners` | operator + current signer set | Replaces the signer keys and threshold (same validation as registration) and increments `signer_set_version`. Besides the operator's signature on the transaction, the message must carry `current_set_signatures`: at least `signature_threshold` signatures from distinct members of the **current** set over `SignerRotationSignDoc{chain_id, sidechain_id, current_signer_set_version, new_signer_keys, new_signature_threshold, not_after_height}` (sign bytes `"lucidchain/sidechain/v1/rotation-signdoc\0" ‖ proto.Marshal(doc)`). `not_after_height` is required and the rotation is refused once the chain is past it; the current version in the doc makes an approval valid for exactly one rotation. Each verified signature costs `Params.rotation_signature_gas`. The approver bitmap is stored as `last_rotation_approver_bitmap`. Allowed while `PENDING`, `ACTIVE` or `SUSPENDED` (compromised keys must be rotatable without a governance resume); refused for `SLASHED` and `EXITED`. A checkpoint in flight that was signed under the old version is rejected by `x/checkpoint`, so rotate between checkpoints. `tools/signrotation` produces the approvals off-chain for both key profiles (secp256k1 and ML-DSA-65); the `--signer-keys` order on the CLI must match the `-new-key` order given to the tool, because the ordered list is part of the signed document. Rationale: without the co-signature the operator's single account key could replace the whole post-quantum signer set in one message (review finding N9) |
| `MsgUpdateSidechain` | operator | Changes `name`, `metadata_uri` and `checkpoint_interval_seconds`; allowed in any status except `EXITED` |
| `MsgUpgradeSidechain` | operator | Raises `tier` (CLI: `upgrade-tier`) and escrows `additional_bond`, so that the resulting bond (current bond + `additional_bond`, same denom) is at least `Params.min_bond_by_tier` for the new tier. Upward only; a tier may be skipped. Allowed while `PENDING`, `ACTIVE` or `SUSPENDED`; rejected for `SLASHED` and `EXITED`. Does not change `status`. See §2.7 |
| `MsgInitiateSidechainExit` | operator | Allowed from `PENDING`, `ACTIVE`, `SUSPENDED` or `SLASHED`. Sets status `EXITED` immediately (the sidechain stops accepting checkpoints), sets `bond_return_at = now + exit_cooldown_seconds` and queues the sidechain. The remaining bond is returned by `ProcessExits` once the cooldown has passed. Records already accepted by `x/checkpoint`, `x/attestor` and `x/proofs` are unaffected |
| `MsgSuspendSidechain` | authority | `ACTIVE` → `SUSPENDED`; a suspended sidechain cannot submit checkpoints |
| `MsgResumeSidechain` | authority | `SUSPENDED` → `ACTIVE` |
| `MsgSlashSidechain` | authority | Burns a fraction of the current bond; see §2.8 |
| `MsgUpdateParams` | authority | Governance-gated parameter update; all parameters must be supplied |

"Authority" is the module authority, the `x/gov` module account unless overridden.

### 2.4 Keeper interface (consumed by other modules)

```go
// What x/checkpoint depends on. x/attestor needs only GetSidechain.
type SidechainKeeper interface {
    // GetSidechain returns the registration record, with signer_keys unpacked.
    GetSidechain(ctx context.Context, id string) (sidechaintypes.Sidechain, error)

    // RecordCheckpoint updates the sidechain's last-checkpoint fields.
    RecordCheckpoint(ctx context.Context, id string, lc_sequence uint64, height int64, hash []byte) error
}
```

The consuming modules depend only on this interface, not on `x/sidechain`'s internal state layout, which keeps them loosely coupled and makes `x/sidechain` easier to extend later (for example with delegated operators). No other module has a way to slash a sidechain: slashing is reachable only through `MsgSlashSidechain`.

### 2.5 Status machine and invariants

Status transitions, and what triggers each:

| From → to | Trigger |
|---|---|
| (new) → `PENDING` or `ACTIVE` | `MsgRegisterSidechain`; `ACTIVE` only with `auto_activate` |
| `PENDING` → `ACTIVE` | `MsgActivateSidechain` |
| `ACTIVE` → `SUSPENDED` | `MsgSuspendSidechain` (and the missed-checkpoint rule, §2.6) |
| `SUSPENDED` → `ACTIVE` | `MsgResumeSidechain` |
| `PENDING`, `ACTIVE`, `SUSPENDED` → `SLASHED` | `MsgSlashSidechain`; a `SLASHED` sidechain can be slashed again |
| `PENDING`, `ACTIVE`, `SUSPENDED`, `SLASHED` → `EXITED` | `MsgInitiateSidechainExit` |

`EXITED` is terminal. `SLASHED` is not: the operator can still exit and recover the remaining bond, but nothing returns a `SLASHED` sidechain to `ACTIVE`. An `EXITED` sidechain that is slashed stays `EXITED` and just loses the slashed part of its bond.

Invariants:

- A `Sidechain.id` is immutable and unique once registered.
- The bond held in the module account equals the sum of all sidechains' current `bond` amounts, across registration, upgrades (the escrowed top-up goes into the same account), slashes (the burn matches the bond reduction) and exits. The keeper tests assert this after each operation.
- `1 ≤ signature_threshold ≤ len(signer_keys)`, `len(signer_keys) ≤ max_signer_keys`, and every key's type URL is in `allowed_pubkey_type_urls`.
- `signer_set_version` only increases, and by one per change.
- `tier` never decreases: there is no downgrade message, and slashing does not change `tier`.
- At the moment of a `MsgUpgradeSidechain`, the resulting bond is at least the new tier's minimum. This is not re-checked later: a governance increase of `min_bond_by_tier`, or a slash, can leave a sidechain holding less bond than its tier's current minimum.
- `last_checkpoint_*` fields are written only by `RecordCheckpoint`.

### 2.6 EndBlocker responsibilities

- **Bond return.** `ProcessExits` returns the remaining bond of every `EXITED` sidechain whose `bond_return_at` has passed.
- **Missed-checkpoint suspension.** An `ACTIVE` sidechain whose last checkpoint (or, before its first checkpoint, `activated_at`) is older than `checkpoint_interval_seconds + missed_checkpoint_grace_seconds` is moved to `SUSPENDED`, with an event.

### 2.7 Assurance tiers and tier upgrades

A sidechain declares a tier at registration and can raise it later with `MsgUpgradeSidechain`. Each tier has a minimum bond in `Params.min_bond_by_tier` (one entry per tier, set by governance).

| Tier | Enum | What it enforces |
|---|---|---|
| 1 | `ASSURANCE_TIER_NOTARIZED` | Timestamped commitment only. Nothing beyond the Tier 1 anchor. |
| 2 | `ASSURANCE_TIER_ATTESTED` | `x/attestor` accepts attestations about the sidechain's checkpoints only for sidechains at this tier or higher (§4.4). |
| 3 | `ASSURANCE_TIER_PROVEN` | `x/checkpoint` requires every checkpoint to carry a valid `zk_anchor` (§3.8). |

**`MsgUpgradeSidechain` rules**

- Only the sidechain's operator can send it.
- The new tier must rank strictly above the current one. Skipping a tier is allowed, because the tiers are additive layers. There are no downgrades: releasing bond immediately would bypass the exit cooldown that gives governance time to slash. If downgrades are added later, the released amount should go through a delayed queue like the exit queue.
- The sidechain must be `PENDING`, `ACTIVE` or `SUSPENDED`. A `SLASHED` sidechain must not be able to buy its way back toward good standing, and an `EXITED` one is leaving.
- The bond rule compares the resulting bond with the new tier's minimum, not with the old tier's minimum. The `additional_bond` coin must use the bond denom and may be zero if the current bond already covers the new tier. Example with minimums of 1,000,000 / 5,000,000 / 10,000,000: a sidechain holding 1,000,000 needs a top-up of 4,000,000 to reach `ATTESTED`, then another 5,000,000 to reach `PROVEN`; a direct jump to `PROVEN` needs 9,000,000.
- All checks run before any escrow, so nothing is escrowed or stored if a check fails.
- Status is unchanged: upgrading never activates a `PENDING` sidechain or resumes a `SUSPENDED` one.
- The handler emits `sidechain_tier_upgraded` with `sidechain_id`, `previous_tier`, `new_tier`, `additional_bond` and the resulting `bond`.

**What the upgrade does not check.** It is gated by bond only. It does not require an attestor to exist for `ATTESTED`, and it does not require anything from `x/proofs` for `PROVEN`. A tier's requirements are enforced where they are used (table above), so an operator can pay for a tier that nothing currently serves; the cost to the operator is its own locked bond. A check for attestor availability needs a `domain` on the sidechain and is left for later (§8).

**Operator order for `PROVEN`.** The anchor is covered by the checkpoint signatures, so a checkpoint signed without one cannot be fixed afterwards. Update the off-chain signer to include anchors and to derive `state_root` from them first (anchors are accepted at any tier), then upgrade. The upgrade is operator-only and has no way back; see the note on signer rotation above for why the operator key alone is a single point of failure.

```bash
# <tier> is the short enum name: notarized | attested | proven
lucidchaind tx sidechain upgrade-tier <sidechain-id> <tier> <additional-bond> \
  --from <operator> --chain-id <chain-id> --gas auto --gas-adjustment 1.5 --yes

# example: 1,000,000 bond -> ATTESTED
lucidchaind tx sidechain upgrade-tier hospitality-platform-01 attested 4000000stake \
  --from validator --chain-id my-testnet-1 --gas auto --gas-adjustment 1.5 --yes
```

### 2.8 Slashing and where slashed bonds go

`MsgSlashSidechain` is authority-only and takes a `slash_fraction` in (0, 1] and a non-empty `reason`.

- The slashed amount is `bond × slash_fraction`, truncated to a whole unit. It must be positive, so a tiny fraction of a small bond that truncates to zero is rejected.
- The amount is **burned** from the module account through the bank module. Nobody is paid: no validator, community pool or other account receives it, and total supply drops by that amount. The module account therefore needs the bank `Burner` permission. A challenger-reward model like the one in `x/attestor` §4.6 does not exist for sidechains; routing slashed bonds to a pool instead would be a change in the slash path.
- The record's `bond` drops by exactly the burned amount, `slashed_at` and `slash_reason` are set, and the status becomes `SLASHED` (an `EXITED` sidechain stays `EXITED`). The `sidechain_slashed` event carries the slashed amount, the remaining bond, the new status and the reason.
- Any slash is terminal for checkpoints: a `SLASHED` sidechain is not `ACTIVE`, cannot be resumed or activated, and cannot upgrade its tier. Its operator can still call `MsgInitiateSidechainExit` to recover whatever bond is left after the cooldown. Graduated penalties would need a path from `SLASHED` back to `ACTIVE`, which does not exist today.
- Slashing does not change the tier, so a `PROVEN` sidechain slashed to a small bond keeps its tier label. Its checkpoints stop anyway, because it is no longer `ACTIVE`.

The exact fault conditions that justify a slash are not specified here; see §8.

### 2.9 Queries

| Query | Description | REST |
|---|---|---|
| `Sidechain` | One record by `id` | `/lucidchain/sidechain/v1/sidechains/{id}` |
| `Sidechains` | Paginated list; optional filters by `tier` and `status` (`UNSPECIFIED` means no filter) | `/lucidchain/sidechain/v1/sidechains` |
| `Params` | Module parameters | `/lucidchain/sidechain/v1/params` |

## 3. Module: `x/checkpoint`

### 3.1 Purpose

Accepts one message type — a signed checkpoint — validates it against the sidechain's registration, and finalizes it into an append-only, per-sidechain lc_sequence. This is the module that actually implements "anchoring."

### 3.2 State layout

| Prefix | Key → value | Description |
|---|---|---|
| `checkpoint/value/` | `(sidechain_id, lc_sequence)` → `Checkpoint` | Primary record |
| `checkpoint/latest/` | `sidechain_id` → `uint64` | The latest accepted lc_sequence, for O(1) latest-checkpoint lookups |
| `p_checkpoint` | — → `Params` | Module parameters |

**`Checkpoint` record:** `sidechain_id`, `lc_sequence`, `state_root`, `previous_checkpoint_hash`, `record_count`, `checkpoint_hash`, `signer_set_version`, `signer_bitmap`, `signatures_digest`, `finalized_height`, `finalized_at`, `data_pointer` and the optional `zk_anchor`. The individual signatures are verified and then not stored: the record keeps a bitmap of which signers signed and a digest over the verified signatures.

### 3.3 Message handling: `MsgSubmitCheckpoint`

Message fields: `submitter`, `sidechain_id`, `lc_sequence`, `state_root`, `previous_checkpoint_hash`, `record_count`, `data_pointer`, `signer_set_version`, `signatures` (each a `signer_index` and a `signature`) and the optional `zk_anchor`.

Handler logic, in order. The handler runs in a cached context, so any failure aborts the whole message — no partial state changes:

1. **Bound the message.** Validate the submitter address, load params, and check the per-field limits (`record_count`, `data_pointer` size, `state_root` size, number and total bytes of signatures) and the shape of the optional `zk_anchor`. **One checkpoint, one record set:** if `zk_anchor` is present, `record_count ≤ 2^records_tree_depth` and `state_root` must equal `SHA-256("lucidchain/checkpoint/v1/state-root\0" ‖ scheme(1 byte) ‖ records_root(32) ‖ records_tree_depth(1 byte) ‖ record_count(8 bytes, big-endian))`. The Tier 1 root and the Tier 3 anchor therefore commit to the same records, and a proof verified against the anchor is a proof about the records under the state root (review finding N5). Checkpoints without an anchor keep an operator-defined `state_root`. Test vector: scheme 1, root `0x00…01`, depth 5, count 3 → `62daea805d87aec991f46a0b887bdb9337bd53a92a565604831dda49cc3ec137`; `tools/signcheckpoint -derive-state-root` computes it and `submit-checkpoint-from-batch.sh` applies it.
2. **Resolve sidechain.** Call `sidechainKeeper.GetSidechain(sidechain_id)`. Fail if not found.
3. **Check status and signer set.** Fail unless sidechain status is `ACTIVE`, and unless `signer_set_version` equals the sidechain's current version.
4. **Check tier requirements.** If the sidechain's tier is `PROVEN`, `zk_anchor` must be present; fail with `ErrZkAnchorRequired` otherwise. This runs before signature verification, so a missing anchor costs no verification gas (§3.8).
5. **Check sequencing and chaining.** `lc_sequence` must equal the latest accepted sequence + 1 (or `1` if there is none), so gaps and replays are rejected. `previous_checkpoint_hash` must equal the hash of the checkpoint at `lc_sequence - 1` (or be empty for `lc_sequence == 1`).
6. **Build the sign document and compute the hash** (§3.4).
7. **Verify signatures.** Each signature names a `signer_index`, a position in the sidechain's `signer_keys`. The signature count must meet `signature_threshold`, and every submitted signature must verify over the sign bytes: an invalid, duplicate or out-of-range signature rejects the whole message. `signature_verification_gas` is charged per verification. The result is the signer bitmap and the signatures digest that get stored.
8. **Compute and charge the fee.** `fee = base_fee + per_record_fee * record_count`, taken from the `submitter`'s account through the bank module.
9. **Persist.** Write the `Checkpoint` at `{sidechain_id, lc_sequence}`, update the latest-sequence pointer, and call `sidechainKeeper.RecordCheckpoint(...)` to update the sidechain's `last_checkpoint_*` fields.
10. **Emit event.** A checkpoint-submitted event carrying the sidechain ID, sequence, state root (hex) and checkpoint hash (hex) — this is what off-chain indexers, explorers, and IBC light clients (later) will consume. The response returns `finalized_height` and `checkpoint_hash`.

### 3.4 Signing document and checkpoint hash

```
CheckpointSignDoc = {
    chain_id, sidechain_id, lc_sequence, state_root,
    previous_checkpoint_hash, record_count,
    signer_set_version, data_pointer, zk_anchor (optional)
}

sign_bytes      = SignDocDomain || proto.Marshal(CheckpointSignDoc)
SignDocDomain   = "lucidchain/checkpoint/v1/signdoc" || 0x00
checkpoint_hash = SHA-256(sign_bytes)
```

Every signer signs `sign_bytes`, and the stored `checkpoint_hash` is chained through the next checkpoint's `previous_checkpoint_hash`.

The encoding is consensus-critical, so it is fixed here: standard proto3 marshaling (fields in field-number order, default values omitted). Off-chain signers must produce byte-identical output, so publish test vectors for signer SDKs. The domain prefix means a signature over a checkpoint can never be valid in another context, and `chain_id` stops replay across chains. `signer_set_version` binds each signature to a specific key set. An absent `zk_anchor` encodes as nothing, and an empty non-nil anchor is rejected, so "no anchor" has exactly one encoding.

### 3.5 Merkle inclusion proofs (off-chain)

For a checkpoint **without** a `zk_anchor`, the on-chain `state_root` is a Merkle root over a batch of off-chain records, computed by the sidechain (or its edge aggregators, for high-volume IoT sources). For a checkpoint **with** a `zk_anchor`, `state_root` is the binding hash defined in §3.3 step 1 and inclusion proofs are made against `zk_anchor.records_root` (MiMC-BN254, the same tree the circuits use) instead. The module does not verify individual records — only the root. A relying party who wants to prove a specific record was included:

1. Obtains the original record and its Merkle path from the sidechain operator (or its data store per `data_pointer`).
2. Recomputes the root by hashing up the path.
3. Compares against the on-chain `state_root` for the relevant `{sidechain_id, lc_sequence}` via a standard `Checkpoint` query.

This proof mechanism is entirely off-chain and language/library-agnostic — the module only needs to guarantee that `state_root` is correct and immutable once finalized.

### 3.6 Invariants

- `lc_sequence` values for a given `sidechain_id` are strictly increasing with no gaps, starting at 1.
- Each stored `Checkpoint.previous_checkpoint_hash` matches the actual hash of the prior checkpoint (self-consistency of the chain can be verified by replaying all checkpoints for a sidechain).
- A `Checkpoint` is immutable once written — no message in this module updates or deletes an existing checkpoint.
- Genesis validation enforces the same shape: for every sidechain the checkpoints form a gapless chain `1..n` with matching hash links, every `checkpoint_hash` is 32 bytes, the first checkpoint has an empty `previous_checkpoint_hash`, and every `zk_anchor` that is present is valid.

### 3.7 Fee design notes

The fee is `base_fee + per_record_fee * record_count`, both in the same denom, and both default to zero. Batching is what keeps this workable for high-volume sources (e.g. IoT). The `per_record_fee` deliberately doesn't scale linearly with true per-event cost — it's a fraction of it, since the marginal cost to the chain is closely tied to `O(1)` per checkpoint (one state write, one signature check set) regardless of how many off-chain records the root represents. Governance should tune `max_records_per_checkpoint` alongside block gas/size limits so a single checkpoint tx can't be used to smuggle disproportionate off-chain claims relative to what's actually been verified (the batch root, not its contents).

### 3.8 Tier-dependent requirements

- **`PROVEN` sidechains must anchor every checkpoint.** A checkpoint without a `zk_anchor` is rejected with `ErrZkAnchorRequired`, and nothing is stored or charged.
- **Lower tiers are unchanged.** `NOTARIZED` and `ATTESTED` sidechains may include an anchor but are not required to. Whenever an anchor is present it is validated (supported hash scheme, a 32-byte canonical field-element `records_root`, `records_tree_depth` within range) and bound to the checkpoint: `state_root` must be derived from it and `record_count` must fit the tree (§3.3 step 1). So "PROVEN" means the state root is the one the proof covers, not merely that an anchor is attached.
- **Forward-only.** The tier is read live from the sidechain record through the `SidechainKeeper`, not copied. After a sidechain is upgraded to `PROVEN`, the next checkpoint needs an anchor; checkpoints accepted earlier are unchanged and stay valid.
- **Anchors are signed.** `zk_anchor` is part of the checkpoint sign document, so it is bound by the signatures and by `checkpoint_hash`. Signers must add it before the sidechain upgrades to `PROVEN`.
- **The anchor is what proofs bind to.** `x/proofs` can bind a proof's public inputs to the anchor's `records_root` (§5.4), so a `PROVEN` sidechain's anchored checkpoints are the ones that can carry such a proof.
- Checkpoints do not currently record the tier in force when they were accepted.

### 3.9 Params

| Param | Default | Meaning |
|---|---|---|
| `base_fee`, `per_record_fee` | 0 in the bond denom | Fee components (§3.7); must share a denom |
| `max_records_per_checkpoint` | 1,000,000 | Upper bound on `record_count` |
| `max_data_pointer_bytes` | 256 | Upper bound on `data_pointer` size |
| `max_signatures_per_checkpoint` | 7 | Upper bound on the number of signatures |
| `max_signature_bytes_per_checkpoint` | 24,000 | Upper bound on total signature bytes (7 × 3,309-byte ML-DSA-65 signatures) |
| `signature_verification_gas` | 50,000 | Gas charged per signature verification. A placeholder: benchmark ML-DSA-65 verification on validator hardware and set it from the result |

`state_root` is separately bounded at 64 bytes (a Merkle root is normally 32; the headroom allows other commitment schemes).

## 4. Module: `x/attestor`

### 4.1 Purpose

Implements Tier 2. Separate, staked attestors — certification bodies, auditors, regulators — publish signed claims about a specific `{sidechain_id, checkpoint_sequence}`, under a governance-approved schema that fixes exactly what the claim does and does not assert. Because falsity of a real-world claim is usually not objectively checkable on-chain, the module includes a bonded dispute process rather than automatic slashing.

### 4.2 State layout

| Collection | Key → value | Description |
|---|---|---|
| `Attestors` | `id` → `Attestor` | Primary attestor record |
| `Schemas` | `schema_id` → `AttestationSchema` | Governance-published schema |
| `Attestations` | `id` → `Attestation` | Primary attestation record; the id is derived deterministically from `(schema_id, sidechain_id, lc_sequence, attestor_id)` |
| `AttestationsByCheckpoint` | `((sidechain_id, lc_sequence), attestation_id)` | Secondary index so a checkpoint's attestations can be listed and paginated |
| `Disputes` | `id` → `Dispute` | Primary dispute record; the id is derived from the attestation id, so an attestation has at most one dispute |
| `ExitQueue` | `(bond_return_at as unix seconds, attestor_id)` | `EXITED` attestors awaiting their bond return |
| `Params` | — → `Params` | Module parameters |

There is no `byDomain` index. The by-checkpoint index and the exit queue are derived data, rebuilt on genesis import.

**`Attestor` record:** `id`, `name`, `operator`, `authorized_schema_ids`, `signer_keys`, `signature_threshold`, `signer_set_version`, `status` (`PENDING`, `ACTIVE`, `SUSPENDED`, `SLASHED`, `EXITED`), `bond_amount`, `credential_uri`, `reputation_score` (informational), `open_disputes`, `registered_at`, `exit_requested_at` and `bond_return_at`.

**`Params`:** `bond_denom` (cannot be changed after genesis, because bonds are stored as bare amounts), `min_attestor_bond`, `min_dispute_bond`, `slash_fraction`, `challenger_reward_fraction`, `dispute_resolver_authority` (optional; the module authority when unset), `max_claim_payload_bytes`, `max_uri_bytes`, `signature_verification_gas`, `exit_cooldown_seconds`, and the signer-set limits.

### 4.3 Messages

| Message | Signer | Effect |
|---|---|---|
| `MsgRegisterAttestor` | operator | Validates id, name, credential URI, authorized schemas (each must exist and be active), signer set, and a bond of at least `min_attestor_bond`. Escrows the bond and creates the `Attestor` in `PENDING`. Governance must call `MsgActivateAttestor` before it can attest |
| `MsgActivateAttestor` | authority | `PENDING` → `ACTIVE` |
| `MsgSuspendAttestor` | authority | `ACTIVE` → `SUSPENDED`, with a reason of at most 256 bytes; the governance lever for a dispute that should not wait for a ruling. `RaiseDispute` never suspends on its own |
| `MsgReinstateAttestor` | authority | `SUSPENDED` → `ACTIVE`. Open disputes do not block it |
| `MsgUpdateAttestorSigners` | operator | Rotates signer keys and threshold and increments `signer_set_version`; allowed while `PENDING`, `ACTIVE` or `SUSPENDED`. Existing attestations keep the version they were signed under |
| `MsgInitiateAttestorExit` | operator | Allowed from `PENDING`, `ACTIVE`, `SUSPENDED` or `SLASHED`. Sets `EXITED`; the attestor can no longer attest. The bond is returned by `ProcessExits` after a cooldown of the larger of `exit_cooldown_seconds` and the longest dispute window among its authorized schemas, and only once no dispute against it is open |
| `MsgSubmitAttestation` | attestor's operator | Validates schema authorization, that the sidechain's tier is `ATTESTED` or higher, checkpoint existence, and signatures; writes an `Attestation` in `ACTIVE` status (§4.4) |
| `MsgRevokeAttestation` | attestor's operator | Withdraws one of the attestor's own `ACTIVE` attestations. A disputed attestation cannot be revoked, so an attestor cannot dodge a pending dispute |
| `MsgRaiseDispute` | challenger | Any account except the attestor's own operator can challenge an `ACTIVE` attestation within its schema's `dispute_window_seconds`, posting a bond; moves the attestation to `DISPUTED` |
| `MsgResolveDispute` | resolver | Called by `dispute_resolver_authority` (the gov module unless configured otherwise); upholds or dismisses the dispute (see §4.6) |
| `MsgCreateAttestationSchema` / `MsgRetireAttestationSchema` | authority | Governance-only; schemas are immutable once created (retire, don't edit, to preserve the historical meaning of past attestations) |
| `MsgUpdateParams` | authority | Governance-gated parameter update; `bond_denom` cannot change |

### 4.4 `MsgSubmitAttestation` handler logic

Any failure aborts the whole message. In order:

1. **Resolve the schema.** It must exist and be `active`.
2. **Resolve the attestor.** It must exist, the submitter must be its operator, it must be `ACTIVE`, the `schema_id` must be in its `authorized_schema_ids`, and `signer_set_version` must match the attestor's current version.
3. **Check the request.** `sidechain_id` is required and `checkpoint_sequence` must be at least 1. `claim_payload` must be non-empty, within `max_claim_payload_bytes`, and valid JSON.
4. **Resolve the sidechain and check its tier.** Call `sidechainKeeper.GetSidechain(sidechain_id)`; fail if not found. The sidechain's tier must be `ATTESTED` or `PROVEN`; otherwise fail with `ErrSidechainTierTooLow`. Attestations only apply to sidechains that registered for attestor sign-off. This runs before the conflict-of-interest check and before any signature verification, so a rejected submission costs no verification gas.
5. **Check conflict of interest** against the sidechain (§4.6).
6. **Resolve the checkpoint.** Call into `x/checkpoint`'s keeper to confirm `{sidechain_id, checkpoint_sequence}` exists. Fail if not found — an attestation can never reference a nonexistent or future checkpoint. Its `checkpoint_hash` is bound into the signed document.
7. **Check uniqueness.** The same `(schema_id, sidechain_id, lc_sequence, attestor_id)` tuple can only ever be attested once, even after the attestation was revoked or overturned.
8. **Verify signatures.** Build the sign document (`chain_id`, `schema_id`, `attestor_id`, `sidechain_id`, `checkpoint_sequence`, `checkpoint_hash`, `claim_payload`, `signer_set_version`) and verify the signatures against the attestor's registered keys and threshold. The count and threshold checks run before any expensive verification, and any invalid, duplicate or out-of-range signature rejects the whole message. `signature_verification_gas` is charged per verification. The signer bitmap (bit *i* in byte *i/8*, least significant bit first) and the signatures digest (SHA-256 over the verified signatures ordered by `signer_index`) are stored.
9. **Persist** the `Attestation` as `ACTIVE`, with `expires_at = issued_at + schema.validity_period_seconds`, write its by-checkpoint index entry, and increase the attestor's reputation score by 1.
10. **Emit event.** `attestation_submitted` with the attestation ID, schema, attestor, sidechain, checkpoint sequence and checkpoint hash.

### 4.5 Keeper surface (consumed by queries and other modules)

```go
func (k Keeper) GetAttestor(ctx context.Context, id string) (types.Attestor, error)       // ErrAttestorNotFound
func (k Keeper) GetSchema(ctx context.Context, id string) (types.AttestationSchema, error) // ErrSchemaNotFound
func (k Keeper) GetAttestation(ctx context.Context, id string) (types.Attestation, error)  // ErrAttestationNotFound
func (k Keeper) GetDispute(ctx context.Context, id string) (types.Dispute, error)          // ErrDisputeNotFound
func (k Keeper) GetParams(ctx context.Context) (types.Params, error)
func (k Keeper) GetDisputeResolver(ctx context.Context) ([]byte, error)
func (k Keeper) ProcessExits(ctx context.Context) error // EndBlock
```

Attestations about a checkpoint are listed through the `AttestationsByCheckpoint` index. There is no separate `SlashBond` entry point: slashing happens inside `MsgResolveDispute`.

### 4.6 Design notes specific to this module

- **Schema immutability is deliberate.** Once published, a schema's `claim_description` and `exclusions` are fixed. This means a relying party reading an old attestation always knows exactly what was and wasn't claimed at the time, even if the schema is later retired in favor of a v2.
- **Dispute flow.**
  - *Raising.* The challenger posts a bond of at least `min_dispute_bond` and an `evidence_uri`. The attestation must be `ACTIVE` and inside its dispute window (`issued_at + schema.dispute_window_seconds`). An attestation can be disputed at most once. The attestation becomes `DISPUTED` and the attestor's `open_disputes` counter rises, which blocks its bond return. The attestor is not suspended automatically.
  - *Upheld.* The attestor's bond is cut by `slash_fraction` of its current bond. The challenger gets its dispute bond back plus `challenger_reward_fraction` of the slashed amount, and the rest of the slashed amount is burned. The attestor's reputation drops by 2 and it becomes `SLASHED` (an `EXITED` attestor stays `EXITED` and just loses the slashed part of its bond). The attestation becomes `OVERTURNED`.
  - *Not upheld.* The challenger's dispute bond is burned, to discourage spurious disputes. The attestation becomes `UPHELD` and the attestor's status is unchanged; only `MsgReinstateAttestor` lifts a suspension.
- **Dispute resolution starts centralized (gov module, or a configured `dispute_resolver_authority`) by design**, not as an oversight. A dedicated decentralized arbitration mechanism is a larger design problem (juror selection, incentive compatibility, evidence handling) that deserves its own spec once there's real dispute volume to learn from — bolting on a placeholder mechanism now would likely need to be redesigned anyway.
- **Reputation score is explicitly informational.** It rises by 1 per submitted attestation and falls by 2 on an upheld dispute. It must never gate consensus-critical logic (e.g. signature verification), only off-chain UX like sorting attestors in an explorer. This avoids a whole category of gaming/manipulation attacks on a supposedly "trust-scored" on-chain number.
- **Conflict-of-interest enforcement** runs when an attestation is submitted (`MsgRegisterAttestor` cannot check it, because an attestor is not tied to a sidechain until it attests) and again at genesis import. Against the target sidechain, the attestor's operator, the submitter and every attestor signer-key address must not match the sidechain's operator or any of its checkpoint signer-key addresses. The check fails closed: a sidechain whose signer keys are not unpacked rejects the submission. Genesis compares only the two operator addresses, because signer keys are mutable and public — if genesis compared them, a sidechain operator could rotate an honest attestor's public key into its own signer set and make every later export unimportable.
- **Tier gate.** `x/attestor` reads the sidechain's tier through the sidechain keeper and never stores its own copy. `PROVEN` sidechains are accepted as well as `ATTESTED` ones, because the tiers are cumulative. The check applies only to new submissions: tiers never go down, so an existing attestation never ends up on a sidechain below `ATTESTED`, and revocation and disputes do not depend on the tier. The handler does not currently require the sidechain to be `ACTIVE`; whether it should is an open decision.
- **Bond custody.** The module account holds every attestor bond and every open dispute bond. Bonds are returned by `ProcessExits` for each `EXITED` attestor after its cooldown, once `open_disputes` is zero. Each exit is processed in its own cached context, and a failure is logged and skipped, so a bad entry can never halt the chain.

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

Implements Tier 3. Unlike `x/attestor`, this module doesn't rely on anyone's honesty — it cryptographically verifies a validity proof on-chain against a governance-registered verifier, synchronously, in the message handler. A proof is either checked and correct, or the transaction fails; there is no dispute window because there is nothing to dispute. Verifiers are registered per `(proof_system_id, claim_id)`, so each narrow claim has its own verification key and public-input layout.

### 5.2 State layout

| Collection | Key → value | Description |
|---|---|---|
| `Verifiers` | `(proof_system_id, claim_id)` → `VerifierRegistration` | Registered verifier for one claim under one proof system |
| `Proofs` | `id` → `ProofRecord` | Persisted, verified proof. The id is `hex(SHA-256(domain ‖ lp(sidechain_id) ‖ be64(checkpoint_sequence) ‖ lp(proof_system_id) ‖ lp(claim_id)))`, so a given (checkpoint, system, claim) can be proven only once |
| `ProofsByCheckpoint` | `((sidechain_id, lc_sequence), proof_id)` → `proof_id` | Secondary index so a checkpoint's proofs can be listed and paginated |
| `Params` | — → `Params` | Module parameters |

Proto definitions: `proto/lucidchain/proofs/v1/*.proto`.

**`VerifierRegistration`:** `proof_system_id`, `claim_id`, `description`, `verification_key` (opaque to the module), `status` (`ACTIVE` or `DEPRECATED`), `version` (starts at 1; incremented when the key or the input layout changes), `num_public_inputs` (derived from the key), `input_layout`, `verification_gas`, `registered_at` and `updated_at`.

**`ProofRecord`:** `id`, `sidechain_id`, `checkpoint_sequence`, `proof_system_id`, `verifier_version`, `claim_id`, `public_inputs`, `proof`, `verified` (always true: failed proofs are rejected, not stored), `verified_height`, `verified_at`, `checkpoint_hash`, `binding` and `submitter`. `ProofSummary` is the same record without the proof and public-input bytes, for listings.

**`Params`:** `base_verification_fee` and `fee_denom`, `max_proof_bytes`, `max_public_input_bytes` and `max_verification_key_bytes`.

### 5.3 Verifier implementations are Go code, not on-chain data

**Current verifiers.** The verifiers are implemented with [gnark](https://github.com/Consensys/gnark), over the BN254 curve (for example Groth16 and PLONK). gnark is not quantum-resistant; see §5.7 for what that means and for the planned move to Rust code in a microVM.

`VerifierRegistration.verification_key` is opaque on-chain state — the actual cryptographic verification (pairing checks, FRI, whatever the proof system requires) is implemented as compiled Go code, handed to the keeper at construction as a map keyed by `proof_system_id`. The keeper itself has no dependency on a proof library; it only uses this interface:

```go
type ProofVerifier interface {
    ValidateKey(verificationKey []byte) error             // rejects malformed keys at registration
    PublicInputCount(verificationKey []byte) (int, error) // statement width, derived from the key
    BindingWidth() int                                    // public inputs reserved for the binding (2)
    CheckBinding(publicInputs []byte, binding types.Binding) error
    CheckInputs(publicInputs []byte, expected []types.ExpectedInput) error
    Verify(verificationKey, publicInputs, proof []byte) error
}

// app/app.go: the implementations compiled into the binary are supplied to the
// dependency-injection container, which hands them to the proofs keeper.
depinject.Supply(
    // proof verifier implementations compiled into the binary
    proofsverifiers.Default(),
)
```

`proofsverifiers.Default()` (package `x/proofs/verifiers`) returns the map from `proof_system_id` to implementation; the keeper clones it at construction, so the set of available proof systems is fixed for the lifetime of the binary.

Governance can only register a verifier for a `proof_system_id` that already has a corresponding Go implementation compiled into the running binary (`HasImplementation`) — governance cannot make the chain accept a genuinely new proof system without a coordinated software upgrade. This is a deliberate safety property: arbitrary verification logic is never accepted as untrusted on-chain data. The scalar field needed to validate fixed input values is known only for `-bn254` systems today; another curve must be added to the keeper's field lookup before verifiers for it can be registered.

### 5.4 Messages and handler logic

| Message | Signer | Effect |
|---|---|---|
| `MsgSubmitProof` | submitter | Verifies a proof about an existing checkpoint and, if valid, stores a `ProofRecord`; an invalid proof fails the transaction and nothing is stored |
| `MsgRegisterVerifier` | authority | Registers a verifier for `(proof_system_id, claim_id)`, or rotates the key or layout of an `ACTIVE` one |
| `MsgDeprecateVerifier` | authority | `ACTIVE` → `DEPRECATED`: no new proofs are accepted, existing records stay queryable |
| `MsgUpdateParams` | authority | Governance-gated parameter update |

**The binding.** A proof must be tied to exactly one checkpoint and claim on this chain. The module computes a 32-byte binding as a hash over the chain id, sidechain id, checkpoint `lc_sequence`, state root, checkpoint hash and `claim_id`. The first two public inputs of the proof must be the 128-bit big-endian halves (high, low) of that binding, so circuits must declare these two public variables first. `public_inputs` uses the verifier's own encoding; for the gnark verifiers it is a gnark public witness (`MarshalBinary`).

**The input layout.** Every other public input can be bound too. A registration's `input_layout` lists, by index, which inputs must equal the checkpoint's `zk_anchor.records_root`, the checkpoint's `record_count`, or a fixed value set at registration. Layout indexes must lie after the binding inputs, with no duplicates; a records-root entry needs a `-bn254` proof system, and a fixed value must be 1 to 32 bytes and below the field modulus.

`MsgSubmitProof` handler logic, cheap checks first so bad input costs little. Any failure aborts the whole message (the handler runs in a cached context):

1. **Bound the message.** Validate the submitter address and load params. `sidechain_id`, `claim_id` and `proof_system_id` must be valid, `checkpoint_sequence` at least 1, and `public_inputs` and `proof` non-empty and within `max_public_input_bytes` and `max_proof_bytes`.
2. **Resolve the verifier** for `(proof_system_id, claim_id)`. It must be `ACTIVE` (otherwise `ErrVerifierDeprecated`) and must have a compiled-in implementation (otherwise `ErrVerifierUnavailable`).
3. **Check uniqueness.** The proof id must be unused: a given (checkpoint, system, claim) can be proven only once.
4. **Resolve the checkpoint** through `x/checkpoint`'s keeper and derive its binding. Fail if the checkpoint does not exist — a proof can never reference a nonexistent or future checkpoint.
5. **Check the binding.** The first public inputs must carry the binding (`ErrBindingMismatch`).
6. **Check the input layout.** The remaining bound inputs must equal their expected values (`ErrInputMismatch`). An entry sourced from the records root fails if the checkpoint has no valid MiMC-BN254 anchor.
7. **Charge.** Collect `base_verification_fee` (in `fee_denom`, zero means free) from the submitter into the fee collector, and consume the registration's `verification_gas`. The gas is spent whether or not the proof turns out to be valid, which discourages spamming invalid proofs; the fee transfer is reverted with the rest of a failed transaction.
8. **Verify.** On failure the transaction aborts with `ErrInvalidProof` and nothing is written.
9. **Persist** the `ProofRecord` with the verifier version it was checked under, plus its by-checkpoint index entry, and emit `proof_verified` (proof id, sidechain, checkpoint sequence, proof system, claim id, verifier version, binding).

**Verifier faults.** Every verifier call is wrapped. A panic inside a verifier becomes an ordinary, deterministic rejection, so a malformed proof can never crash block execution. An error wrapping `ErrVerifierFault` is an infrastructure failure, not a verdict, and is never reported as an invalid proof: while executing a block it escalates to a typed panic that the app's recovery handler (`FaultRecoveryHandler` with `HardHalt`, registered in `app/app.go`) turns into a node halt, and in simulation or check mode it is returned as-is.

`MsgRegisterVerifier` (authority only): `proof_system_id`, `claim_id` and a description are required; `verification_gas` must be positive; the key must be non-empty, within `max_verification_key_bytes`, and accepted by the implementation's `ValidateKey`. The implementation derives the number of public inputs from the key, and the layout is validated against that number, never against a count the submitter claims. A new verifier starts `ACTIVE` at version 1. Re-registering an `ACTIVE` one with a different key or layout increments the version; the same key and layout only updates the description and gas. A `DEPRECATED` verifier cannot be re-registered: register a new `claim_id` instead. Existing proof records keep the version they were verified under.

### 5.5 Keeper surface and queries

```go
func (k Keeper) GetVerifier(ctx context.Context, proofSystemID, claimID string) (types.VerifierRegistration, error) // ErrVerifierNotFound
func (k Keeper) GetProof(ctx context.Context, id string) (types.ProofRecord, error)                                  // ErrProofNotFound
func (k Keeper) GetParams(ctx context.Context) (types.Params, error)
func (k Keeper) HasImplementation(proofSystemID string) bool
```

A checkpoint's proofs are listed through the `ProofsByCheckpoint` index.

**Queries**

| Query | Description |
|---|---|
| `Verifier` | One registration, by `proof_system_id` and `claim_id` |
| `Verifiers` | Paginated list of registrations |
| `Proofs` | Paginated proof summaries, ordered by proof id. The page size is capped at 50, because each item is loaded in full (proof bytes included) even though only a summary is returned |
| `ProofRecord` | One full proof record, by id |
| `ProofsForCheckpoint` | The full proof records of one checkpoint, ordered by proof id, read through the by-checkpoint index |
| `Binding` | For a `(sidechain_id, checkpoint_sequence, claim_id)`: the 32-byte binding a prover must put in the first two public inputs, given both as bytes and as its `hi` and `lo` limbs, together with the chain id, state root and checkpoint hash it is computed from. Fails with not-found if the checkpoint does not exist |

A prover's workflow is therefore: read the checkpoint, call `Binding` for the claim, build a proof whose first two public inputs are `hi` and `lo`, and submit it with `MsgSubmitProof`.

### 5.6 Design notes specific to this module

- **Verification cost varies enormously by proof system** (a Groth16 pairing check is cheap; a large STARK can be expensive). The gas is therefore set per registration (`verification_gas`), so each system and claim carries its own cost; benchmark on validator hardware before setting it. `base_verification_fee` is a flat floor on top, still a placeholder.
- **This module never blocks on Tier 1, and does not read the sidechain.** A checkpoint is finalized by `x/checkpoint` regardless of whether any proof is ever submitted for it. `x/proofs` depends only on `x/checkpoint` and the bank module, so it does not check the sidechain's tier or status: the Tier 3 requirement is enforced upstream, by `x/checkpoint` requiring an anchor from `PROVEN` sidechains (§3.8). Proofs (and attestations) are additive evidence layered on top of an already-finalized anchor, which keeps the core anchoring path simple and keeps Tier 2/3 entirely optional per sidechain.
- **`claim_id` scoping mirrors `x/attestor`'s `schema_id`**, and here it is part of the registration key, so a checkpoint can accumulate multiple narrow, named claims over time (from attestors, from proofs, or both) rather than one all-or-nothing verdict.
- **Proofs are replay-proof by construction.** The binding commits to the chain id, the checkpoint and the claim, so a valid proof cannot be reused for another checkpoint, claim or chain.

### 5.7 Quantum resistance and the planned verifier architecture

**Today: gnark over BN254, not quantum-resistant.** Groth16 and PLONK over BN254 rest on elliptic-curve pairing assumptions, which a sufficiently large quantum computer could break. The consequence is for soundness: after such a break, a forged proof could pass verification, so Tier 3 evidence would be only as strong as classical assumptions. This is separate from the rest of the chain, where checkpoint signatures can already be ML-DSA-65 only (`allowed_pubkey_type_urls`, §2.2). Proofs accepted before a quantum break remain timestamped on-chain records, but proofs submitted after one could not be trusted.

The `zk_anchor` is coupled to the same curve: its only supported scheme today is `ZK_HASH_SCHEME_MIMC_BN254`, with a `records_root` that must be a canonical BN254 scalar-field element. The `scheme` field is the extension point for a different commitment scheme when the proof system changes.

**Planned: Rust code in a microVM over vsock.** The intended direction is to move to Rust code running in a microVM, which the node reaches over vsock. This draft does not specify it yet, including which parts of the pipeline would move: verification inside the consensus path, or only proving and aggregation outside it. If verification moves, the design must settle these consensus-critical points before it replaces the gnark verifiers:

- **Determinism.** Every validator must reach the same accept or reject result for the same inputs, on every platform and version.
- **Gas and limits.** Cost must be metered and bounded, with a hard timeout, so a proof cannot stall block processing.
- **Failure behaviour.** The current code already separates a verdict from an infrastructure fault (§5.4): a fault is never reported as an invalid proof, and during block execution it halts the affected node rather than letting it diverge. A microVM that is slow, crashed or unreachable would be such a fault, so timeouts and recovery must be specified. It must also be possible to tell "the verifier crashed on this malformed input" (a deterministic rejection, the same on every node) from "the microVM is unavailable on this node" (a fault). If a crafted proof could crash the microVM on every validator and be classified as a fault, a single transaction could halt the whole chain.
- **Verifier identity and upgrades.** The exact verifier build (for example a pinned image hash) must be fixed by a governance or software-upgrade path so all validators run the identical version. This replaces the §5.3 property that verification code is compiled into the node binary.
- **Interface.** The encoding of `verification_key`, `public_inputs` and `proof` across vsock, and the isolation between the node and the microVM, must be specified. The seam is already narrow: the keeper depends only on the `ProofVerifier` interface (§5.3), so a microVM-backed implementation would implement that interface without changes to the keeper.

## 6. Cross-module interaction diagram

```
                    Sidechain operator's
                    checkpoint submitter
                            │
                            │  MsgSubmitCheckpoint
                            ▼
                  ┌──────────────────────┐        GetSidechain() etc.     ┌─────────────────────┐
                  │   x/checkpoint       │ ──────────────────────────────►│   x/sidechain       │
                  │  - validates         │◄────────────────────────────── │  - registry, bonds  │
                  │  - sequences         │                                │  - status/tier      │
                  │  - stores commitment │                                └─────────────────────┘
                  └──────────────────────┘
                        ▲            ▲
      GetCheckpoint()   │            │   GetCheckpoint()
                        │            │
        ┌───────────────┘            └────────────────┐
        │                                             │
┌──────────────────┐                            ┌───────────────────┐
│  x/attestor      │                            │  x/proofs         │
│  - registry      │                            │  - verifier       │
│  - schemas       │                            │    registry       │
│  - attestations  │                            │  - proof records  │
│  - disputes      │                            │    (sync-verified)│
└──────────────────┘                            └───────────────────┘
        │                                              │
        │ attestation_submitted                        │ proof_verified
        ▼                                              ▼
              Indexers / explorers / relying parties
              (a relying party queries x/checkpoint for the anchor,
               then x/attestor and/or x/proofs for whatever additional
               assurance the sidechain's declared tier provides)
```

`x/attestor` also reads `x/sidechain` directly, for the tier check and the conflict-of-interest check (§4.4). `x/checkpoint` reads `x/sidechain` for registration, status, signer set and tier, and writes back the last-checkpoint fields through `RecordCheckpoint`.

## 7. Testing plan

- **Unit tests** per keeper method (registration edge cases, sequencing gaps, signature threshold logic, bond math).
- **Tier tests** covering `MsgUpgradeSidechain` (bond math including the current-bond rule, each status, upward-only, authorization, bad denoms, and that nothing is escrowed on failure), the `PROVEN` anchor requirement in `x/checkpoint` (every tier, rejection before signature verification, mid-stream upgrade, malformed anchors), and the `ATTESTED`-or-higher requirement in `x/attestor`.
- **Simulation tests** using the SDK's simulation framework to fuzz message sequences and check invariants hold under random operation.
- **Integration tests** on a local multi-validator testnet: register a sidechain, submit a realistic batch of checkpoints, confirm finality timing and fee deduction.
- **Load tests** specifically for the checkpoint path at high frequency/large `record_count`, since this is the module most exposed to smart-city-scale volume later.

## 8. Explicit non-goals for this draft

- No validation of off-chain record contents by any module — `x/checkpoint` stores a root, `x/attestor` stores a claim about a checkpoint, `x/proofs` cryptographically verifies a claim about a checkpoint; none of them inspect raw sidechain data.
- No IBC light client wiring yet — the checkpoint-submitted event (and the attestor/proof events) are designed to make that addition straightforward later.
- No decentralized dispute arbitration — `x/attestor` disputes resolve through the gov module (or a configured resolver address) as an interim authority; a dedicated arbitration mechanism is future work (§4.6).
- No tier downgrades. Releasing bond immediately would bypass the exit cooldown; if added, released bond should go through a delayed queue like the exit queue.
- No attestor-availability check at upgrade time. The original idea (at least one available attestor for the sidechain's declared domain before moving to `ATTESTED`) needs a `domain` field on `Sidechain` and a dependency from `x/sidechain` on `x/attestor`, which would create a dependency cycle. For now `ATTESTED` is enforced where it is used, in `x/attestor`.
- No per-checkpoint attestation or proof requirement beyond the anchor. An `ATTESTED` sidechain's checkpoints are accepted whether or not any attestor has signed them.
- No retroactive tier re-check. A governance increase of `min_bond_by_tier`, or a slash, does not downgrade a sidechain or force a top-up.
- Tier 3 is not quantum-resistant yet. The current gnark verifiers over BN254 rely on classical assumptions (§5.7). A migration is planned to Rust code in a microVM reached over vsock, but that architecture is not specified in this draft.
- No other proof systems beyond the current gnark-based verifiers are specified; adding one still means a coordinated software upgrade (§5.3), and the likely target use cases (geofence compliance, AML thresholds, etc.) will drive which are needed.
- No slashing conditions beyond bond math are finalized for `x/sidechain` — the exact fault conditions for `MsgSlashSidechain` (e.g. two conflicting checkpoints at the same sequence, signed by the registered keys) need a dedicated fraud-detection spec before mainnet.
- Verification gas is set per registration in `x/proofs`, but the values and the flat `base_verification_fee` are placeholders pending real gas benchmarks on validator hardware.
- No HSM/hardware-backed signing solution is chosen for attestor keys — see §4.7. The on-chain protocol doesn't need this decision made now, but it should be resolved operationally before onboarding a real (non-test) attestor.

---


# lucidchain
**lucidchain** is a blockchain built using Cosmos SDK and Tendermint and created with [Ignite CLI](https://ignite.com/cli).

## Get started

```
ignite cosmos chain serve
```

`serve` command installs dependencies, builds, initializes, and starts your blockchain in development.

### Configure

Your blockchain in development can be configured with `config.yml`. To learn more, see the [Ignite CLI docs](https://docs.ignite.com).

### Web Frontend

Additionally, Ignite CLI offers a frontend scaffolding feature (based on Vue) to help you quickly build a web frontend for your blockchain:

Use: `ignite cosmos scaffold vue`
This command can be run within your scaffolded blockchain project.


For more information see the [monorepo for Ignite front-end development](https://github.com/ignite/web).

## Release
To release a new version of your blockchain, create and push a new tag with `v` prefix. A new draft release with the configured targets will be created.

```
git tag v0.1
git push origin v0.1
```

After a draft release is created, make your final changes from the release page and publish it.

### Install
To install the latest version of your blockchain node's binary, execute the following command on your machine:

```
curl https://get.ignite.com/lucidpay/lucidchain@latest! | sudo bash
```
`lucidpay/lucidchain` should match the `username` and `repo_name` of the Github repository to which the source code was pushed. Learn more about [the install process](https://github.com/ignite/installer).

## Learn more

- [Ignite CLI](https://ignite.com/cli)
- [Tutorials](https://docs.ignite.com/guide)
- [Ignite CLI docs](https://docs.ignite.com)
- [Cosmos SDK docs](https://docs.cosmos.network)