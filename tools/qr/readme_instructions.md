# Jump start your node (quantum-resistant edition)

This guide and its tools live in `tools/qr/`. The original (classical) instructions and tools are untouched, so you can still run the chain in non-quantum-resistant mode for dev. The two modes never share state:

| | Classical dev | Quantum-resistant |
|---|---|---|
| Node home | `~/.lucidchain` | `~/.lucidchain-qr` (`$QR_HOME`) |
| Work area | `~/tmp_workarea` | `~/tmp_workarea_qr` |
| Tools | `tools/...` | `tools/qr/...` (binaries in `~/tools/qr/`) |
| Keys | secp256k1 / ed25519 | ML-DSA-65 only |

Only one chain can run at a time (both use the default ports).

**Enter QR mode (do this first, and again in every new terminal)**

```bash
cd ~/go/src/github.com/lucidpay/lucidchain
source tools/qr/env.sh
```

This defines a `lucidchaind` shell function that adds `--home $QR_HOME` to every call, so every command below automatically targets the QR node. Run `qr-off` to return to the classical setup. To check which mode you are in: `type lucidchaind` prints a function in QR mode and a path otherwise.

## Ground rules

This chain is post-quantum from block one. Follow these rules in every step below:

1. **Every key is ML-DSA-65.** Validator consensus keys, account keys, sidechain signer keys and attestor signer keys all use `ml_dsa_65` (type URL `/cosmos.crypto.mldsa65.PubKey`). Always pass `--key-type ml_dsa_65` to `keys add`.
2. **No classical key types, not even in dev.** Do not add `secp256k1` or `ed25519` to any allow-list. The old dev override has been removed from this guide.
3. **Fresh mnemonic for every ML-DSA key.** Never recover an ML-DSA key from a mnemonic that was ever used for a `secp256k1` key: the ML-DSA seed and the secp256k1 private key would be the same secret.
4. **No pairing-based proof systems.** Groth16 and PLONK over BN254 are not quantum-resistant and have been removed. The `x/proofs` module stays without verifiers until a hash-based (STARK/FRI-style) verifier is added. See "Zero-knowledge proofs" below.
5. **Expect bigger transactions.** An ML-DSA-65 public key is about 1.9 KB and a signature about 3.3 KB. The first transaction from an account writes its public key to state and runs out of the default 200000 gas, so always use `--gas auto`.

Account-key enforcement note: the chain currently does not *reject* classical account keys at the transaction level. Until the ante handler is in place, rule 1 is enforced by discipline, and the checks below catch mistakes.

Start by building your work area in your home directory after getting the source code

```bash

mkdir tmp_workarea_qr
cd tmp_workarea_qr
mkdir json
mkdir out
cd

```

Initialize the blockchain with an ML-DSA consensus key

```bash

lucidchaind init dev-test-node --chain-id my-testnet-1 --consensus-key-algo ml_dsa_65

nano $QR_HOME/config/app.toml

```

`--consensus-key-algo ml_dsa_65` also replaces the genesis `pub_key_types` list with only `ml_dsa_65`. The consensus key is generated at init, so if you already initialized without the flag, re-run init (`--overwrite`) or start from a fresh home before creating the gentx.

**modify using nano**

minimum-gas-prices = "0ucheck" 

**Ctrl+x and y to save**

```bash

lucidchaind keys add qr_validator --key-type ml_dsa_65

lucidchaind genesis add-genesis-account qr_validator 100000000stake,1000000001000ucheck

lucidchaind genesis gentx qr_validator 1000000stake --chain-id my-testnet-1

lucidchaind genesis collect-gentxs

```

**Setting up the genesis**

The default genesis already restricts sidechain and attestor signer keys to `/cosmos.crypto.mldsa65.PubKey`. Leave those lists alone.

A fresh `lucidchaind init` sets `auto_activate` to `false`, which leaves a new sidechain `PENDING` so every checkpoint is rejected. Set it to `true` for dev.

make sure you are in your home directory

```bash

G=$QR_HOME/config/genesis.json
jq '.app_state.sidechain.params.auto_activate = true' "$G" > /tmp/g.json && mv /tmp/g.json "$G"

```

Note: with the default genesis the governance voting period is 48 hours, so each proposal below takes two days to pass. For a dev chain, shorten it before the first start:

