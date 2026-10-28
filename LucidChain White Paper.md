# LucidChain: A Public Assurance Layer for Private Chains

Technical White Paper · Draft v0.4 · October 2026

Based on the LucidChain technical specification, Draft v0.4

## Abstract

Organizations increasingly run private ledgers because they need to keep data and execution confidential. Private ledgers have a structural weakness, though: whoever controls them can rewrite history, and outsiders have no independent way to check what the ledger said at a given moment. LucidChain addresses this gap. It is a public, proof-of-stake blockchain, with quantum-resistant signatures, that acts as a neutral assurance layer for private sidechains.

Sidechains keep their data and execution private. They periodically publish compact cryptographic commitments, called checkpoints, to LucidChain. In return they receive a tamper-evident, independently timestamped record and, at higher assurance tiers, signed attestations from qualified third parties or cryptographic validity proofs. LucidChain is deliberately minimal. It has no general-purpose smart contract virtual machine, only four purpose-built native modules. It runs on two tokens: a volatile staking token that secures the chain and backs bonds, and a stable utility token that pays for services.

This paper describes the problem, the architecture, the three assurance tiers, the security and incentive model, the current state of quantum resistance, and the known limitations of the design.

## 1. Introduction

Private and permissioned ledgers are common in hospitality, finance, identity, logistics and municipal infrastructure. Their operators keep sensitive data off public networks for good reasons: confidentiality, regulation, performance and cost. The same privacy makes their records hard to trust from outside. A customer, auditor or regulator who is shown a record from a private ledger has to take the operator's word that the record existed at the claimed time and has not been altered.

LucidChain does not try to move private data onto a public chain. It separates two questions that are often conflated:

- **What does the data say?** This stays private, on the sidechain.
- **Can anyone prove the data has not been changed since a given time?** This is answered publicly, by LucidChain.

The design principle is that a public chain should store as little as possible and prove as much as possible. A single 32-byte root can commit to a million off-chain records, and the chain can finalize that root, order it, and make it immutable.

## 2. The Problem

**Mutable history.** An operator of a private ledger can restate past records. Without an external anchor, nobody can show that they did.

**No independent time.** A private ledger's timestamps are the operator's own. A relying party cannot tell whether a record was created when it claims, or backdated.

**Unverifiable claims.** Even when records are authentic, higher-level claims about them ("revenue matches the source data", "this subject passed KYC", "this device stayed inside the permitted area") are statements that someone must check. Today those checks live in PDFs and emails, not in a form that machines and counterparties can verify.

**Heavy general-purpose chains.** Putting this logic into smart contracts on a general-purpose chain brings a large attack surface, unpredictable costs and an execution environment that most of these use cases do not need.

**A looming cryptographic deadline.** Records that must stay trustworthy for years, such as audit trails, identity and compliance evidence, will outlive the classical signature schemes that protect most chains today. A sufficiently capable quantum computer could forge those signatures.

## 3. Design Overview

### 3.1 Principles

1. **Anchor first.** The core function is a finalized, append-only record of checkpoints. Everything else is optional and additive.
2. **Minimal on-chain surface.** Four native modules, no smart contract VM. Each module does one job and exposes a narrow interface to the others.
3. **Additive assurance.** Attestations and proofs never replace the anchor. They are evidence layered on top of an already-finalized checkpoint.
4. **Skin in the game.** Sidechains and attestors post bonds. Misbehavior costs money.
5. **Honest about limits.** Where a guarantee depends on classical assumptions, centralized interim authority or unfinished work, the specification says so. This paper does the same (Section 11).

### 3.2 Technology base

LucidChain is built with the Cosmos SDK and CometBFT (Tendermint) consensus, scaffolded with Ignite CLI. All four modules are standard Cosmos SDK modules with Protobuf-defined types, keeper-based state and standard message routing. The specification targets Cosmos SDK v0.55 and later.

### 3.3 Architecture

