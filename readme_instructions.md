# Jump start your node

lucidchaind init dev-test-node --chain-id my-testnet-1

nano $HOME/.lucidchain/config/app.toml

**modify using nano**

minimum-gas-prices = "0ucheck" 

**Ctrl+x and y to save**


lucidchaind keys add validator

lucidchaind genesis add-genesis-account validator 100000000stake,1000000001000ucheck

lucidchaind genesis gentx validator 1000000stake --chain-id my-testnet-1

lucidchaind genesis collect-gentxs

lucidchaind keys show validator --address

lucidchaind query bank balances <address>

lucidchaind start


**Open New Terminal and run these commands one by one**


lucidchaind version

lucidchaind q upgrade module-versions

lucidchaind query auth module-account gov

lucidchaind query consensus comet block-latest

lucidchaind query bank total

lucidchaind query sidechain -h

lucidchaind tx sidechain -h

lucidchaind tx sidechain register-sidechain -h

**How to register a chain**

First enable secp256k1 in dev, for both sidechains and attestors. Do not do this for production, to keep the node quantum-resistant.

A fresh `lucidchaind init` also sets `auto_activate` to `false`, which leaves a new sidechain `PENDING` so every checkpoint is rejected. Set it to `true` for dev.

```bash

#make sure you are in your home directory
G=~/.lucidchain/config/genesis.json
jq '.app_state.sidechain.params.allowed_pubkey_type_urls += ["/cosmos.crypto.secp256k1.PubKey"]
  | .app_state.attestor.params.allowed_pubkey_type_urls += ["/cosmos.crypto.secp256k1.PubKey"]
  | .app_state.sidechain.params.auto_activate = true' "$G" > /tmp/g.json && mv /tmp/g.json "$G"

lucidchaind genesis validate
lucidchaind comet unsafe-reset-all
lucidchaind start


```

If you have initialised the node with old code you will need to fix the genesis file

```bash

#make sure you are in your home directory
G=~/.lucidchain/config/genesis.json   # adjust to your home dir
jq '.app_state.sidechain.params = {
  "min_bond_by_tier": [
    {"tier":"ASSURANCE_TIER_NOTARIZED","min_bond":{"denom":"stake","amount":"1000000"}},
    {"tier":"ASSURANCE_TIER_ATTESTED","min_bond":{"denom":"stake","amount":"5000000"}},
    {"tier":"ASSURANCE_TIER_PROVEN","min_bond":{"denom":"stake","amount":"10000000"}}
  ],
  "missed_checkpoint_grace_seconds":"3600",
  "exit_cooldown_seconds":"1209600",
  "max_signer_keys":7,
  "allowed_pubkey_type_urls":["/cosmos.crypto.mldsa65.PubKey"],
  "auto_activate": true
}' "$G" > /tmp/g.json && mv /tmp/g.json "$G"

lucidchaind genesis validate
lucidchaind comet unsafe-reset-all
lucidchaind start

```

**create signer keys**

lucidchaind keys add sidechainkey1

lucidchaind keys add sidechainkey2

lucidchaind keys add sidechainkey3

**display the public keys**

lucidchaind keys show sidechainkey1 --pubkey

lucidchaind keys show sidechainkey2 --pubkey

lucidchaind keys show sidechainkey3 --pubkey


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

#EXAMPLE
#lucidchaind tx sidechain register-sidechain \
#  hospitality-platform-01 "Hospitality Platform" notarized 1000000stake \
#  --signer-keys '{"@type":"/cosmos.crypto.secp256k1.PubKey","key":"Aj3S0dAItEDqOpHm7lw2eU79vgigg29etjNMO0i+BgZF"}' \
#  --signer-keys '{"@type":"/cosmos.crypto.secp256k1.PubKey","key":"AsxltdpDXisuwLfiuSC0S5MzJ/EN1ATdzULinSEZZKjM"}' \
#  --signer-keys '{"@type":"/cosmos.crypto.secp256k1.PubKey","key":"Am9As0veC6DyLha69fjDFbeO7eUU78GQ7La0d474+T0Q"}' \
#  --signature-threshold 2 \
#  --checkpoint-interval-seconds 300 \
#  --metadata-uri https://example.org/meta.json \
#  --from validator --chain-id my-testnet-1 --gas auto --gas-adjustment 1.5