```bash
#go back to home directory

G=$QR_HOME/config/genesis.json
jq '.app_state.gov.params.voting_period = "60s" | .app_state.gov.params.expedited_voting_period = "30s"' "$G" > /tmp/g.json && mv /tmp/g.json "$G"

```

Add attestors schema

```bash
G=$QR_HOME/config/genesis.json

jq '.app_state.attestor.schemas = [
  {
    "id": "kyc-v1",
    "title": "KYC check",
    "domain": "identity",
    "claim_description": "Subject passed KYC",
    "exclusions": "Does not cover sanctions screening",
    "required_data_sources": [],
    "validity_period_seconds": "31536000",
    "dispute_window_seconds": "86400",
    "active": true
  }
]' "$G" > /tmp/g.json && mv /tmp/g.json "$G"

```

**Quantum-resistance check (must pass before starting)**

```bash
G=$QR_HOME/config/genesis.json

jq -e '
  .consensus.params.validator.pub_key_types == ["ml_dsa_65"]
  and .app_state.sidechain.params.allowed_pubkey_type_urls == ["/cosmos.crypto.mldsa65.PubKey"]
  and .app_state.attestor.params.allowed_pubkey_type_urls == ["/cosmos.crypto.mldsa65.PubKey"]
  and (.app_state.proofs.verifiers | length) == 0
' "$G" && echo "PQ genesis check: OK"

# qr_ keys that are NOT ML-DSA-65 (expect 0)
lucidchaind keys list --output json | jq '[.[] | select(.name | startswith("qr_")) | select(.pubkey | contains("mldsa65") | not)] | length'

unset G
```

If the `jq` check does not print `OK`, or the key count is not `0`, stop and fix it before continuing.

Validate, reset and start the test chain

```bash

lucidchaind genesis validate

```

Reset and start the node if validation is good

```bash
lucidchaind comet unsafe-reset-all
lucidchaind start

```
**Open New Terminal and run these commands one by one**

Check that the validator is running an ML-DSA consensus key

```bash

lucidchaind query staking validators -o json | jq '.validators[].consensus_pubkey'   # type should be /cosmos.crypto.mldsa65.PubKey

```

Check that no proof verifiers are registered (expected for now)

```bash

lucidchaind q proofs verifiers

```
Get the validator's address

```bash

lucidchaind keys show qr_validator --address

```

Check the validator's balance

```bash

lucidchaind query bank balances <address>

```
Get to know the chain

```bash

lucidchaind version

lucidchaind q upgrade module-versions

lucidchaind query auth module-account gov

lucidchaind query consensus comet block-latest

lucidchaind query bank total

lucidchaind query sidechain -h

lucidchaind tx sidechain -h

lucidchaind tx sidechain register-sidechain -h


```


**How to register a sidechain**

Only `notarized` and `attested` tiers are usable for now. The `proven` tier depends on zero-knowledge proofs, which are disabled until a quantum-resistant verifier exists.

**create signer keys**

Each key needs its own fresh mnemonic.

```bash

lucidchaind keys add qr_sidechainkey1 --key-type ml_dsa_65

lucidchaind keys add qr_sidechainkey2 --key-type ml_dsa_65

lucidchaind keys add qr_sidechainkey3 --key-type ml_dsa_65

```
Write down the mnemonics in order to recover the keys just in case they get deleted by mistake. To recover a key use `lucidchaind keys add <name> --recover --key-type ml_dsa_65`.

**display the public keys**

Each public key is a JSON object with `@type` `/cosmos.crypto.mldsa65.PubKey` and a base64 `key` of about 2.6 KB (1952 bytes), so use command substitution instead of pasting.

```bash

lucidchaind keys show qr_sidechainkey1 --pubkey

lucidchaind keys show qr_sidechainkey2 --pubkey

lucidchaind keys show qr_sidechainkey3 --pubkey

```

Then register, filling in the signer keys

```bash

lucidchaind tx sidechain register-sidechain \
  hospitality-platform-01 "Hospitality Platform" notarized 1000000stake \
  --signer-keys "$(lucidchaind keys show qr_sidechainkey1 --pubkey)" \
  --signer-keys "$(lucidchaind keys show qr_sidechainkey2 --pubkey)" \
  --signer-keys "$(lucidchaind keys show qr_sidechainkey3 --pubkey)" \
  --signature-threshold 2 \
  --checkpoint-interval-seconds 300 \
  --metadata-uri https://example.org/meta.json \
  --from qr_validator --chain-id my-testnet-1 --gas auto --gas-adjustment 1.5
```