| Module | Role | Depends on |
| --- | --- | --- |
| `x/sidechain` | Registry of sidechains: identity, signer keys, bonds, status, assurance tier | none |
| `x/checkpoint` | Accepts, verifies and finalizes signed checkpoints into an append-only chain per sidechain | `x/sidechain` |
| `x/attestor` | Registers staked attestors and claim schemas, accepts signed attestations about checkpoints, handles disputes | `x/checkpoint`, `x/sidechain` |
| `x/proofs` | Registers validity-proof verifiers and verifies proofs about checkpoints on-chain | `x/checkpoint` |

The dependency structure is strict. `x/sidechain` depends on nothing else, and `x/checkpoint` depends on neither `x/attestor` nor `x/proofs`. A checkpoint is finalized whether or not anyone ever attests to it or proves anything about it.

A relying party's workflow is simple: query `x/checkpoint` for the anchor, then query `x/attestor` and `x/proofs` for whatever additional assurance the sidechain's declared tier provides.

## 4. Assurance Tiers

Every sidechain declares a tier at registration and can raise it later. Tiers are cumulative, and each has its own minimum bond, set by governance.

| Tier | Name | What it provides |
| --- | --- | --- |
| 1 | Notarized | A timestamped, signed, chained commitment. Nothing beyond the anchor. |
| 2 | Attested | Everything in Tier 1, plus signed claims from staked third parties (certification bodies, auditors, regulators) about specific checkpoints, with a bonded dispute process. |
| 3 | Proven | Everything in Tier 2's foundation, plus every checkpoint must carry a zero-knowledge anchor, so that validity proofs can be verified on-chain against it. |

Tiers can be upgraded but never downgraded. Allowing downgrades would release bond immediately and bypass the exit cooldown that gives governance time to slash. Upgrading requires the resulting bond to meet the new tier's minimum. As an illustration, with minimum bonds of 1,000,000, 5,000,000 and 10,000,000 units, a sidechain holding 1,000,000 needs a 4,000,000 top-up to reach Attested, or 9,000,000 to jump directly to Proven.

## 5. Protocol Modules

### 5.1 Sidechain registry (`x/sidechain`)

A sidechain registers by posting a bond and declaring:

- an identity (`id`, name, metadata URI),
- an **operator** account, the only account that can run operator actions,
- a set of **checkpoint signer keys** and a **signature threshold** (for example, 2 of 3),
- an expected checkpoint interval, and
- its assurance tier.

Registration can be permissionless (the bond is the only gate) or governance-gated, depending on the `auto_activate` parameter. A sidechain moves through a defined lifecycle:

- `PENDING`, `ACTIVE`, `SUSPENDED`, `SLASHED`, `EXITED`.
- Governance can suspend, resume and slash. Only the operator can exit.
- A sidechain that misses its checkpoint interval plus a grace period is automatically suspended.
- `EXITED` is terminal. The remaining bond is returned after a cooldown (14 days in the reference configuration).

Signer sets are versioned. Every rotation increments `signer_set_version`, and each checkpoint records the version it was signed under. To prevent a compromised operator account from swapping the entire signer set in one transaction, a rotation must also carry signatures from at least a threshold of the **current** signers over a purpose-built signing document. Each approval is valid for exactly one rotation and expires at a stated block height.

### 5.2 Checkpoints (`x/checkpoint`)

A checkpoint is a signed commitment to a batch of off-chain records. It carries a state root, a record count, a pointer to where the data is held, a link to the previous checkpoint, and signatures from the sidechain's signers.

When a checkpoint is submitted, the module checks, in order:

1. Message size and field limits.
2. That the sidechain exists, is `ACTIVE`, and that the signer-set version matches the current one.
3. Tier requirements (Proven sidechains must include a ZK anchor).
4. Strict sequencing: the sequence number must be exactly the last accepted number plus one, and the previous-checkpoint hash must match. Gaps and replays are rejected.
5. Every submitted signature, against the registered keys and threshold. Any invalid, duplicate or out-of-range signature rejects the whole message.
6. Fee payment.

The whole handler runs in a cached context, so a failure at any step leaves no partial state. Once written, a checkpoint is immutable. No message in the module can update or delete it.