#you should see something similar to this
#gas estimate: 121149
#auth_info:
#  fee:
#    amount: []
#    gas_limit: "121149"
#    granter: ""
#    payer: ""
#  signer_infos: []
#  tip: null
#body:
#  extension_options: []
#  memo: ""
#  messages:
#  - '@type': /lucidchain.sidechain.v1.MsgRegisterSidechain
#    bond:
#      amount: "1000000"
#      denom: stake
#    checkpoint_interval_seconds: "300"
#    id: hospitality-platform-01
#    metadata_uri: https://example.org/meta.json
#    name: Hospitality Platform
#    operator: cosmos18yem5p78w8efrx6dvc45dufjntvq0j6kng2ce0
#    signature_threshold: 2
#    signer_keys:
#    - '@type': /cosmos.crypto.secp256k1.PubKey
#      key: Aj3S0dAItEDqOpHm7lw2eU79vgigg29etjNMO0i+BgZF
#    - '@type': /cosmos.crypto.secp256k1.PubKey
#      key: AsxltdpDXisuwLfiuSC0S5MzJ/EN1ATdzULinSEZZKjM
#    - '@type': /cosmos.crypto.secp256k1.PubKey
#      key: Am9As0veC6DyLha69fjDFbeO7eUU78GQ7La0d474+T0Q
#    tier: ASSURANCE_TIER_NOTARIZED
#  non_critical_extension_options: []
#  timeout_height: "0"
#  timeout_timestamp: null
#  unordered: false
#signatures: []
#confirm transaction before signing and broadcasting [y/N]: y
#code: 0
#codespace: ""
#data: ""
#events: []
#gas_used: "0"
#gas_wanted: "0"
#height: "0"
#info: ""
#logs: []
#raw_log: ""
#timestamp: ""
#tx: null
#txhash: 3AA09EE67BCF0960D6F30FDEA10BE40365EE5C2B076FA427CB690ED2245320FA


lucidchaind q tx 3AA09EE67BCF0960D6F30FDEA10BE40365EE5C2B076FA427CB690ED2245320FA # will show information about the registration

lucidchaind query sidechain sidechains
#will show something similar to this
#pagination:
#  total: "1"
#sidechains:
#- activated_at: "2026-10-09T10:39:29.496697798Z"
#  bond:
#    amount: "1000000"
#    denom: stake
#  checkpoint_interval_seconds: "300"
#  id: hospitality-platform-01
#  metadata_uri: https://example.org/meta.json
#  name: Hospitality Platform
#  operator: cosmos18yem5p78w8efrx6dvc45dufjntvq0j6kng2ce0
#  registered_at: "2026-10-09T10:39:29.496697798Z"
#  signature_threshold: 2
#  signer_keys:
#  - type: /cosmos.crypto.secp256k1.PubKey
#    value: Aj3S0dAItEDqOpHm7lw2eU79vgigg29etjNMO0i+BgZF
#  - type: /cosmos.crypto.secp256k1.PubKey
#    value: AsxltdpDXisuwLfiuSC0S5MzJ/EN1ATdzULinSEZZKjM
#  - type: /cosmos.crypto.secp256k1.PubKey
#    value: Am9As0veC6DyLha69fjDFbeO7eUU78GQ7La0d474+T0Q
#  signer_set_version: "1"
#  status: SIDECHAIN_STATUS_ACTIVE
#  tier: ASSURANCE_TIER_NOTARIZED


```


**How to work with checkpoints**

```bash

# Tx
#lucidchaind tx checkpoint submit-checkpoint [sidechain-id] [lc-sequence] [state-root] [previous-checkpoint-hash] [record-count] [data-pointer] [signer-set-version] [flags]

lucidchaind tx checkpoint submit-checkpoint \
  hospitality-platform-01 \
  1 \
  d841f966f8bf17d49335f4b134c2178fd5aca8244d46b8d5f9a5470338095b7b \
  "" \
  100 \
  "ipfs://test-checkpoint-1" \
  1 \
  --signatures '{"signer_index":0,"signature":"<base64>"}' \
  --signatures '{"signer_index":1,"signature":"<base64>"}' \
  --from <key> \
  --chain-id <id> \
  --gas auto --gas-adjustment 1.5 \
  --gas-prices 0.025stake \
  -y