You should see a transaction preview with three signer keys of type `/cosmos.crypto.mldsa65.PubKey` (long base64 values), then:

```
confirm transaction before signing and broadcasting [y/N]: y

code: 0
txhash: <hash>
```

Run  

```bash
lucidchaind q tx <txhash> # will show information about the registration

lucidchaind query sidechain sidechains

```

will show something similar to this  
  
```
pagination:
  total: "1"
sidechains:
- activated_at: "2026-10-09T10:39:29.496697798Z"
  bond:
    amount: "1000000"
    denom: stake
  checkpoint_interval_seconds: "300"
  id: hospitality-platform-01
  metadata_uri: https://example.org/meta.json
  name: Hospitality Platform
  operator: cosmos1...
  registered_at: "2026-10-09T10:39:29.496697798Z"
  signature_threshold: 2
  signer_keys:
  - type: /cosmos.crypto.mldsa65.PubKey
    value: <base64 ML-DSA-65 public key>
  - type: /cosmos.crypto.mldsa65.PubKey
    value: <base64 ML-DSA-65 public key>
  - type: /cosmos.crypto.mldsa65.PubKey
    value: <base64 ML-DSA-65 public key>
  signer_set_version: "1"
  status: SIDECHAIN_STATUS_ACTIVE
  tier: ASSURANCE_TIER_NOTARIZED
```


**How to work with checkpoints**

Syntax:  

lucidchaind tx checkpoint submit-checkpoint [sidechain-id] [lc-sequence] [state-root] [previous-checkpoint-hash] [record-count] [data-pointer] [signer-set-version] [flags]

# IMPORTANT
Checkpoints are submitted **without a ZK anchor**. The `--zk-anchor` flag is optional and nullable, and the old anchor scheme (`ZK_HASH_SCHEME_MIMC_BN254`) was built for the Groth16/PLONK proofs that have been removed. Do not pass `--zk-anchor` until a quantum-resistant anchor scheme is added.

**Example**

Checkpoint signatures are ML-DSA-65 signatures (about 3.3 KB each, roughly 4.4 KB in base64). The chain accepts at most 7 signatures and 24000 signature bytes per checkpoint, so 7 ML-DSA-65 signatures just fit.

First build the signatures. Make sure you have the following folder structure in your home directory ./tools/qr/signcheckpoint/
 and the binary signcheckpoint is in it ("chmod +x signcheckpoint"), then run the code below.
 To build the binary, run this from the repository folder:

> `tools/qr/signcheckpoint` signs with ML-DSA-65. `lucidchaind keys export --unarmored-hex --unsafe` returns the 4032-byte packed private key (8064 hex characters), which the tool reads directly. Each signature is checked against its own public key before it is printed. Anchor flags are omitted on purpose.

cd go/src/github.com/lucidpay/lucidchain

mkdir -p ~/tools/qr/signcheckpoint ~/tools/qr/mldsasign
go build -o ~/tools/qr/signcheckpoint/signcheckpoint ./tools/qr/signcheckpoint
go build -o ~/tools/qr/mldsasign/mldsasign ./tools/qr/mldsasign

export STATE_ROOT=d841f966f8bf17d49335f4b134c2178fd5aca8244d46b8d5f9a5470338095b7b
export STATE_ROOT_B64=$(echo $STATE_ROOT | xxd -r -p | base64 -w0)

K1=$(lucidchaind keys export qr_sidechainkey1 --unarmored-hex --unsafe)
K2=$(lucidchaind keys export qr_sidechainkey2 --unarmored-hex --unsafe)

$HOME/tools/qr/signcheckpoint/signcheckpoint \
  --chain-id my-testnet-1 \
  --sidechain-id hospitality-platform-01 \
  --lc-sequence 1 \
  --state-root "$STATE_ROOT" \
  --previous-hash "" \
  --record-count 100 \
  --data-pointer "ipfs://test-checkpoint-1" \
  --signer-set-version 1 \
  --key 0="$K1" --key 1="$K2"

