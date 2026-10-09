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

Firt enable secp256k1 in dev. Do not do this for production to keep the node quantum-resistant

```bash

#make sure you are in your home directory
G=~/.lucidchain/config/genesis.json
jq '.app_state.sidechain.params.allowed_pubkey_type_urls += ["/cosmos.crypto.secp256k1.PubKey"]' "$G" > /tmp/g.json && mv /tmp/g.json "$G"

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

export STATE_ROOT=d841f966f8bf17d49335f4b134c2178fd5aca8244d46b8d5f9a5470338095b7b
export STATE_ROOT_B64=$(echo $STATE_ROOT | xxd -r -p | base64 -w0)
K1=$(lucidchaind keys export sidechainkey1 --unarmored-hex --unsafe)
K2=$(lucidchaind keys export sidechainkey2 --unarmored-hex --unsafe)

go run ./tools/signcheckpoint \
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

lucidchaind query attestor -h

lucidchaind query proofs -h

lucidchaind tx sign -h