The checkpoint hash is SHA-256 over a domain-separated signing document that includes the chain ID, sidechain ID, sequence, state root, previous hash, record count, signer-set version, data pointer and optional ZK anchor. Each checkpoint hash is linked into the next, so a sidechain's history forms a hash chain that anyone can replay and verify.

**Proving a single record.** The chain stores only the root. A relying party who wants to prove that a specific record was included obtains the record and its Merkle path from the sidechain operator, recomputes the root, and compares it with the finalized on-chain value. The chain never sees the record itself.

**Batching.** Fees follow `base_fee + per_record_fee × record_count`. Because the marginal on-chain cost of a checkpoint is essentially constant regardless of how many records it represents (one state write and one signature check), high-volume sources such as IoT fleets can aggregate millions of records into a single checkpoint.

### 5.3 Attestations and disputes (`x/attestor`)

Tier 2 recognizes that many claims about real-world facts cannot be checked by code. Instead, institutional parties with a stake in their reputation make signed claims, and the system provides a way to challenge them.

**Attestors** register with a bond and are activated by governance. Each is authorized for specific schemas only.

**Schemas** are governance-published definitions of exactly what a claim asserts, what it excludes, which data sources it relies on, how long it stays valid, and how long it can be disputed. Schemas are immutable. To change one, governance retires it and publishes a new version, so a reader of an old attestation always knows precisely what was and was not claimed.

**Attestations** bind a claim payload to a specific checkpoint. The checkpoint's hash is part of the signed document, so an attestation cannot reference a nonexistent or future checkpoint, and each (schema, sidechain, sequence, attestor) combination can be attested only once.

**Disputes.** Any account other than the attestor's own operator can challenge an active attestation within its dispute window by posting a bond and an evidence URI.

- If the dispute is **upheld**, the attestor is slashed by a configured fraction of its bond. The challenger recovers its bond and receives a share of the slashed amount, and the remainder is burned. The attestation is marked overturned.
- If the dispute is **dismissed**, the challenger's bond is burned, which discourages frivolous challenges.

**Conflict of interest.** An attestor cannot attest to a sidechain with which it shares an operator or any signer-key address. This check runs at submission time and fails closed.

**Reputation.** Each attestor carries a reputation score (plus one per attestation, minus two per upheld dispute). It is explicitly informational, for explorers and user interfaces only, and never gates consensus-critical logic.

### 5.4 Validity proofs (`x/proofs`)

Tier 3 removes the need to trust anyone's honesty for narrow, mathematically expressible claims. A prover submits a zero-knowledge proof about a specific checkpoint and claim. The chain verifies it synchronously inside the transaction. Either the proof is valid and is recorded, or the transaction fails. There is no dispute window because there is nothing to dispute.

**Binding.** Every proof must be tied to exactly one checkpoint and claim on this chain. The module computes a 32-byte binding from the chain ID, sidechain ID, checkpoint sequence, state root, checkpoint hash and claim ID. The first two public inputs of the proof must equal this binding, so a valid proof cannot be replayed against a different checkpoint, claim or chain. Further public inputs can be required to equal the checkpoint's ZK-anchor records root, its record count, or a fixed value.

**Verifiers are code, not data.** Governance registers a verification key for a `(proof_system_id, claim_id)` pair, but the verification logic itself is compiled into the node binary. Governance cannot make the chain accept a new proof system without a coordinated software upgrade. This is a deliberate safety property: arbitrary verification logic is never accepted as untrusted on-chain data.

**Fault handling.** A panic inside a verifier becomes an ordinary deterministic rejection, so a malformed proof cannot crash block execution. An infrastructure fault is never reported as an invalid proof. During block execution, it halts the affected node rather than letting it diverge from consensus.

**One checkpoint, one record set.** When a ZK anchor is present, the checkpoint's state root must be derived from the anchor and the record count. A proof verified against the anchor is therefore a proof about the same records that the Tier 1 commitment covers.

## 6. Security Model

**Signatures and replay protection.** Every signed document carries a unique domain prefix, the chain ID and a signer-set version. A signature made for one purpose, chain or key set cannot be valid in another.