unset K1 K2

```

Take the signatures from the tool output and add them to submit-checkpoint

```bash

lucidchaind tx checkpoint submit-checkpoint \
  hospitality-platform-01 1 $STATE_ROOT_B64 "" 100 "ipfs://test-checkpoint-1" 1 \
  --signatures '{"signature":"<base64 ML-DSA-65 signature from signer 0>","signer_index":0}' \
  --signatures '{"signature":"<base64 ML-DSA-65 signature from signer 1>","signer_index":1}' \
  --from qr_validator --chain-id my-testnet-1 \
  --gas auto --gas-adjustment 1.5 --gas-prices 0.025stake -y

```

you should get an output similar to this
  
```
code: 0
txhash: <hash>
```

Queries  

```bash

lucidchaind q checkpoint params

lucidchaind q checkpoint checkpoint hospitality-platform-01 1

lucidchaind q checkpoint latest-checkpoint hospitality-platform-01

```

this should output something similar to this  

```
checkpoint:
  checkpoint_hash: 0VvDymCJ4DU02QeRm3Z423D1g2ICxKetgCLr1xq1zhs=
  data_pointer: ipfs://test-checkpoint-1
  finalized_at: "2026-10-09T06:17:54.235499177Z"
  finalized_height: "1239"
  lc_sequence: "1"
  previous_checkpoint_hash: ""
  record_count: "100"
  sidechain_id: hospitality-platform-01
  signatures_digest: <digest>
  signer_bitmap: Aw==
  signer_set_version: "1"
  state_root: <state root>
```

```bash

lucidchaind q checkpoint checkpoints hospitality-platform-01 --page-limit 10 --page-reverse

lucidchaind query checkpoint -h

```

**Zero-knowledge proofs (x/proofs): currently disabled**

The following were removed from this guide because they are not quantum-resistant:

- `zkgeo setup` for Groth16 and PLONK over BN254, and the `keys-geo-*` key directories
- `tools/gen-vk/add-verifier.sh` and the `groth16-bn254` / `plonk-bn254` verifiers in genesis
- the provable-checkpoint example (`records.json`, `build-batch.py`, `batchroot`, `submit-checkpoint-from-batch.sh`)
- `prove-and-submit-geo.sh` for both schemes, and the `proofs-for-checkpoint` queries

Why: Groth16 and PLONK over BN254 rely on pairing and elliptic-curve assumptions that a quantum computer breaks, so proofs could be forged. Keep `app_state.proofs.verifiers` empty.

To bring proofs back, the plan is:

1. Choose a hash-based proof system (STARK/FRI-style) and a hash scheme suited to it, and add a matching ZK anchor scheme.
2. Register its verifier through genesis, with a measured gas cost.
3. Raise `max_proof_bytes` (currently 8192), because hash-based proofs are much larger than Groth16/PLONK proofs.
4. Only then enable the `proven` assurance tier.


**How to work with attestor**

first create the needed keys (each with a fresh mnemonic)

```bash

lucidchaind keys add qr_attestor-operator --key-type ml_dsa_65
lucidchaind keys add qr_attestorkey1 --key-type ml_dsa_65
lucidchaind keys add qr_attestorkey2 --key-type ml_dsa_65

lucidchaind tx bank send qr_validator $(lucidchaind keys show qr_attestor-operator -a) 5000000stake \
  --chain-id my-testnet-1 --gas auto --gas-adjustment 1.5 --yes

lucidchaind keys show qr_attestor-operator --address
lucidchaind keys show qr_attestorkey1 --address
lucidchaind keys show qr_attestorkey2 --address
lucidchaind keys show qr_attestor-operator --pubkey
lucidchaind keys show qr_attestorkey1 --pubkey
lucidchaind keys show qr_attestorkey2 --pubkey

```

Register an attestor

The operator's first transaction writes its ML-DSA public key to state, so `--gas auto` is required.

```bash
lucidchaind tx attestor register-attestor \
  --id kyc-attestor-01 --name "KYC Attestor 01" --authorized-schema-ids kyc-v1 \
  --signer-keys "$(lucidchaind keys show qr_attestorkey1 --pubkey)" \
  --signature-threshold 1 --bond-amount 1000000 \
  --credential-uri https://example.org/attestor.json \
  --from qr_attestor-operator --chain-id my-testnet-1 --gas auto --gas-adjustment 1.5 --gas-prices 0stake --yes

