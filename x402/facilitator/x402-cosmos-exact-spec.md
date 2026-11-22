# x402 `exact` scheme for lucidchain (Cosmos SDK) — DRAFT

**Status:** draft for internal review. Reference code: `x402/facilitator/` (core verify/settle logic is unit-tested; SDK and node adapters are not written yet).

**Before publishing or proposing upstream, check against the current x402 spec:** header names (v1 used `X-PAYMENT` / `X-PAYMENT-RESPONSE`; later versions renamed them), `PaymentRequirements` / settle-response field names, and the acceptance criteria in x402's `CONTRIBUTING.md`. Field names below follow the v2 style and may need adjusting.

## 1. Identifiers

| Item | Value |
|---|---|
| `network` | CAIP-2: `cosmos:<chain-id>` (testnet today: `cosmos:my-testnet-1`). The chain-id is final once published. |
| `asset` | The base denom string (single denom; final name still to be chosen). |
| `amount` | Integer string in base-denom units (no decimals). |
| `payTo` | bech32 account address on the chain. |
| `scheme` | `exact`. |

Classic and post-quantum (PQ) account keys coexist on one chain, so one `network` serves both.

## 2. Payment requirements (resource server → client)

```json
{
  "scheme": "exact",
  "network": "cosmos:my-testnet-1",
  "amount": "10000",
  "asset": "<base-denom>",
  "payTo": "cosmos1...",
  "maxTimeoutSeconds": 60,
  "extra": {
    "flow": "send",
    "keyTypes": ["/cosmos.crypto.secp256k1.PubKey", "/cosmos.crypto.mldsa65.PubKey"],
    "transports": ["inline"],
    "feeMode": "payer",
    "paymentId": "<unique per request>"
  }
}
```

- `maxTimeoutSeconds` MUST be at most the chain's unordered-tx window (SDK default 10 minutes); 30–60 s is recommended.
- `keyTypes` lists pubkey type URLs the chain accepts for payments. Removing the classic type is how classic keys are phased out; no protocol change is needed.
- `paymentId` binds the payment to the request and is carried in the tx memo.

## 3. Payment payload (client → resource server)

```json
{
  "x402Version": 2,
  "scheme": "exact",
  "network": "cosmos:my-testnet-1",
  "payload": {
    "transport": "inline",
    "keyType": "/cosmos.crypto.secp256k1.PubKey",
    "tx": "<base64 TxRaw>"
  }
}
```

- **`inline`** (default, implemented): the full signed `TxRaw` is in the payload.
- **`reference`** (reserved, not implemented): the payload carries only `ref`, and the signed tx is uploaded to the facilitator separately. Add this only if measurement shows PQ payloads exceed header limits in your target stack. It adds a round trip and deviates from what generic x402 clients expect.

### Size budget (estimates; measure on your build)

| | Classic (secp256k1) | PQ (ML-DSA-65) |
|---|---|---|
| Public key / signature | 33 B / 64 B | 1,952 B / 3,309 B (FIPS 204) |
| Signed payment tx | a few hundred bytes | roughly 5–6 KB |
| Base64 header payload | well under 1 KB | roughly 7 KB or more |
| Size gas at 10 gas/byte | ~3k | ~55k |

Many HTTP servers and proxies cap a header line near 8 KB. Test PQ payloads through every hop (CDN, reverse proxy, resource server) before relying on `inline`.

## 4. Flow `send` (the normative flow)

The payer signs a single unordered `bank.MsgSend` for exactly the required amount. This flow is trust-minimizing: the signed bytes can only move the stated amount to `payTo`, and the facilitator cannot alter them.

### 4.1 Transaction rules

The signed tx MUST have:

1. Exactly one message, of type `/cosmos.bank.v1beta1.MsgSend`, and exactly one signer equal to `from_address`.
2. `to_address == payTo`; a single coin with `denom == asset` and `amount == amount` exactly (not more, not less).
3. `unordered = true`, signer sequence `0`, and a `timeout_timestamp` such that `MinRemaining <= timeout - now <= min(MaxWindow, maxTimeoutSeconds)`. Payers need reasonably accurate clocks (use a 30–60 s margin).
4. Memo equal to `"x402:" + paymentId`.
5. No fee granter or fee payer unless `feeMode` is `sponsored`, in which case only the facilitator's sponsor address is allowed.
6. A signing pubkey whose type URL equals `payload.keyType` and is in the allowed list.

### 4.2 Verification (`/verify`)

A facilitator MUST perform, in order, and return `isValid: false` with a reason code on the first failure:

1. scheme, network and asset match; transport is supported;
2. decode and parse the tx; check the rules in 4.1;
3. `payTo` is **not a module account** (bank's blocked list is narrower than the set of module accounts: `gov`, IBC transfer, ICA and the staking key-rotation pool may not be blocked, so a payment there would succeed on-chain but be unrecoverable);
4. **verify the signature** against the sign doc (see 4.3);
5. payer balance covers `amount` (plus a fee reserve when the payer pays fees);
6. `Simulate` succeeds (messages would execute). Record gas used for pricing.

### 4.3 Signature verification — IMPORTANT

**`Simulate` does not verify signatures.** The SDK skips signature verification when simulating, so a facilitator that relies on `Simulate` alone would accept forged payments. Implement one of:

- **Local verification (recommended):** fetch the payer's account number and pubkey from the chain, rebuild the sign doc, and verify with the SDK's sign-mode handler. This works for any key type through the `PubKey` interface, which keeps the facilitator algorithm-agnostic across classic and PQ keys.
- **Node-side CheckTx:** only after confirming it is read-only on your node. A CheckTx call normally mutates the node's check state (sequence or unordered-nonce bookkeeping), so a "verify" call could make the later broadcast fail.

### 4.4 Settlement (`/settle`)

1. Claim the payment (key = network + payer + hash of signed bytes) so concurrent or repeated calls cannot double-settle.
2. Run full verification again (state may have changed since `/verify`).
3. Broadcast in **sync** mode; CheckTx rejection means failure (release the claim).
4. Poll for the tx by hash until it appears in a block or a confirmation timeout elapses.
5. Return `success`, `transaction` (tx hash), `payer`, `network`. A tx included with a non-zero code is a failure (`tx_failed`).
6. Ambiguous broadcast errors: check by hash first; re-broadcasting identical bytes is safe because the chain rejects a repeated `(sender, timeout_timestamp)` pair.

The chain's unordered-nonce rule is a second line of defence against double settlement; the claim store is the first.

## 5. Fee modes

| Mode | How | Status |
|---|---|---|
| `payer` | Payer signs with `SIGN_MODE_DIRECT` and pays gas from their balance. | Implemented in the reference code. |
| `sponsored` | Payer signs only the tx body with `SIGN_MODE_DIRECT_AUX`; the facilitator adds fee and gas and signs last. The payer needs no gas balance and no feegrant. | **Unverified.** Prototype first: confirm it works with unordered txs and with ML-DSA keys on your build. |

A `fee-granter` feegrant is an alternative sponsorship route but needs a one-time on-chain allowance per payer.

## 6. Key types and the PQ transition

- Both classic and PQ account keys are valid at once. The facilitator reads allowed type URLs from configuration (ideally chain state or a governed list) and advertises them in `/supported`.
- Phase-out is a configuration and governance change: remove the classic type URL. The reference code tests this.
- Account keys cannot be rotated in place as far as the v0.55 notes show, so agents moving to PQ keys likely need new addresses, moved funds and re-issued grants. Plan a migration path before the phase-out date.
- **Enforcement gap:** the files reviewed show no on-chain restriction on which account key types may sign. If classic keys must be refused at the chain level, add a governed allowlist enforced in an ante decorator. Until then, the facilitator's allowlist protects only x402 payments.

## 7. Delegated flow (authz) — NOT part of `exact` v1

Granting the facilitator an `authz` `SendAuthorization` lets it spend on the payer's behalf, but it is **not trust-minimizing**: the facilitator can spend up to the grant's limit to any allowed recipient, and an off-chain "payment intent" signature cannot be enforced by the chain. It is acceptable only where a payer explicitly trusts a specific facilitator and scopes the grant tightly (spend limit, `allow_list`, expiry). If you need on-chain enforcement of per-payment intents, that is the case for a small custom module (nonce and intent verification), not a change to `x/bank`.

## 8. Chain prerequisites (checklist)

- [ ] `EnableUnorderedTransactions: true` in the auth module config (confirmed in `app_config.go`) and `auth` in `PreBlockers` (confirmed).
- [ ] Single base denom chosen; denom metadata in genesis (`patch-single-denom.sh`).
- [ ] Non-zero `minimum-gas-prices` on every node (profile first; it is a per-node mempool filter, not a consensus rule).
- [ ] Finite block `max_gas` above the heaviest tx (PQ proof verification uses about 1,000,000 gas in your runbook).
- [ ] `query-gas-limit` set on nodes that serve the facilitator.
- [ ] Decide which module accounts to add to the bank blocked list (`transfer`, `ica`, key-rotation pool).
- [ ] ICA host disabled or restricted (it currently allows all messages).
- [ ] Faster `timeout_commit` (confirm it is still honoured by CometBFT v0.40).
- [ ] Facilitator runs beside a dedicated node; gRPC stays on localhost.
- [ ] Facilitator signer keys in a keyring backend or KMS, never exported or in shell history.

## 9. Security considerations

- **Forgery:** never accept a payment on `Simulate` alone (4.3).
- **Replay:** unordered-nonce uniqueness plus the claim store; keep the expiry window short.
- **Front-running the payer's balance:** a payer can sign a valid tx and spend the funds before settlement; settle promptly and keep `maxTimeoutSeconds` small.
- **Facilitator trust:** in `send` flow the facilitator cannot change amount or recipient; in `sponsored` mode it controls fee and gas only.
- **Replay across chains:** the chain-id is part of the sign doc; do not reuse chain-ids between testnet and mainnet.
- **DoS:** bound request size (header and body), rate-limit `/verify`, and note that PQ verification costs more CPU and gas.
- **Panics:** the app installs a hard-halt recovery handler, so a panic reachable from any message type can halt nodes; keep custom-module message handlers panic-free.

## 10. Reason codes

`unsupported_scheme`, `unsupported_network`, `unsupported_asset`, `unsupported_transport`, `malformed_payload`, `unexpected_messages`, `recipient_mismatch`, `amount_mismatch`, `key_type_not_allowed`, `not_unordered`, `sequence_must_be_zero`, `expiry_too_soon`, `expiry_too_far`, `memo_mismatch`, `payto_is_module_account`, `fee_fields_not_allowed`, `invalid_signature`, `insufficient_funds`, `simulation_failed`, `internal_error`; settle-only: `settlement_in_progress`, `broadcast_failed`, `tx_failed`, `confirmation_timeout`.

## 11. Open questions

1. Final base denom name and decimals.
2. Gas price and `max_gas` from profiling (classic vs PQ, ordered vs unordered, `MsgSend` vs `MsgExec`).
3. Does `SIGN_MODE_DIRECT_AUX` work for unordered txs and ML-DSA keys on this build?
4. Are PQ-sized payloads acceptable end to end, or is `reference` transport needed?
5. Does the chain need an on-chain restriction on account key types?
6. Exact x402 header names and response field names for the target spec version.
