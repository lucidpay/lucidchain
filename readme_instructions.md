# Jump start your node

Start by building your work area in your home directory after getting the source code

```bash

mkdir tmp_workarea
cd tmp_workarea
mkdir keys-geo-groth16-bn254
mkdir keys-geo-plonk-bn254
mkdir json
mkdir out

#build the bins for both Groth16 and PLONK
cd ~/go/src/github.com/lucidpay/lucidchain
go run ./cmd/zkgeo setup -scheme plonk -capacity 8 -records-depth 5 -area-depth 4 -out ~/tmp_workarea/keys-geo-plonk-bn254
go run ./cmd/zkgeo setup -scheme groth16 -capacity 8 -records-depth 5 -area-depth 4 -out ~/tmp_workarea/keys-geo-groth16-bn254
cd 

```

Initialize the blockchain

```bash

lucidchaind init dev-test-node --chain-id my-testnet-1

nano $HOME/.lucidchain/config/app.toml

```

**modify using nano**

minimum-gas-prices = "0ucheck" 

**Ctrl+x and y to save**

```bash

lucidchaind keys add validator

lucidchaind genesis add-genesis-account validator 100000000stake,1000000001000ucheck

lucidchaind genesis gentx validator 1000000stake --chain-id my-testnet-1

lucidchaind genesis collect-gentxs

```

**Seting up the genesis**


Enable secp256k1 in dev, for both sidechains and attestors. Do not do this for production, to keep the node quantum-resistant.

A fresh `lucidchaind init` also sets `auto_activate` to `false`, which leaves a new sidechain `PENDING` so every checkpoint is rejected. Set it to `true` for dev.


make sure you are in your home directory

```bash

G=~/.lucidchain/config/genesis.json
jq '.app_state.sidechain.params.allowed_pubkey_type_urls += ["/cosmos.crypto.secp256k1.PubKey"]
  | .app_state.attestor.params.allowed_pubkey_type_urls += ["/cosmos.crypto.secp256k1.PubKey"]
  | .app_state.sidechain.params.auto_activate = true' "$G" > /tmp/g.json && mv /tmp/g.json "$G"

```

now add the verifiers

```bash

cd ~/go/src/github.com/lucidpay/lucidchain
chmod +x ./tools/gen-vk/add-verifier.sh
#./tools/gen-vk/add-verifier.sh                       # defaults: ~/.lucidchain/config/genesis.json, ./vk.bin
./tools/gen-vk/add-verifier.sh ~/.lucidchain/config/genesis.json ~/tmp_workarea/keys-geo-plonk-bn254/vk.bin plonk-bn254 500000
./tools/gen-vk/add-verifier.sh ~/.lucidchain/config/genesis.json ~/tmp_workarea/keys-geo-groth16-bn254/vk.bin groth16-bn254 500000

```

Note: with the default genesis the governance voting period is 48 hours, so each proposal below takes two days to pass. For a dev chain, shorten it before the first start:

```bash
#go back to home directory
cd

G=~/.lucidchain/config/genesis.json
`jq '.app_state.gov.params.voting_period = "60s" | .app_state.gov.params.expedited_voting_period = "30s"' "$G" > /tmp/g.json && mv /tmp/g.json "$G"`

#validate, reset and start the test chain
lucidchaind genesis validate

```
Reset and start the node if validation is good

```bash
lucidchaind comet unsafe-reset-all
lucidchaind start

```
**Open New Terminal and run these commands one by one**

Check that the verifiers have been registered correctly 

```bash

lucidchaind q proofs verifiers

```
Get the validator's address

```bash

lucidchaind keys show validator --address

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


**create signer keys**

```bash

lucidchaind keys add sidechainkey1

lucidchaind keys add sidechainkey2

lucidchaind keys add sidechainkey3

```
Write down the mnemonics in order to recover the keys just in case they get deleted by mistake

**display the public keys**

```bash

lucidchaind keys show sidechainkey1 --pubkey

lucidchaind keys show sidechainkey2 --pubkey

lucidchaind keys show sidechainkey3 --pubkey

```

Then register filling in the signer keys

```bash