#check
lucidchaind q attestor attestor kyc-attestor-01

```

Activate attestor


```bash
cat > ~/tmp_workarea_qr/json/activate-attestor.json <<'EOF'
{
  "messages": [{
    "@type": "/lucidchain.attestor.v1.MsgActivateAttestor",
    "authority": "cosmos10d07y265gmmuvt4z0w9aw880jnsr700j6zn9kn",
    "id": "kyc-attestor-01"
  }],
  "metadata": "ipfs://test",
  "deposit": "10000000stake",
  "title": "Activate kyc-attestor-01",
  "summary": "Activate kyc-attestor-01"
}
EOF

lucidchaind tx gov submit-proposal ~/tmp_workarea_qr/json/activate-attestor.json --from qr_validator \
  --chain-id my-testnet-1 --gas auto --gas-adjustment 1.5 --gas-prices 0stake --yes
sleep 7
lucidchaind q gov proposals -o json | jq '.proposals[-1] | {id, status}'
PID=$(lucidchaind q gov proposals -o json | jq -r '.proposals[-1].id')
lucidchaind tx gov vote "$PID" yes --from qr_validator \
  --chain-id my-testnet-1 --gas auto --gas-adjustment 1.5 --yes  # proposal id should be 1 

```

To validate

```bash

lucidchaind q gov proposal "$PID" -o json | jq '.proposal.messages[0]."@type", .proposal.title'
lucidchaind q attestor attestor kyc-attestor-01 | grep status


```


`submit-attestation.sh` expects schema `kyc-v1` and an ACTIVE attestor `acme` whose signer key is the `qr_validator` key. Creating the schema and activating the attestor are governance-only, so set them up first.

> `tools/qr/signbytes/submit-attestation.sh` signs with ML-DSA-65 through the `mldsasign` helper (built above) and honors `$QR_HOME`. If the chain rejects the signatures file, see the signature-encoding note in the tool's output (hex vs base64).

**1. create the schema (governance)**

Skip this step if you added `kyc-v1` to genesis above (Add attestors schema). Otherwise the proposal fails when it executes, with `"kyc-v1": attestation schema already exists`.

```bash
cat > ~/tmp_workarea_qr/json/schema.json <<'EOF'
{
  "messages": [{
    "@type": "/lucidchain.attestor.v1.MsgCreateAttestationSchema",
    "authority": "cosmos10d07y265gmmuvt4z0w9aw880jnsr700j6zn9kn",
    "schema": {
      "id": "kyc-v1",
      "title": "Revenue report",
      "domain": "hospitality",
      "claim_description": "Reported revenue matches the source data",
      "exclusions": "Does not verify transaction authenticity",
      "required_data_sources": ["pms"],
      "validity_period_seconds": "31536000",
      "dispute_window_seconds": "3600",
      "active": true
    }
  }],
  "metadata": "ipfs://test",
  "deposit": "10000000stake",
  "title": "Create kyc-v1 schema",
  "summary": "Create kyc-v1 schema"
}
EOF
lucidchaind tx gov submit-proposal ~/tmp_workarea_qr/json/schema.json --from qr_validator \
    --chain-id my-testnet-1 --gas auto --gas-adjustment 1.5 --gas-prices 0stake --yes
lucidchaind tx gov vote <proposal-id> yes --from qr_validator --chain-id my-testnet-1 --gas-prices 0stake --yes  # proposal ID should be 2 if this is your first proposal


```

**2. register the attestor, with an ML-DSA key as its signer**

```bash
lucidchaind tx attestor register-attestor \
  --id acme --name Acme --authorized-schema-ids kyc-v1 \
  --signer-keys "$(lucidchaind keys show qr_attestorkey2 --pubkey)" \
  --signature-threshold 1 --bond-amount 1000000 \
  --credential-uri https://example.org/acme.json \
  --from qr_attestor-operator --chain-id my-testnet-1 --gas auto --gas-adjustment 1.5 --gas-prices 0stake --yes


lucidchaind query attestor attestor acme   # status should be ATTESTOR_STATUS_PENDING

