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

lucidchaind query checkpoint -h

lucidchaind query attestor -h

lucidchaind query proofs -h

lucidchaind tx sign -h