**Atomicity.** Handlers for checkpoints, attestations and proofs run in cached contexts. Failed messages leave no partial state.

**Cheap checks first.** Handlers run inexpensive validation (tier, size limits, existence) before expensive signature or proof verification, and gas for verification is charged per operation. This limits the cost an attacker can impose with malformed submissions.

**Bond accounting invariant.** The bond held in each module account equals the sum of all recorded bonds, across registrations, upgrades, slashes and exits. Keeper tests assert this after every operation.

**No cross-module slashing.** The only route to slash a sidechain is a governance message. Other modules have no way to do it.

**Exit cooldowns.** Operators cannot withdraw bonds instantly. Sidechain exit uses a cooldown, and attestor exit waits for the longer of the cooldown and the longest dispute window among its schemas, and only once no dispute is open.

**Bounded failure.** Bond returns are processed in isolated contexts, so a bad entry is logged and skipped rather than halting the chain.

**Testing.** The test plan covers unit tests per keeper method, tier-upgrade tests, simulation-based fuzzing of message sequences against invariants, multi-validator integration tests and load tests of the checkpoint path at high record counts.

## 7. Quantum Resistance

LucidChain treats quantum resistance as a staged, honestly scoped property rather than a blanket claim.

**What is quantum-resistant today.** Signatures. The chain supports ML-DSA-65 (the NIST-standardized lattice-based scheme) for validator consensus keys, account keys, sidechain checkpoint signers and attestor signers. A quantum-resistant deployment restricts the allowed public-key types to ML-DSA-65 only, because allowing any classical type re-opens the sidechain to quantum forgery of its checkpoint signatures.

**What this costs.** ML-DSA-65 public keys are 1,952 bytes and signatures are 3,309 bytes. Transactions are larger, and the first transaction from an account writes its public key to state and needs more gas. Checkpoints are capped at 7 signatures and 24,000 signature bytes, which is exactly enough for seven ML-DSA-65 signatures.

**What is not yet quantum-resistant.** Tier 3 is the one part of the chain still being migrated. The original verifiers use Groth16 and PLONK over the BN254 curve, whose security rests on elliptic-curve pairing assumptions that a large quantum computer could break. After such a break, a forged proof could pass verification. The ZK anchor is coupled to the same curve (MiMC over BN254). A hash-based replacement is in progress (below).

**How the two modes are handled.** The reference tooling provides two modes that never share state:

- A **classical development mode** that includes the BN254 verifiers, so Tier 3 can be exercised end to end.
- A **quantum-resistant mode** with ML-DSA-65 keys only, no pairing-based verifiers registered, and the Proven tier unusable.

**The path forward: a hash-based verifier (in progress).** Tier 3 quantum resistance is being delivered by replacing the pairing-based verifiers with a hash-based STARK/FRI-style system, which has no pairing or elliptic-curve assumption. The reference code uses Plonky3 over the BabyBear field (proof system ID `plonky3-babybear-v1`). It implements the existing `ProofVerifier` interface, so the keeper does not change. The design works as follows:

- **Go validates, Rust decides.** A Go adapter checks and canonicalizes everything it can: the key, the public inputs, the checkpoint binding and the size limits. Only then does it ask an isolated verifier for a verdict: valid, invalid or malformed.
- **Keys are identifiers, not key material.** A verification key is a 32-byte `vk_id` that commits to the circuit, the field, the hash, the FRI parameters, the public-input layout and the verifier version. The parameters live in the pinned verifier image, never in the proof. Each `vk_id` must be on an allowlist compiled into the node binary.
- **One encoding per input.** Public inputs are a count followed by canonical BabyBear field elements, and trailing bytes are rejected. The first 16 elements are the checkpoint binding, split into 16-bit limbs.
- **Pinned verifier image.** Every verdict carries the hash of the image that produced it. A mismatch with the pinned hash is a fault, not a rejection, so all validators run the identical verifier.
- **Faults are never verdicts.** A crash, timeout or transport failure is an infrastructure fault that halts the node, as in the existing design. Only the verifier's own answer can reject a proof. Proof size is capped before the verifier is called, so it cannot be used to amplify load.
- **Startup self-check.** At startup the node sends a junk proof for every allowlisted `vk_id`. A healthy verifier answers invalid or malformed. Anything else stops the node from starting, instead of halting it on the first real proof.