#Steps needed 
# first build a signature. make sure you have the following folder structure in your home directory ./tools/signcheckpoint/
# and the binary signcheckpoint is in it "chmod +x signcheckpoint" then run the code bellow
# to build the binary, run this from the repository folder:
#   go build -o ~/tools/signcheckpoint/signcheckpoint ./tools/signcheckpoint

export STATE_ROOT=d841f966f8bf17d49335f4b134c2178fd5aca8244d46b8d5f9a5470338095b7b
export STATE_ROOT_B64=$(echo $STATE_ROOT | xxd -r -p | base64 -w0)
K1=$(lucidchaind keys export sidechainkey1 --unarmored-hex --unsafe)
K2=$(lucidchaind keys export sidechainkey2 --unarmored-hex --unsafe)

./tools/signcheckpoint/signcheckpoint \
  --chain-id my-testnet-1 \
  --sidechain-id hospitality-platform-01 \
  --lc-sequence 1 \
  --state-root $STATE_ROOT \
  --previous-hash "" \
  --record-count 100 \
  --data-pointer "ipfs://test-checkpoint-1" \
  --signer-set-version 1 \
  --key 0=$K1 --key 1=$K2

# take the signatures and add them to submit-checkpoint
export STATE_ROOT=d841f966f8bf17d49335f4b134c2178fd5aca8244d46b8d5f9a5470338095b7b
export STATE_ROOT_B64=$(echo $STATE_ROOT | xxd -r -p | base64 -w0)

lucidchaind tx checkpoint submit-checkpoint \
  hospitality-platform-01 1 $STATE_ROOT_B64 "" 100 "ipfs://test-checkpoint-1" 1 \
  --signatures '{"signature":"baV//6Hw/F37LTggzIV3mUyx0h1GoX20KF7H3IAf2W867R0cIaEDETtRCs2LdpSnz89ijDPVSb11L3Tm5RbHgg==","signer_index":0}' \
  --signatures '{"signature":"QWFPMHbhGTXrwgKmMAYrnSuCbpV3MXNa8MBRqzb4mhEoGoxwqlkUS8iTikGZfURbtBZ9HaRBiYS/j/abAxe68w==","signer_index":1}' \
  --from validator --chain-id my-testnet-1 \
  --gas auto --gas-adjustment 1.5 -y

#you should get an output similar to this

#gas estimate: 258096
#code: 0
#codespace: ""
#data: ""
#events: []
#gas_used: "0"
#gas_wanted: "0"
#height: "0"
#info: ""
#logs: []
#raw_log: ""
#timestamp: ""
#tx: null
#txhash: 058043461378E698B4168564CFBB30963EABB252FE1A2D2B81F3097D9D2F310F


# Queries
lucidchaind q checkpoint params

lucidchaind q checkpoint checkpoint hospitality-platform-01 1

lucidchaind q checkpoint latest-checkpoint hospitality-platform-01
#this should output something similar to this
#checkpoint:
#  checkpoint_hash: 0VvDymCJ4DU02QeRm3Z423D1g2ICxKetgCLr1xq1zhs=
#  data_pointer: ipfs://test-checkpoint-1
#  finalized_at: "2026-10-09T06:17:54.235499177Z"
#  finalized_height: "1239"
#  lc_sequence: "2"
#  previous_checkpoint_hash: KMsdoqcJeJIMp64OmPh8j7Ia5shHYU0rhfFmN3cTTBk=
#  record_count: "50"
#  sidechain_id: hospitality-platform-01
#  signatures_digest: pd1NgFzaJxzI6l+FtZ54OYWAHP0WzQvXHdtzfx1xqG8=
#  signer_bitmap: Aw==
#  signer_set_version: "1"
#  state_root: TDlx18fsUYPdjmEg7o1UKP2otukHED7xLvYB6ya1D1w=