lucidchaind tx sidechain register-sidechain \
  hospitality-platform-01 "Hospitality Platform" notarized 1000000stake \
  --signer-keys '{"@type":"/cosmos.crypto.secp256k1.PubKey","key":"<base64>"}' \
  --signer-keys '{"@type":"/cosmos.crypto.secp256k1.PubKey","key":"<base64>"}' \
  --signer-keys '{"@type":"/cosmos.crypto.secp256k1.PubKey","key":"<base64>"}' \
  --signature-threshold 2 \
  --checkpoint-interval-seconds 300 \
  --metadata-uri https://example.org/meta.json \
  --from validator --chain-id my-testnet-1 --gas auto --gas-adjustment 1.5
```

EXAMPLE  
  
lucidchaind tx sidechain register-sidechain \
  hospitality-platform-01 "Hospitality Platform" notarized 1000000stake \
  --signer-keys '{"@type":"/cosmos.crypto.secp256k1.PubKey","key":"Aj3S0dAItEDqOpHm7lw2eU79vgigg29etjNMO0i+BgZF"}' \
  --signer-keys '{"@type":"/cosmos.crypto.secp256k1.PubKey","key":"AsxltdpDXisuwLfiuSC0S5MzJ/EN1ATdzULinSEZZKjM"}' \
  --signer-keys '{"@type":"/cosmos.crypto.secp256k1.PubKey","key":"Am9As0veC6DyLha69fjDFbeO7eUU78GQ7La0d474+T0Q"}' \
  --signature-threshold 2 \
  --checkpoint-interval-seconds 300 \
  --metadata-uri https://example.org/meta.json \
  --from validator --chain-id my-testnet-1 --gas auto --gas-adjustment 1.5

you should see something similar to this  

gas estimate: 121149  
auth_info:  
  fee:  
    amount: []  
    gas_limit: "121149"  
    granter: ""  
    payer: ""  
  signer_infos: []  
  tip: null  
body:  
  extension_options: []  
  memo: ""  
  messages:  
  - '@type': /lucidchain.sidechain.v1.MsgRegisterSidechain  
    bond:  
      amount: "1000000"  
      denom: stake  
    checkpoint_interval_seconds: "300"  
    id: hospitality-platform-01  
    metadata_uri: https://example.org/meta.json  
    name: Hospitality Platform  
    operator: cosmos18yem5p78w8efrx6dvc45dufjntvq0j6kng2ce0  
    signature_threshold: 2  
    signer_keys:  
    - '@type': /cosmos.crypto.secp256k1.PubKey  
      key: Aj3S0dAItEDqOpHm7lw2eU79vgigg29etjNMO0i+BgZF  
    - '@type': /cosmos.crypto.secp256k1.PubKey  
      key: AsxltdpDXisuwLfiuSC0S5MzJ/EN1ATdzULinSEZZKjM  
    - '@type': /cosmos.crypto.secp256k1.PubKey  
      key: Am9As0veC6DyLha69fjDFbeO7eUU78GQ7La0d474+T0Q  
    tier: ASSURANCE_TIER_NOTARIZED  
  non_critical_extension_options: []  
  timeout_height: "0"  
  timeout_timestamp: null  
  unordered: false  
signatures: []  
  
confirm transaction before signing and broadcasting [y/N]: y  
  
code: 0  
codespace: ""  
data: ""  
events: []  
gas_used: "0"  
gas_wanted: "0"  
height: "0"  
info: ""  
logs: []  
raw_log: ""  
timestamp: ""  
tx: null  
txhash: 3AA09EE67BCF0960D6F30FDEA10BE40365EE5C2B076FA427CB690ED2245320FA  
  
Run  
  
```bash
lucidchaind q tx 3AA09EE67BCF0960D6F30FDEA10BE40365EE5C2B076FA427CB690ED2245320FA # will show information about the registration

lucidchaind query sidechain sidechains

```

will show something similar to this  
  
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
  operator: cosmos18yem5p78w8efrx6dvc45dufjntvq0j6kng2ce0  
  registered_at: "2026-10-09T10:39:29.496697798Z"  
  signature_threshold: 2  
  signer_keys:  
  - type: /cosmos.crypto.secp256k1.PubKey  
    value: Aj3S0dAItEDqOpHm7lw2eU79vgigg29etjNMO0i+BgZF  
  - type: /cosmos.crypto.secp256k1.PubKey  
    value: AsxltdpDXisuwLfiuSC0S5MzJ/EN1ATdzULinSEZZKjM  
  - type: /cosmos.crypto.secp256k1.PubKey  
    value: Am9As0veC6DyLha69fjDFbeO7eUU78GQ7La0d474+T0Q  
  signer_set_version: "1"  
  status: SIDECHAIN_STATUS_ACTIVE  
  tier: ASSURANCE_TIER_NOTARIZED  


**How to work with checkpoints**

Syntax:  

lucidchaind tx checkpoint submit-checkpoint [sidechain-id] [lc-sequence] [state-root] [previous-checkpoint-hash] [record-count] [data-pointer] [signer-set-version] [flags]


# IMPORTAMT
These steps create a checkpoint with jusrt a place holder for ZK_Anchor and that cannot be proved using ZKP. Second example does

**Example 1**

```bash