One transport to the isolated verifier exist behind one interface:

1. **In-process WebAssembly.** The verifier, compiled to WebAssembly, runs under the wazero runtime. The module imports nothing (no WASI, no host functions). The transport caps concurrent calls and guest memory, enforces a per-call timeout, and discards an instance after any trap or timeout instead of reusing it.

The WebAssembly transport is tested with real proofs at 2^8 and 2^14 rows. The tests confirm that tampered, truncated and padded proofs are deterministic rejections rather than faults, and that a timeout or starved memory is a fault from which the transport recovers. Which transport ships is not yet decided.

**What remains before the Proven tier can be enabled in quantum-resistant mode:**

- **Circuits and key metadata.** The number of public values is a stopgap constant in the adapter and should be held per `vk_id`.
- **A hash-based anchor scheme.** The current anchor, and the rule that binds a proof to the records root, assume BN254. The new verifier compares each expected value with a single BabyBear element, so a 32-byte records root cannot be bound directly.
- **Deterministic limits.** Timeouts are wall-clock, which can differ between validators. Verification cost also needs a deterministic bound (Section 11).
- **Gas and size limits.** Verification gas must be benchmarked, and the proof-size cap must be raised well above the current 8,192 bytes, because hash-based proofs are much larger.

Proofs accepted before a quantum break remain timestamped on-chain records. Proofs submitted after one could not be trusted. This is the reason Tier 3 is described as conditional on its proof system.

## 8. Economics and Incentives

LucidChain uses two tokens with separate jobs. The chain's security does not depend on stable prices, and customers' costs do not depend on a volatile asset. The roles below are the design intent. Section 11 lists what is still to be aligned in the specification.

| Property | Staking token | Utility token |
| --- | --- | --- |
| Value behavior | Volatile | Stable |
| Primary job | Secures the chain and backs bonds | Pays for services on the chain |
| Used for | Validator staking, sidechain, attestor and dispute bonds, governance voting power | Checkpoint, proof and other fees, and gas |
| Who holds it | Validators, delegators, sidechain operators, attestors, challengers | Sidechain operators, provers and other users of the chain |

This paper does not specify supply, distribution, pricing or the utility token's stabilization mechanism. Those decisions are outside the current specification (Section 11).

**Bonds.** Sidechains, attestors and challengers post bonds in the staking token. Bonds make misbehavior costly and spam expensive. Because the staking token is volatile, a bond's real-world value moves with its price. A tier minimum set in token units can become too small after a price fall, or too large after a rise.

**Slashing.** Slashed bonds are denominated in the staking token. Slashed sidechain bonds are burned: nobody is paid, and total supply falls by the slashed amount. Slashed attestor bonds are split between the successful challenger, as a reward, and a burn.

**Fees.** The utility token is the intended fee currency, so customers pay in a stable unit.

- Checkpoint fee: `base_fee + per_record_fee × record_count`. Both default to zero in the reference configuration and are tuned by governance. The per-record component is deliberately a fraction of true per-record cost, because the chain's marginal cost per checkpoint is nearly constant.
- Proof fee: a flat `base_verification_fee`, charged in a separately configured `fee_denom`, plus per-registration verification gas that is spent whether or not the proof is valid, which discourages spam.
- Gas: signature and proof verification gas is parameterized. Current values are placeholders pending benchmarks on validator hardware.

**Bond adequacy.** The tier minimum is checked when a sidechain upgrades, not afterwards. A governance increase of minimums, a slash or a fall in the staking token's price can leave a sidechain holding less security than its tier implies. Whether to require top-ups, or to express minimums in value terms, is an open design question.

## 9. Governance

Governance is exercised through the standard Cosmos `x/gov` module and is used for:

- activating attestors and, optionally, sidechains,
- publishing and retiring attestation schemas,
- registering and deprecating proof verifiers (limited to proof systems already compiled into the binary),
- suspending, resuming and slashing sidechains,
- reinstating suspended attestors,
- resolving attestation disputes (by default), and
- updating module parameters.

Using governance for dispute resolution is a deliberate interim choice. A decentralized arbitration mechanism involves juror selection, incentive compatibility and evidence handling, and deserves its own specification once there is real dispute volume to learn from. A placeholder would probably have to be redesigned. The resolver address is configurable.

## 10. Use Cases

The following are illustrative. They draw on the examples used in the specification and development guides.

**Business reporting integrity (Tier 1 to 2).** A hospitality platform keeps its transaction ledger private and anchors batches of records every few minutes. A staffed auditor then attests that reported monthly revenue matches the source data, under a schema that also states what the check does not cover (for example, transaction authenticity).

**Identity and KYC evidence (Tier 2).** A certification body attests that a subject passed a KYC check, under a schema with a one-year validity period and an explicit exclusion for sanctions screening. A counterparty can verify who attested, when, against which checkpoint, and whether the attestation was later disputed.

**Provable compliance (Tier 3).** A fleet operator proves, without revealing individual locations, that a batch of records satisfies a geofence condition. The reference tooling demonstrates this with records of longitude, latitude and timestamp, committed under a Merkle root and proven with both Groth16 and PLONK. AML-style threshold checks are a likely next class of claim.

**High-volume sensor data.** IoT and smart-city sources can aggregate very large numbers of readings at the edge and anchor a single root per interval, with a per-checkpoint limit of one million records in the reference configuration.

## 11. Limitations and Open Items

The specification is explicit about what is unfinished. Readers should weigh the following.

1. **Tier 3 quantum resistance is in progress.** The Groth16 and PLONK verifiers are not quantum-resistant, and the hash-based replacement is not yet enabled. In quantum-resistant mode the Proven tier stays disabled until it is (Section 7).
2. **Slashing conditions for sidechains are not finalized.** The mechanics of slashing exist, but the fault conditions that justify it (for example, two conflicting checkpoints at the same sequence signed by registered keys) need a dedicated fraud-detection specification before mainnet.
3. **Centralized interim authorities.** Dispute resolution, attestor activation and slashing are governance actions. There is no decentralized arbitration yet.
4. **Placeholder economics.** Verification gas, the proof base fee and checkpoint fees are placeholders pending benchmarks.
5. **No tier downgrades, and no retroactive tier re-check.** A slash or a minimum-bond increase does not force a top-up, and a slashed Proven sidechain keeps its tier label even though it stops checkpointing.
6. **No attestor-availability check at upgrade.** An operator can pay for the Attested tier even if no attestor currently serves its domain. Fixing this needs a domain field on sidechains and would create a module dependency cycle that must be resolved first.
7. **Attestations to inactive sidechains.** Whether attestations should require the sidechain to be `ACTIVE` is an open decision.
8. **No raw-data validation.** No module inspects sidechain data. The chain proves integrity and timing of commitments, not the truth of what was committed. Garbage in, notarized garbage out. Truthfulness is the job of attestors and proofs.
9. **Data availability is off-chain.** The data pointer is an unverified reference. If an operator stops serving the underlying data, the commitment remains but cannot be opened.
10. **HSM-backed attestor keys.** The Cosmos SDK keyring has no native HSM backend, and remote-signing support for application-level signatures is still maturing. This does not affect the protocol, but it should be settled operationally before onboarding the first real attestor, particularly a regulator.
11. **Account-key enforcement.** In the current quantum-resistant guide, classical account keys are not rejected at the transaction level. Until an ante handler enforces it, key-type discipline is operational.
12. **No IBC integration yet.** Events are designed to make light-client integration straightforward later.
13. **The utility token's stabilization mechanism is not specified.** This paper does not say how the utility token holds its value (for example reserve backing, algorithmic control or a managed peg), who issues or redeems it, or what happens if it loses its peg. These choices define the token's risk profile, and stable-value tokens can attract specific regulation in some jurisdictions. Both need legal review.
14. **Fee and bond denominations are not yet aligned with the two-token design.** The reference specification denominates checkpoint fees and bonds in a single bond denomination. Moving fees to the utility token, and fixing which module charges in which token, is a specification change still to be made.
15. **Verifier limits are wall-clock.** In the reference code a verifier timeout is a fault that halts the node. Wall-clock timeouts can differ between validators, so verification cost also needs a deterministic bound (by proof and trace size, or by metering), with a timeout margin large enough that honest nodes never disagree.

