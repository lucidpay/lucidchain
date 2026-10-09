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


```

lucidchaind query sidechain sidechain -h

**How to work with checkpoints***

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
# first build a signature. make sure you are in the root folder of the source code e.g. lucidchain
# and tehn run the code bellow

export STATE_ROOT=d841f966f8bf17d49335f4b134c2178fd5aca8244d46b8d5f9a5470338095b7b
export STATE_ROOT_B64=$(echo $STATE_ROOT | xxd -r -p | base64 -w0)
K1=$(lucidchaind keys export signer1 --unarmored-hex --unsafe --keyring-backend test)
K2=$(lucidchaind keys export signer2 --unarmored-hex --unsafe --keyring-backend test)

go run ./cmd/signcheckpoint \
  --chain-id my-testnet-1 \
  --sidechain-id hospitality-platform-0 \
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
  --signatures '{"signature":"aFRDy/dnz6A04jQH56LlE/ab1euIa63niAliCSPlvClQIgC0GmVoJkGxGQuu36UdUFponbQC5Fb8XpHPHZc/eQ==","signer_index":0}' \
  --signatures '{"signature":"x9UovSNqbc+QNoJKSnuyw5L9rmpWmUUylMm3cE98uqQiPhwNpK+kJXKHP4zLf0+RYntvuQkB/bIYSrFwRhmq/A==","signer_index":1}' \
  --from validator --chain-id my-testnet-1 \
  --gas auto --gas-adjustment 1.5 -y


# Queries
lucidchaind q checkpoint params
lucidchaind q checkpoint checkpoint hospitality-platform-01 1
lucidchaind q checkpoint latest-checkpoint hospitality-platform-01
lucidchaind q checkpoint checkpoints hospitality-platform-01 --page-limit 10 --page-reverse


  ```

lucidchaind query checkpoint -h

**How to work with attestor**

lucidchaind query attestor -h

lucidchaind query proofs -h

lucidchaind tx sign -h