#Syntax
lucidchaind tx checkpoint submit-checkpoint \
  <sidechain-id> <lc-sequence> <state-root> ... \
  --signatures '{"signer_index":0,"signature":"..."}' \
  --zk-anchor '{"scheme":"ZK_HASH_SCHEME_MIMC_BN254","records_root":"...","records_tree_depth":20}' \
  --from <submitter> --chain-id <chain-id>


```

Steps needed  
 first build a signature. make sure you have the following folder structure in your home directory ./tools/signcheckpoint/
 and the binary signcheckpoint is in it "chmod +x signcheckpoint" then run the code bellow
 to build the binary, run this from the repository folder:
  
```bash

go build -o ~/tools/signcheckpoint/signcheckpoint ./tools/signcheckpoint

chmod +x ./tools/zkgeo/submit-checkpoint-from-batch.sh

export STATE_ROOT=d841f966f8bf17d49335f4b134c2178fd5aca8244d46b8d5f9a5470338095b7b
export STATE_ROOT_B64=$(echo $STATE_ROOT | xxd -r -p | base64 -w0)
export ZK_ROOT=$(printf '%064x' 7)   # test value; use your real MiMC root for real proofs

K1=$(lucidchaind keys export sidechainkey1 --unarmored-hex --unsafe)
K2=$(lucidchaind keys export sidechainkey2 --unarmored-hex --unsafe)

$HOME/tools/signcheckpoint/signcheckpoint \
  --chain-id my-testnet-1 \
  --sidechain-id hospitality-platform-01 \
  --lc-sequence 1 \
  --state-root "$STATE_ROOT" \
  --previous-hash "" \
  --record-count 100 \
  --data-pointer "ipfs://test-checkpoint-1" \
  --signer-set-version 1 \
  --zk-records-root "$ZK_ROOT" \
  --zk-tree-depth 20 \
  --key 0="$K1" --key 1="$K2"


# take the signatures and add them to submit-checkpoint
export STATE_ROOT=d841f966f8bf17d49335f4b134c2178fd5aca8244d46b8d5f9a5470338095b7b
export STATE_ROOT_B64=$(echo $STATE_ROOT | xxd -r -p | base64 -w0)

#NOTE: --zk-anchor is optional and nullable. But here it demonstrates how to use it
lucidchaind tx checkpoint submit-checkpoint \
  hospitality-platform-01 1 $STATE_ROOT_B64 "" 100 "ipfs://test-checkpoint-1" 1 \
  --signatures '{"signature":"xWbozMDCDKXRAYugCVQXuWXlxeNdX4Gj814Nax4meeN1KURU1gxGTOflrIyX1Y/FI5sXvncru8ZlS3mxQmBQIQ==","signer_index":0}' \
  --signatures '{"signature":"+hXNHk0IKTP/4X/30iAv8VZjErcp4nlUJ1AZaxnI509SMeNFr7P64vRJTLMxABkkrwFXAc1c9nf8IdPOfBwAoA==","signer_index":1}' \
  --zk-anchor '{"records_root":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAc=","records_tree_depth":20,"scheme":"ZK_HASH_SCHEME_MIMC_BN254"}' \
  --from validator --chain-id my-testnet-1 \
  --gas auto --gas-adjustment 1.5 --gas-prices 0.025stake -y

```

you should get an output similar to this
  
gas estimate: 258096  
code: 0  
codespace: ""  
data: ""  
events: []  
gas_used: "0"  
gas_wanted: "0"  
height: "0"  
info: ""  
logs: []  
raw_log: ""  
timestamp: ""  
tx: null  
txhash: 058043461378E698B4168564CFBB30963EABB252FE1A2D2B81F3097D9D2F310F  


Queries  

```bash