## 12. Roadmap

The items below follow from the specification's stated plans. They are directions, not dated commitments.

- **Fraud-proof specification** for sidechain slashing, ahead of mainnet.
- **Gas benchmarking** of ML-DSA-65 and proof verification on validator hardware.
- **Quantum-resistant proving (in progress):** finish the hash-based verifier. Remaining work: circuits and per-key public-value counts, a hash-based anchor scheme, larger proof-size limits, deterministic verification bounds, and a choice between the microVM and in-process transports. Then register the verifier and enable the Proven tier in quantum-resistant mode.
- **Two-token design:** specify the utility token's stabilization mechanism, align fee and bond denominations, and review the regulatory position.
- **Attestor operations:** HSM-backed key custody, and an attestor-availability mechanism.
- **Decentralized dispute arbitration,** once dispute volume justifies it.
- **IBC light-client wiring** to let other chains consume checkpoints.
- **Additional proof claims,** driven by demand (geofencing and AML thresholds are the first candidates).

## 13. Conclusion

Private ledgers need public accountability, and public chains do not need to see private data to provide it. LucidChain splits the problem cleanly. A minimal, quantum-resistant public chain finalizes compact commitments, and two optional layers of evidence, staked human attestation and cryptographic proof, can be added per sidechain according to how much assurance its counterparties need.

The design is deliberately narrow, and it is candid about its boundaries. The anchor is solid and the signature layer is post-quantum. The proof layer is functional today and is being migrated to a hash-based, quantum-resistant verifier, and several governance and economic parameters are still to be finalized. For organizations that want their records to be verifiable by outsiders without being exposed, that is a coherent and incrementally adoptable foundation.

## Appendix A: Glossary

- **Anchor:** the finalized, on-chain checkpoint that commits to off-chain records.
- **Attestor:** a staked institutional party that signs claims about checkpoints.
- **Binding:** a 32-byte hash that ties a proof to one chain, sidechain, checkpoint and claim.
- **Checkpoint:** a signed commitment to a batch of off-chain records.
- **ML-DSA-65:** a NIST-standardized post-quantum digital signature scheme.
- **Schema:** an immutable, governance-approved definition of what an attestation claims and excludes.
- **Sidechain:** a private chain that registers with LucidChain and publishes checkpoints.
- **Staking token:** the volatile token that secures the chain and backs bonds.
- **State root:** the Merkle or binding hash committed in a checkpoint.
- **Utility token:** the stable token that pays for services on the chain.
- **vk\_id:** a 32-byte identifier for a verification key in the hash-based verifier. It commits to the circuit, parameters and verifier version.
- **ZK anchor:** an optional commitment inside a checkpoint that validity proofs bind to.

## Appendix B: Reference Parameters (Draft)

| Parameter | Reference value | Note |
| --- | --- | --- |
| Max records per checkpoint | 1,000,000 | Governance-tunable |
| Max signatures per checkpoint | 7 | Sized for ML-DSA-65 |
| Max signature bytes per checkpoint | 24,000 | 7 × 3,309 bytes plus headroom |
| Max data pointer size | 256 bytes |  |
| State root size limit | 64 bytes | Normally 32 |
| Signature verification gas | 50,000 | Placeholder |
| Sidechain exit cooldown | 14 days | Reference configuration |
| Example tier bonds | 1M / 5M / 10M staking-token units | Illustrative |
| Max proof size (QR mode) | 8,192 bytes | To be raised well above this for hash-based proofs |