```

3. activate it (governance): same proposal format as step 1, with this message
   {"@type": "/lucidchain.attestor.v1.MsgActivateAttestor",
    "authority": "cosmos10d07y265gmmuvt4z0w9aw880jnsr700j6zn9kn", "id": "acme"}

```bash

cat > ~/tmp_workarea_qr/json/approve-attestor.json <<'EOF'
{
  "messages": [{"@type": "/lucidchain.attestor.v1.MsgActivateAttestor",
    "authority": "cosmos10d07y265gmmuvt4z0w9aw880jnsr700j6zn9kn", "id": "acme"}
    ],
  "metadata": "ipfs://test",
  "deposit": "10000000stake",
  "title": "Approve Attestor",
  "summary": "Proposal to approve attestor"
}
EOF
lucidchaind tx gov submit-proposal ~/tmp_workarea_qr/json/approve-attestor.json --from qr_validator \
    --chain-id my-testnet-1 --gas auto --gas-adjustment 1.5 --gas-prices 0stake --yes
lucidchaind tx gov vote <proposal-id> yes --from qr_validator --chain-id my-testnet-1 --gas-prices 0stake --yes  # proposal ID should be 3 here 

```

```bash
#give it a couple minutes then check

lucidchaind query attestor attestor acme   # status should be ATTESTOR_STATUS_ACTIVE

#if it doesn't activate make sure your proposal-id is correct. You can list the proposals using
lucidchaind query gov proposals

```

The script is `tools/qr/signbytes/submit-attestation.sh`. Copy it to the folder you work in and make it executable with `chmod +x submit-attestation.sh`.
  
First step is to create a claim  

```bash

printf '%s' '{"revenue":"1250000","period":"2026-09"}' > ~/tmp_workarea_qr/json/claim.json

#run the shell script. shell script can be found under tools/qr/signbytes
chmod +x tools/qr/signbytes/submit-attestation.sh
#dry run
bash -n tools/qr/signbytes/submit-attestation.sh
NO_SUBMIT=1 CLAIM_FILE=~/tmp_workarea_qr/json/claim.json tools/qr/signbytes/submit-attestation.sh
#actual run
CLAIM_FILE=~/tmp_workarea_qr/json/claim.json tools/qr/signbytes/submit-attestation.sh

```
you should get something like this  

```
Checkpoint: sidechain=hospitality-platform-01 seq=1 hash=51e2aa1150901af2f76b4469c4c1ce4db21053c4cceeaecc49c49ed183dbd704
Signer key matches the attestor's registered key.
Wrote sigs.json
gas estimate: 174748
code: 0
txhash: <hash>
```

**now run**

```bash

lucidchaind query attestor checkpoint-attestations hospitality-platform-01 1

```

you should get something similar to this  

```
attestations:
- attestor_id: acme
  checkpoint_hash: UeKqEVCQGvL3a0RpxMHOTbIQU8TM7q7MScSe0YPb1wQ=
  checkpoint_sequence: "1"
  claim_payload: eyJyZXZlbnVlIjoiMTI1MDAwMCIsInBlcmlvZCI6IjIwMjYtMDkifQ==
  expires_at: "2027-10-09T15:20:14.54848753Z"
  id: 30dd87b708f6bbd7a6e6c6527aceec4340cce248ee583fdff0b8455eb03aaafc
  issued_at: "2026-10-09T15:20:14.54848753Z"
  issued_height: "383"
  schema_id: kyc-v1
  sidechain_id: hospitality-platform-01
  signatures_digest: XQLZ1BN9olL0dEc6OG8k1PCnciJyFtt/Txfs4b4Y/GU=
  signer_bitmap: AQ==
  signer_set_version: "1"
  status: ATTESTATION_STATUS_ACTIVE
pagination:
  total: "1"
```


**dispute and attestation**

Create a challenger 

```bash

lucidchaind keys add qr_challenger --key-type ml_dsa_65
lucidchaind keys show qr_challenger -a      # copy the address
#fund the wallet
lucidchaind tx bank send qr_validator <challenger-address> 5000000stake \
  --chain-id my-testnet-1 --gas auto --gas-adjustment 1.5 --gas-prices 0stake --yes

#pull the attestation id for the next command
lucidchaind query attestor checkpoint-attestations hospitality-platform-01 1 -o json | jq -r '.attestations[0].id'