lucidchaind q checkpoint checkpoints hospitality-platform-01 --page-limit 10 --page-reverse


  ```

lucidchaind query checkpoint -h

**How to work with attestor**

`submit-attestation.sh` expects schema `kyc-v1` and an ACTIVE attestor `acme` whose signer key is the `validator` key. Creating the schema and activating the attestor are governance-only, so set them up first.

Note: with the default genesis the governance voting period is 48 hours, so each proposal below takes two days to pass. For a dev chain, shorten it before the first start:
`jq '.app_state.gov.params.voting_period = "60s" | .app_state.gov.params.expedited_voting_period = "30s"' "$G" > /tmp/g.json && mv /tmp/g.json "$G"`

```bash

# 1. create the schema (governance)
cat > schema.json <<'EOF'
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
lucidchaind tx gov submit-proposal schema.json --from validator \
    --chain-id my-testnet-1 --gas auto --gas-adjustment 1.5 --gas-prices 0stake --yes
lucidchaind tx gov vote <proposal-id> yes --from validator --chain-id my-testnet-1 --gas-prices 0stake --yes
lucidchaind query attestor schemas   # after the voting period

# 2. register the attestor, with the validator key as its signer
lucidchaind tx attestor register-attestor \
  --id acme --name Acme --authorized-schema-ids kyc-v1 \
  --signer-keys "$(lucidchaind keys show validator --pubkey)" \
  --signature-threshold 1 --bond-amount 1000000 \
  --credential-uri https://example.org/acme.json \
  --from validator --chain-id my-testnet-1 --gas auto --gas-adjustment 1.5 --gas-prices 0stake --yes

# 3. activate it (governance): same proposal format as step 1, with this message
#   {"@type": "/lucidchain.attestor.v1.MsgActivateAttestor",
#    "authority": "cosmos10d07y265gmmuvt4z0w9aw880jnsr700j6zn9kn", "id": "acme"}
lucidchaind query attestor attestor acme   # status should be ATTESTOR_STATUS_ACTIVE

```

The script is `tools/signbytes/submit-attestation.sh`. Copy it to the folder you work in and make it executable with `chmod +x submit-attestation.sh`.

First step is to create a claim

```bash

printf '%s' '{"revenue":"1250000","period":"2026-09"}' > claim.json

#run the shell script. shell script can be found under tools/signbytes

./submit-attestation.sh

# you should get something like this
#Checkpoint: sidechain=hospitality-platform-01 seq=1 hash=51e2aa1150901af2f76b4469c4c1ce4db21053c4cceeaecc49c49ed183dbd704
#Signer key matches the attestor's registered key.
#Wrote sigs.json
#gas estimate: 174748
#code: 0
#codespace: ""
#data: ""
#events: []
#gas_used: "0"
#gas_wanted: "0"
#height: "0"
#nfo: ""
#logs: []
#raw_log: ""
#timestamp: ""
#tx: null
#txhash: 5D1C540C4E110B77D05FDE3E74EAC2FE9D2685B522C77AB7BBE6993E52928AEB

# now run 

lucidchaind query attestor checkpoint-attestations hospitality-platform-01 1

#you should get something similar to this
#attestations:
#- attestor_id: acme
#  checkpoint_hash: UeKqEVCQGvL3a0RpxMHOTbIQU8TM7q7MScSe0YPb1wQ=
#  checkpoint_sequence: "1"
#  claim_payload: eyJyZXZlbnVlIjoiMTI1MDAwMCIsInBlcmlvZCI6IjIwMjYtMDkifQ==
#  expires_at: "2027-10-09T15:20:14.54848753Z"
#  id: 30dd87b708f6bbd7a6e6c6527aceec4340cce248ee583fdff0b8455eb03aaafc
#  issued_at: "2026-10-09T15:20:14.54848753Z"
#  issued_height: "383"
#  schema_id: kyc-v1
#  sidechain_id: hospitality-platform-01
#  signatures_digest: XQLZ1BN9olL0dEc6OG8k1PCnciJyFtt/Txfs4b4Y/GU=
#  signer_bitmap: AQ==
#  signer_set_version: "1"
#  status: ATTESTATION_STATUS_ACTIVE
#pagination:
#  total: "1"



```

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

```bash
# create proposal.json with the json data bellow. Get dispute_id from the raise dispute tx

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

lucidchaind query proofs -h

lucidchaind tx sign -h