lucidchaind q checkpoint params

lucidchaind q checkpoint checkpoint hospitality-platform-01 1

lucidchaind q checkpoint latest-checkpoint hospitality-platform-01

```

this should output something similar to this  

checkpoint:  
  checkpoint_hash: 0VvDymCJ4DU02QeRm3Z423D1g2ICxKetgCLr1xq1zhs=  
  data_pointer: ipfs://test-checkpoint-1  
  finalized_at: "2026-10-09T06:17:54.235499177Z"  
  finalized_height: "1239"  
  lc_sequence: "2"  
  previous_checkpoint_hash: KMsdoqcJeJIMp64OmPh8j7Ia5shHYU0rhfFmN3cTTBk=  
  record_count: "50"  
  sidechain_id: hospitality-platform-01  
  signatures_digest: pd1NgFzaJxzI6l+FtZ54OYWAHP0WzQvXHdtzfx1xqG8=  
  signer_bitmap: Aw==  
  signer_set_version: "1"  
  state_root: TDlx18fsUYPdjmEg7o1UKP2otukHED7xLvYB6ya1D1w=  

```bash

lucidchaind q checkpoint checkpoints hospitality-platform-01 --page-limit 10 --page-reverse

lucidchaind query checkpoint -h

```

**Example 2**
This checkpoint is provable and has proper geo coordinates

```bash
#first create random seeds
echo "0x00$(openssl rand -hex 31)"
echo "0x00$(openssl rand -hex 31)"
```

#Use the random seeds to create records.json file


[
  {"lon": 55.2700, "lat": 25.2000, "t": 1791817725, "salt": "0x00<62 hex digits>"}, <<== put random seeds here
  {"lon": 55.2705, "lat": 25.2002, "t": 1791817735, "salt": "0x00<62 hex digits>"}  <<== put random seeds here
]

```bash

cat > ~/tmp_workarea/json/records.json <<'EOF'
[
  {"lon": 55.2700, "lat": 25.2000, "t": 1791817725, "salt": "0x00d8824fe3771f23ad8dfb3c499da8e1a156521fdded097914abe8373e2f38c0"},
  {"lon": 55.2705, "lat": 25.2002, "t": 1791817735, "salt": "0x00bf8782b878d4ead438686a98258aa6b158fba629d71190c25c0686e0dacfd2"}
]
EOF

```
now build the batch
syntax: python3 build-batch.py --records records.json \
  --area-box <lon_min> <lat_min> <lon_max> <lat_max> \
  --out-dir ~/tmp_workarea/json

```bash

python3 tools/zkgeo/build-batch.py --records ~/tmp_workarea/json/records.json --area-box 55.269 25.199 55.272 25.201 --out-dir ~/tmp_workarea/json

```

Look at the anchor the batch produces

```bash

go run ./cmd/batchroot -batch ~/tmp_workarea/json/batch.json -records-depth 5

```

It should read something similar

record_count:       2
records_tree_depth: 5
records_root (hex): 1fa36e40d77dacac45dd651536e47cdc74c84aba7ef19fa88e6665c41958faf5
records_root (b64): H6NuQNd9rKxF3WUVNuR83HTISrp+8Z+ojmZlxBlY+vU=

```bash
export STATE_ROOT=d841f966f8bf17d49335f4b134c2178fd5aca8244d46b8d5f9a5470338095b7c
export K1=$(lucidchaind keys export sidechainkey1 --unarmored-hex --unsafe)
export K2=$(lucidchaind keys export sidechainkey2 --unarmored-hex --unsafe)

DRY_RUN=1 BATCH=~/tmp_workarea/json/batch.json tools/zkgeo/submit-checkpoint-from-batch.sh
BATCH=~/tmp_workarea/json/batch.json tools/zkgeo/submit-checkpoint-from-batch.sh

```
#Use the sequence the script printed:

```bash
SEQ=<new sequence> SCHEME=groth16 BATCH=~/tmp_workarea/json/batch.json tools/zkgeo/prove-and-submit-geo.sh
unset K1 K2

```

The PLONK version

```bash
SEQ=<new sequence> SCHEME=plonk BATCH=~/tmp_workarea/json/batch.json tools/zkgeo/prove-and-submit-geo.sh

```
```bash
SEQ=2 SCHEME=plonk BATCH=~/tmp_workarea/json/batch.json tools/zkgeo/prove-and-submit-geo.sh

