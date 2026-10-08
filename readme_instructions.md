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

Firt create a few keys

**create signer keys**

lucidchaind keys add sidechainkey1

lucidchaind keys add sidechainkey2

lucidchaind keys add sidechainkey3

**display the public keys**

lucidchaind keys show sidechainkey1 --pubkey

lucidchaind keys show sidechainkey2 --pubkey

lucidchaind keys show sidechainkey3 --pubkey


Then register filling in the signer keys

lucidchaind tx sidechain register-sidechain \
  hospitality-platform-01 "Hospitality Platform" notarized 1000000stake \
  --signer-keys '{"@type":"/cosmos.crypto.secp256k1.PubKey","key":"<base64>"}' \
  --signer-keys '{"@type":"/cosmos.crypto.secp256k1.PubKey","key":"<base64>"}' \
  --signer-keys '{"@type":"/cosmos.crypto.secp256k1.PubKey","key":"<base64>"}' \
  --signature-threshold 2 \
  --checkpoint-interval-seconds 300 \
  --metadata-uri https://example.org/meta.json \
  --from <operator-key> --chain-id my-testnet-1 --gas auto --gas-adjustment 1.5

lucidchaind query sidechain sidechain -h

lucidchaind query checkpoint -h

lucidchaind query attestor -h

lucidchaind query proofs -h

lucidchaind tx sign -h