#raise a dispute (the challenger's first transaction writes its public key to state, so --gas auto is required)
lucidchaind tx attestor raise-dispute \
  <attestation id> \
  ipfs://test-evidence 100000 \
  --from qr_challenger --chain-id my-testnet-1 \
  --gas auto --gas-adjustment 1.5 --gas-prices 0stake --yes
```

**resolve it**


create proposal.json with the json data below. Get dispute_id from the raise dispute tx  

```bash

#again get the attestation ID
lucidchaind query attestor checkpoint-attestations hospitality-platform-01 1 -o json | jq -r '.attestations[0].id'

#add the attestation id here and get the dispute ID
lucidchaind q txs --query "dispute_raised.attestation_id='<attestation id>'" -o json \
  | jq -r '.txs[0].events[] | select(.type=="dispute_raised") | .attributes[] | select(.key=="dispute_id") | .value'

```

Adjust in the below JSON with the new dispute id

```bash
cat > ~/tmp_workarea_qr/json/resolution-proposal.json <<'EOF'
{
  "messages": [{
    "@type": "/lucidchain.attestor.v1.MsgResolveDispute",
    "authority": "cosmos10d07y265gmmuvt4z0w9aw880jnsr700j6zn9kn",
    "dispute_id": "<dispute id>",
    "upheld": true,
    "rationale_uri": "ipfs://test-rationale"
  }],
  "metadata": "ipfs://test",
  "deposit": "10000000stake",
  "title": "Resolve dispute",
  "summary": "Resolve the test dispute"
}
EOF

```

```bash

lucidchaind tx gov submit-proposal ~/tmp_workarea_qr/json/resolution-proposal.json --from qr_validator \
  --chain-id my-testnet-1 --gas auto --gas-adjustment 1.5 --gas-prices 0stake --yes

sleep 7   # long enough for inclusion in a block

PID=$(lucidchaind q gov proposals -o json | jq -r '.proposals[-1].id')
lucidchaind q gov proposal "$PID" -o json | jq '.proposal | {id, status, voting_end_time}'
lucidchaind tx gov vote "$PID" yes --from qr_validator --chain-id my-testnet-1 \
  --gas auto --gas-adjustment 1.5 --gas-prices 0stake --yes


#wait a minute or two then check the status of the proposal

PID=$(lucidchaind q gov proposals -o json | jq -r '.proposals[-1].id')
lucidchaind q gov proposal "$PID" -o json | jq '.proposal | {status, failed_reason, voting_end_time}'


```

lucidchaind query attestor -h

**How to suspend an attestor**

```bash
cat > ~/tmp_workarea_qr/json/suspend-attestor-proposal.json <<'EOF'
{
  "messages": [{
    "@type": "/lucidchain.attestor.v1.MsgSuspendAttestor",
    "authority": "cosmos10d07y265gmmuvt4z0w9aw880jnsr700j6zn9kn",
    "id": "kyc-attestor-01",
    "reason": "dispute <dispute id>"
  }],
  "metadata": "ipfs://test",
  "deposit": "10000000stake",
  "title": "Suspend kyc-attestor-01",
  "summary": "Suspend kyc-attestor-01"
}
EOF

lucidchaind tx gov submit-proposal ~/tmp_workarea_qr/json/suspend-attestor-proposal.json --from qr_validator \
  --chain-id my-testnet-1 --gas auto --gas-adjustment 1.5 --gas-prices 0stake --yes

sleep 7   # long enough for inclusion in a block

PID=$(lucidchaind q gov proposals -o json | jq -r '.proposals[-1].id')
lucidchaind q gov proposal "$PID" -o json | jq '.proposal | {id, status, voting_end_time}'
lucidchaind tx gov vote "$PID" yes --from qr_validator --chain-id my-testnet-1 \
  --gas auto --gas-adjustment 1.5 --gas-prices 0stake --yes

#to check
lucidchaind q attestor attestor kyc-attestor-01 | grep status