```

To check use

```bash

lucidchaind q proofs proofs-for-checkpoint --sidechain-id hospitality-platform-01 --checkpoint-sequence 2 -o json \
  | jq '.proofs[] | {proof_system_id, claim_id, verified_height}'
  
```


**How to work with attestor**
  
`submit-attestation.sh` expects schema `kyc-v1` and an ACTIVE attestor `acme` whose signer key is the `validator` key. Creating the schema and activating the attestor are governance-only, so set them up first.

**1. create the schema (governance)**

```bash
cat > ~/tmp_workarea/json/schema.json <<'EOF'
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
lucidchaind tx gov submit-proposal ~/tmp_workarea/json/schema.json --from validator \
    --chain-id my-testnet-1 --gas auto --gas-adjustment 1.5 --gas-prices 0stake --yes
lucidchaind tx gov vote <proposal-id> yes --from validator --chain-id my-testnet-1 --gas-prices 0stake --yes  # proposal ID should be 1 if this is your first proposal


```

**2. register the attestor, with the validator key as its signer**

```bash
lucidchaind tx attestor register-attestor \
  --id acme --name Acme --authorized-schema-ids kyc-v1 \
  --signer-keys "$(lucidchaind keys show validator --pubkey)" \
  --signature-threshold 1 --bond-amount 1000000 \
  --credential-uri https://example.org/acme.json \
  --from validator --chain-id my-testnet-1 --gas auto --gas-adjustment 1.5 --gas-prices 0stake --yes


lucidchaind query attestor attestor acme   # status should be ATTESTOR_STATUS_PENDING

```

3. activate it (governance): same proposal format as step 1, with this message
   {"@type": "/lucidchain.attestor.v1.MsgActivateAttestor",
    "authority": "cosmos10d07y265gmmuvt4z0w9aw880jnsr700j6zn9kn", "id": "acme"}

```bash

cat > ~/tmp_workarea/json/approve-attestor.json <<'EOF'
{
  "messages": [{"@type": "/lucidchain.attestor.v1.MsgActivateAttestor",
    "authority": "cosmos10d07y265gmmuvt4z0w9aw880jnsr700j6zn9kn", "id": "acme"}
    ],
  "metadata": "ipfs://test",
  "deposit": "10000000stake",
  "title": "Aprove Attestor",
  "summary": "Proposal to approve attestor"
}
EOF
lucidchaind tx gov submit-proposal ~/tmp_workarea/json/approve-attestor.json --from validator \
    --chain-id my-testnet-1 --gas auto --gas-adjustment 1.5 --gas-prices 0stake --yes
lucidchaind tx gov vote <proposal-id> yes --from validator --chain-id my-testnet-1 --gas-prices 0stake --yes  # proposal ID should be 2 here 

```

```bash
#give it a minute then check
lucidchaind query attestor attestor acme   # status should be ATTESTOR_STATUS_ACTIVE

```

The script is `tools/signbytes/submit-attestation.sh`. Copy it to the folder you work in and make it executable with `chmod +x submit-attestation.sh`.
  
First step is to create a claim  

```bash

printf '%s' '{"revenue":"1250000","period":"2026-09"}' > ~/tmp_workarea/json/claim.json

#run the shell script. shell script can be found under tools/signbytes
chmod +x tools/signbytes/submit-attestation.sh
CLAIM_FILE=~/tmp_workarea/json/claim.json tools/signbytes/submit-attestation.sh

```
you should get something like this  

Checkpoint: sidechain=hospitality-platform-01 seq=1 hash=51e2aa1150901af2f76b4469c4c1ce4db21053c4cceeaecc49c49ed183dbd704  
Signer key matches the attestor's registered key.  
Wrote sigs.json  
gas estimate: 174748  
code: 0  
codespace: ""  
data: ""  
events: []  
gas_used: "0"  
gas_wanted: "0"  
height: "0"  
nfo: ""  
logs: []  
raw_log: ""  
timestamp: ""  
tx: null  
txhash: 5D1C540C4E110B77D05FDE3E74EAC2FE9D2685B522C77AB7BBE6993E52928AEB  


**now run**

```bash

lucidchaind query attestor checkpoint-attestations hospitality-platform-01 1

```

you should get something similar to this  

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


**dispute and attestation**

Create a challenger 

```bash

