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

lucidchaind query consensus comet block-latest

lucidchaind query bank total

lucidchaind query sidechain -h

lucidchaind query sidechain sidechain -h

lucidchaind query checkpoint -h

lucidchaind query attestor -h

lucidchaind query proofs -h