```

**How to reinstate a suspended attestor**

Only a SUSPENDED attestor can be reinstated. It becomes ACTIVE again.

```bash
cat > ~/tmp_workarea_qr/json/reinstate-attestor-proposal.json <<'EOF'
{
  "messages": [{
    "@type": "/lucidchain.attestor.v1.MsgReinstateAttestor",
    "authority": "cosmos10d07y265gmmuvt4z0w9aw880jnsr700j6zn9kn",
    "id": "kyc-attestor-01"
  }],
  "metadata": "ipfs://test",
  "deposit": "10000000stake",
  "title": "Reinstate kyc-attestor-01",
  "summary": "Reinstate kyc-attestor-01"
}
EOF

lucidchaind tx gov submit-proposal ~/tmp_workarea_qr/json/reinstate-attestor-proposal.json --from qr_validator \
  --chain-id my-testnet-1 --gas auto --gas-adjustment 1.5 --gas-prices 0stake --yes

sleep 7   # long enough for inclusion in a block

PID=$(lucidchaind q gov proposals -o json | jq -r '.proposals[-1].id')
lucidchaind tx gov vote "$PID" yes --from qr_validator --chain-id my-testnet-1 \
  --gas auto --gas-adjustment 1.5 --gas-prices 0stake --yes

#to check (after the voting period)
lucidchaind q attestor attestor kyc-attestor-01 | grep status

```


**How to suspend a sidechain**

You will need to create a proposal suspend.json

```bash
cat > ~/tmp_workarea_qr/json/suspension-proposal.json <<'EOF'
{
  "messages": [
    {
      "@type": "/lucidchain.sidechain.v1.MsgSuspendSidechain",
      "authority": "cosmos10d07y265gmmuvt4z0w9aw880jnsr700j6zn9kn",
      "id": "hospitality-platform-01"
    }
  ],
  "metadata": "",
  "deposit": "10000000stake",
  "title": "Suspend hospitality-platform-01",
  "summary": "Temporarily halt checkpoint submission for this sidechain."
}
EOF

lucidchaind tx gov submit-proposal ~/tmp_workarea_qr/json/suspension-proposal.json --from qr_validator --chain-id my-testnet-1 --gas auto --gas-adjustment 1.5 -y
sleep 7
PID=$(lucidchaind q gov proposals -o json | jq -r '.proposals[-1].id')
lucidchaind tx gov vote "$PID" yes --from qr_validator --chain-id my-testnet-1 --gas auto --gas-adjustment 1.5 -y

#wait for a minute or two and then query to verify
lucidchaind query sidechain sidechain hospitality-platform-01

```



**How to slash a sidechain**
You will need to create a proposal slash.json

```bash
cat > ~/tmp_workarea_qr/json/slash-proposal.json <<'EOF'
{
  "messages": [
    {
      "@type": "/lucidchain.sidechain.v1.MsgSlashSidechain",
      "authority": "cosmos10d07y265gmmuvt4z0w9aw880jnsr700j6zn9kn",
      "id": "hospitality-platform-01",
      "slash_fraction": "0.100000000000000000",
      "reason": "conflicting checkpoints signed at sequence 42"
    }
  ],
  "metadata": "",
  "deposit": "10000000stake",
  "title": "Slash hospitality-platform-01",
  "summary": "Burn 10% of the bond for signing conflicting checkpoints."
}
EOF


# This will make submission and voting within the time frame
lucidchaind tx gov submit-proposal ~/tmp_workarea_qr/json/slash-proposal.json --from qr_validator --chain-id my-testnet-1 --gas auto --gas-adjustment 1.5 -y
sleep 7
PID=$(lucidchaind q gov proposals -o json | jq -r '.proposals[-1].id')
lucidchaind tx gov vote "$PID" yes --from qr_validator --chain-id my-testnet-1 --gas auto --gas-adjustment 1.5 -y

#wait for a minute or two then query to verify
lucidchaind query sidechain sidechain hospitality-platform-01

#you should see
#  bond:
#    amount: "900000"
#    denom: stake
#...
#  status: SIDECHAIN_STATUS_SLASHED

PID=$(lucidchaind q gov proposals -o json | jq -r '.proposals[-1].id')
lucidchaind query gov proposal "$PID"


```

**How to exit a sidechain**

No proposals or voting is needed for this. There is a cool-off period of 14 days, and the bond value returns to the sidechain owner

```bash
lucidchaind tx sidechain exit hospitality-platform-01 --from qr_validator --chain-id my-testnet-1 --gas auto --gas-adjustment 1.5 -y

```