lucidchaind keys add challenger
lucidchaind keys show challenger -a      # copy the address
#fund the wallet
lucidchaind tx bank send validator <challenger-address> 5000000stake \
  --chain-id my-testnet-1 --gas auto --gas-adjustment 1.5 --gas-prices 0stake --yes


#raise a dispute
lucidchaind tx attestor raise-dispute \
  30dd87b708f6bbd7a6e6c6527aceec4340cce248ee583fdff0b8455eb03aaafc \
  ipfs://test-evidence 100000 \
  --from challenger --chain-id my-testnet-1 \
  --gas auto --gas-adjustment 1.5 --gas-prices 0stake --yes
```

**resolve it**


create proposal.json with the json data bellow. Get dispute_id from the raise dispute tx  

```json
  {
    "messages": [{
      "@type": "/lucidchain.attestor.v1.MsgResolveDispute",
      "authority": "cosmos10d07y265gmmuvt4z0w9aw880jnsr700j6zn9kn",
      "dispute_id": "d754de88bd68083b99594458e9463d743fc911772fdf7a83298725e75de3b0c8",
      "upheld": true,
      "rationale_uri": "ipfs://test-rationale"
    }],
    "metadata": "ipfs://test",
    "deposit": "10000000stake",
    "title": "Resolve dispute",
    "summary": "Resolve the test dispute"
  }
```

```bash
#run
lucidchaind tx gov submit-proposal proposal.json --from validator \
    --chain-id my-testnet-1 --gas auto --gas-adjustment 1.5 --gas-prices 0stake --yes

#wait until block created and then run (the proposal passes after the voting period: 48 hours by default)
lucidchaind tx gov vote 1 yes --from validator \
    --chain-id my-testnet-1 --gas-prices 0stake --yes

#check
lucidchaind q tx <txID>


```

lucidchaind query attestor -h


**working with proofs**


Check if the verifiers are already registered  

```bash
lucidchaind q proofs verifier --proof-system-id groth16-bn254 --claim-id geofence.v1 -o json
lucidchaind q proofs verifier --proof-system-id plonk-bn254 --claim-id geofence.v1 -o json

```

**build a proof system**

Build the record file

```bash
cat > ~/tmp_workarea/json/records.json <<'EOF'
[
  {"lon": 55.2700, "lat": 25.2000, "t": 1791817725, "salt": "0x00d8824fe3771f23ad8dfb3c499da8e1a156521fdded097914abe8373e2f38c0"},
  {"lon": 55.2705, "lat": 25.2002, "t": 1791817735, "salt": "0x00bf8782b878d4ead438686a98258aa6b158fba629d71190c25c0686e0dacfd2"}
]
EOF

python3 tools/zkgeo/build-batch.py --records ~/tmp_workarea/json/records.json --area-box 55.269 25.199 55.272 25.201 --out-dir ~/tmp_workarea/json

chmod +x tools/zkgeo/prove-and-submit-geo.sh


BATCH_DIR=~/tmp_workarea/json
OUT_DIR=~/tmp_workarea/out
SCHEME=groth16 BATCH="$BATCH_DIR/batch.json" OUT="$OUT_DIR/out-groth16" tools/zkgeo/prove-and-submit-geo.sh
 
# for groth16 
SYSTEM=groth16 KEYS=~/tmp_workarea/keys-geo-groth16-bn254 tools/xkgeo/prove-and-submit-geo.sh
# for PLONK do not pass SYSTEM since it is the default
KEYS=$PWD/keys-geo-groth16 ./prove-and-submit.sh 

```

you should see something similar to this  

binding hi=lY3I4mLg8f3O1lyYbB0f4Q== lo=lgIZtZypaOPDIJGMYI9iUg==  
17:59:27 INF compiling circuit  
17:59:27 INF parsed circuit inputs nbPublic=2 nbSecret=1  
17:59:27 INF building constraint builder nbConstraints=1  
17:59:27 DBG constraint system solver done nbConstraints=1 took=0.021333  
17:59:27 DBG prover done acceleration=none backend=groth16 curve=bn254 nbConstraints=1 took=1.444299  
17:59:27 DBG verifier done backend=groth16 curve=bn254 took=1.029061  
proof 164 bytes, public inputs 76 bytes, local verify ok  
gas estimate: 399475  
code: 0  
codespace: ""  
data: ""  
events: []  
gas_used: "0"  
gas_wanted: "0"  
height: "0"  
info: ""  
logs: []  
raw_log: ""  
timestamp: ""  
tx: null  
txhash: AD31674DD9608B4DDB37A361966F47706888686159381DDC28A76EDD8FB43306  



**To validate** 

```bash

lucidchaind q proofs proofs-for-checkpoint --sidechain-id hospitality-platform-01 --checkpoint-sequence 1

```

you should see something like this  

pagination:  
  total: "1"  
proofs:  
- binding: lY3I4mLg8f3O1lyYbB0f4ZYCGbWcqWjjwyCRjGCPYlI=  
  checkpoint_hash: UeKqEVCQGvL3a0RpxMHOTbIQU8TM7q7MScSe0YPb1wQ=  
  checkpoint_sequence: "1"  
  claim_id: geofence.v1  
  id: d3ccd1dc31e6accd20028e460b95680483523c9ecc6e088592ba8bd2ed7f71fe  
  proof: 7m4d5SPE9s2PD8P0HimpK7zVc4EV96gthWfqCfh7Z8uXwT2aLmNO3Dhgd0xVRibr5ZFCChmG/4nKyCRNwcPNdA5OLn6jEfNu7JaQoIdZbUkE6CABTKaVE6xJoKX1jxVcyic1dIK3MLv8JsVp5FQwtgFoTAMDTCaueMBZ3EtfSg0AAAAAQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=  
  proof_system_id: groth16-bn254  
  public_inputs: AAAAAgAAAAAAAAACAAAAAAAAAAAAAAAAAAAAAJWNyOJi4PH9ztZcmGwdH+EAAAAAAAAAAAAAAAAAAAAAlgIZtZypaOPDIJGMYI9iUg==  
  sidechain_id: hospitality-platform-01  
  submitter: cosmos18yem5p78w8efrx6dvc45dufjntvq0j6kng2ce0  
  verified: true  
  verified_at: "2026-10-10T13:59:27.012497883Z"  
  verified_height: "319"  
  verifier_version: 1  


**How to suspend a sidechain**

You will need to create a proposal suspend.json

```json
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

```

Submit and vote in one go

```bash

lucidchaind tx gov submit-proposal suspend.json --from validator --chain-id my-testnet-1 -y; sleep 7; lucidchaind tx gov vote <proposal id> yes --from validator --chain-id my-testnet-1 -y

#example
#lucidchaind tx gov submit-proposal suspend.json --from validator --chain-id my-testnet-1 -y; sleep 7; lucidchaind tx gov vote 3 yes --from validator --chain-id my-testnet-1 -y

```



**How to slash a sidechain**
You will need to create a proposal slash.json

```json
{
  "messages": [
    {
      "@type": "/lucidchain.sidechain.v1.MsgSlashSidechain",
      "authority": "<gov module address>",
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

```

Run the following to get the gov module address

```bash

lucidchaind query auth module-account gov

```

you should see a similar output

account:  
  type: /cosmos.auth.v1beta1.ModuleAccount  
  value:  
    account_number: 14541275249009342428  
    address: cosmos10d07y265gmmuvt4z0w9aw880jnsr700j6zn9kn  <<== grab this  
    name: gov  
    permissions:  
    - burner  
    public_key: ""  
    sequence: 0  

Then run the following

```bash
lucidchaind tx gov submit-proposal slash.json --from validator --chain-id my-testnet-1 -y

#wait until tx is completed, then vote. Submission and voting should happen within 60s timeframe otherwise the proposal gets dissmissed

lucidchaind tx gov vote 1 yes --from validator --chain-id my-testnet-1 -y

# This will make submission and voting within the time frame
lucidchaind tx gov submit-proposal slash.json --from validator --chain-id my-testnet-1 -y && sleep 7 && lucidchaind tx gov vote 1 yes --from validator --chain-id my-testnet-1 -y

#query to verify
lucidchaind query sidechain sidechain hospitality-platform-01

#you should see
#  bond:
#    amount: "900000"
#    denom: stake
#...
#  status: SIDECHAIN_STATUS_SLASHED


lucidchaind query gov proposal 1


```

**How to exit a sidechain**

No proposals or voting is needed for this. There is a cool of period of 14 days, and the bond value returns to the sidechain owner

```bash
lucidchaind tx sidechain exit hospitality-platform-01 --from validator --chain-id my-testnet-1 -y

```
