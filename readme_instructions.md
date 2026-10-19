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

lucidchaind init dev-test-node --chain-id my-testnet-1 # --consensus-key-algo ml_dsa_65 turnes on quantum resistance

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

**now add the verifiers**

Add both Groth16 and PLONK

```bash

cd ~/go/src/github.com/lucidpay/lucidchain
chmod +x ./tools/gen-vk/add-verifier.sh
#./tools/gen-vk/add-verifier.sh                       # defaults: ~/.lucidchain/config/genesis.json, ./vk.bin
./tools/gen-vk/add-verifier.sh ~/.lucidchain/config/genesis.json ~/tmp_workarea/keys-geo-plonk-bn254/vk.bin plonk-bn254 500000
./tools/gen-vk/add-verifier.sh ~/.lucidchain/config/genesis.json ~/tmp_workarea/keys-geo-groth16-bn254/vk.bin groth16-bn254 500000
cd

```

Note: with the default genesis the governance voting period is 48 hours, so each proposal below takes two days to pass. For a dev chain, shorten it before the first start:

```bash
#go back to home directory

G=~/.lucidchain/config/genesis.json
`jq '.app_state.gov.params.voting_period = "60s" | .app_state.gov.params.expedited_voting_period = "30s"' "$G" > /tmp/g.json && mv /tmp/g.json "$G"`

```

Add attestors schema

```bash
G=~/.lucidchain/config/genesis.json

`jq '.app_state.attestor.schemas = [
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
]' "$G" > /tmp/g.json && mv /tmp/g.json "$G"`

unset G
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

lucidchaind keys show attestor-operator --pubkey

lucidchaind keys show sidechainkey1 --pubkey

lucidchaind keys show sidechainkey2 --pubkey

lucidchaind keys show sidechainkey3 --pubkey

```

Register the sidechain

```bash

lucidchaind tx sidechain register-sidechain \
  hospitality-platform-01 "Hospitality Platform" notarized 1000000stake \
  --signer-keys "$(lucidchaind keys show sidechainkey1 --pubkey)" \
  --signer-keys "$(lucidchaind keys show sidechainkey2 --pubkey)" \
  --signer-keys "$(lucidchaind keys show sidechainkey3 --pubkey)" \
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

cd go/src/github.com/lucidpay/lucidchain

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
export ZK_ROOT=$(printf '%064x' 7 | xxd -r -p | base64 -w0)   # test value; use your real MiMC root for real proofs

#NOTE: --zk-anchor is optional and nullable. But here it demonstrates how to use it
lucidchaind tx checkpoint submit-checkpoint \
  hospitality-platform-01 1 $STATE_ROOT_B64 "" 100 "ipfs://test-checkpoint-1" 1 \
  --signatures '{"signature":"xWbozMDCDKXRAYugCVQXuWXlxeNdX4Gj814Nax4meeN1KURU1gxGTOflrIyX1Y/FI5sXvncru8ZlS3mxQmBQIQ==","signer_index":0}' \
  --signatures '{"signature":"+hXNHk0IKTP/4X/30iAv8VZjErcp4nlUJ1AZaxnI509SMeNFr7P64vRJTLMxABkkrwFXAc1c9nf8IdPOfBwAoA==","signer_index":1}' \
  --zk-anchor '{"records_root":"'"$ZK_ROOT"'","records_tree_depth":20,"scheme":"ZK_HASH_SCHEME_MIMC_BN254"}' \
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
  {"lon": 55.2700, "lat": 25.2000, "t": 1791817725, "salt": "0x00e0bb7026e79cf9dd2c6b5850a59f5600e87d18b08d88e74c524b67669867e7"},
  {"lon": 55.2705, "lat": 25.2002, "t": 1791817735, "salt": "0x0091b755f9d5c450ed41cd0f36526dc06ad905d0059ff0ee4b345f7e9fce504b"}
]
EOF

```
now build the batch
syntax: python3 build-batch.py --records records.json \
  --area-box <lon_min> <lat_min> <lon_max> <lat_max> \
  --out-dir ~/tmp_workarea/json

```bash

cd go/src/github.com/lucidpay/lucidchain

python3 tools/zkgeo/build-batch.py --records ~/tmp_workarea/json/records.json --area-box 55.269 25.199 55.272 25.201 --out-dir ~/tmp_workarea/json

```

Look at the anchor the batch produces

```bash

go run ./cmd/batchroot -batch ~/tmp_workarea/json/batch.json -records-depth 5

```

It should read something similar

record_count:       2
records_tree_depth: 5
records_root (hex): 05852543a19218143ceda094cc7615c4b58f116bcadd6336c4333bcd6a913ea5
records_root (b64): BYUlQ6GSGBQ87aCUzHYVxLWPEWvK3WM2xDM7zWqRPqU=

```bash
export STATE_ROOT=05852543a19218143ceda094cc7615c4b58f116bcadd6336c4333bcd6a913ea5
export K1=$(lucidchaind keys export sidechainkey1 --unarmored-hex --unsafe)
export K2=$(lucidchaind keys export sidechainkey2 --unarmored-hex --unsafe)

DRY_RUN=1 BATCH=~/tmp_workarea/json/batch.json tools/zkgeo/submit-checkpoint-from-batch.sh
BATCH=~/tmp_workarea/json/batch.json tools/zkgeo/submit-checkpoint-from-batch.sh

```

**run a proof system**

Build the record file

Use the sequence the script printed: e.g. 

next: SEQ=2 SCHEME=groth16 BATCH=/home/dev/tmp_workarea/json/batch.json tools/zkgeo/prove-and-submit-geo.sh


```bash
SEQ=<new sequence> SCHEME=groth16 BATCH=~/tmp_workarea/json/batch.json tools/zkgeo/prove-and-submit-geo.sh
unset K1 K2

```

The PLONK version
Same sequence
```bash
SEQ=<new sequence> SCHEME=plonk BATCH=~/tmp_workarea/json/batch.json tools/zkgeo/prove-and-submit-geo.sh

```

**To validate** 

```bash

lucidchaind q proofs proofs-for-checkpoint --sidechain-id hospitality-platform-01 --checkpoint-sequence 2 -o json \
  | jq '.proofs[] | {proof_system_id, claim_id, verified_height}'

```

you should see something like this  

pagination:  
  total: "2"  
proofs:  
- binding: br3wyQb3gvjlQN/8LLIrW9MOpQNLJxK64r6v0Teyky4=  
  checkpoint_hash: a4N9ViAmzubkNDnYaY0NpnkF/hBVKafGqAgrS1rMTkE=  
  checkpoint_sequence: "1"  
  claim_id: geofence.v1  
  id: d3ccd1dc31e6accd20028e460b95680483523c9ecc6e088592ba8bd2ed7f71fe
  proof: kKEVFzKRjgpg4jZ3GIwVdQFAkUSkULB5S2iB276pb5aU/KL9+F7tyOoIOg2H2F3JnqSdNO3IJhwuvwRECdeDzgtvOTCRrc6PS6GVsHwePYAagUQrd4ly37GJPGur48QOoE5pXBnnF0LR9QnVvGlEjingj+iDbQ00LBhu6S/+y88AAAABy5Chiyhs12bV7qsNLeozLUh8kt1l8hvJ9v/Tst1/11bfIoDSMfCfKqxCck5ofIW3GLhCzfFchOyfHOduen8DVg==  
  proof_system_id: groth16-bn254  
  public_inputs: AAAABwAAAAAAAAAHAAAAAAAAAAAAAAAAAAAAAG698MkG94L45UDf/CyyK1sAAAAAAAAAAAAAAAAAAAAA0w6lA0snErrivq/RN7KTLgWFJUOhkhgUPO2glMx2FcS1jxFryt1jNsQzO81qkT6lKWPF3FkkrBXuimDeCZY0KjJ2QWctyrcWgDtELVGTjA0AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAasz38wAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABqzRQTAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAI=  
  sidechain_id: hospitality-platform-01  
  submitter: cosmos18yem5p78w8efrx6dvc45dufjntvq0j6kng2ce0  
  verified: true  
  verified_at: "2026-10-16T14:21:58.257008984Z"  
  verified_height: "109"  
  verifier_version: 1  
- binding: br3wyQb3gvjlQN/8LLIrW9MOpQNLJxK64r6v0Teyky4=  
  checkpoint_hash: a4N9ViAmzubkNDnYaY0NpnkF/hBVKafGqAgrS1rMTkE=  
  checkpoint_sequence: "1"  
  claim_id: geofence.v1  
  id: fb4a238f92ae1dce9f5b3bda48bd4747a7e63f2e2f0cd562a07d484214f5b729  
  proof: xqh3+c+hEg0dWuh+gCtnEOmzkGG5mN6+8AVWIuzMommu4tWpgmOxMcZtjPtmQFubzXPTp4sz5QsjmSh0/AJFMce5d9XTbI0Us/yZanhPaIOHfrVyMF9fQPa/AxFMZgNMh9zi1hR9DODh8P0e7QvbJsns/Dm11Z/cLzLWEM9NQEjFzoh6vqcGzqmg9w8/H7NyN/QX9NZdc6YkGV4/hWllq5x0Jiz1MLXYUhae+qyYGjVsg8Kbe01Seop+9pfxDspdoc12A8ieQxK03aLbWSdd0Wm3IWj6ftnZuzzgC0Du2KreS3JRDnM0Mc4rugTaFa46K60TwlhtNZb39JeWAzQDbgAAAAcLcdjo79f3mhIfOOTyAA+vYsCHOox/GzGBKsoYkfjOMh9ptSUjoyRIlXV3GRBdm68ihyxlU/2nhFvAcwZzSELjDFqfDY5SKzSkQDq42O1zHjP13OQ6ZXjNFblLetrKvq4flNaquHlCPrUPf7hojFAIg2zn1uUtl6sWag5J+zAl5xmSzP/cY02uHMFYORiLm+xQQK52rij2WoMjs7BgsKsUKp3DPl9UQA6GPl8G5oNaSH7lcyMhr3B23otuT0UMhLwPYdPbmt18CyXo0iNPif7+bY+2MyAZ4TJPWITiCYDEF5N3vvFVP5i2cb5qmrKKiHSkmbCKlsTyF6e5QdjL8Uv+L4aMLDBzA0zs9V9T7bEeQMyyXKBSjnYgxqJCHbRpwVcAAAABreE7an8GrsAKMIvmucoxkmnBNJPpHl9tkZ+q//UeqAg=  
  proof_system_id: plonk-bn254  
  public_inputs: AAAABwAAAAAAAAAHAAAAAAAAAAAAAAAAAAAAAG698MkG94L45UDf/CyyK1sAAAAAAAAAAAAAAAAAAAAA0w6lA0snErrivq/RN7KTLgWFJUOhkhgUPO2glMx2FcS1jxFryt1jNsQzO81qkT6lKWPF3FkkrBXuimDeCZY0KjJ2QWctyrcWgDtELVGTjA0AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAasz38wAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABqzRQTAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAI=  
  sidechain_id: hospitality-platform-01  
  submitter: cosmos18yem5p78w8efrx6dvc45dufjntvq0j6kng2ce0  
  verified: true  
  verified_at: "2026-10-16T14:25:35.753168091Z"  
  verified_height: "152"  
  verifier_version: 1  



**How to work with attestor**

first create the needed keys

```bash

lucidchaind keys add attestor-operator
lucidchaind keys add attestorkey1
lucidchaind keys add attestorkey2

lucidchaind tx bank send validator $(lucidchaind keys show attestor-operator -a) 5000000stake \
  --chain-id my-testnet-1 --gas auto --gas-adjustment 1.5 --yes

lucidchaind keys show attestor-operator --address
lucidchaind keys show attestorkey1 --address
lucidchaind keys show attestorkey2 --address
lucidchaind keys show attestor-operator --pubkey
lucidchaind keys show attestorkey1 --pubkey
lucidchaind keys show attestorkey2 --pubkey

```

Register an attestor

```bash
export ATT_OPER_ADDRESS=$(lucidchaind keys show attestor-operator --address)
export ATT_KEY_PK=$(lucidchaind keys show attestorkey1 --pubkey)
cat << EOF > ~/tmp_workarea/json/register-attestor.json 
{
  "body": {
    "messages": [{
      "@type": "/lucidchain.attestor.v1.MsgRegisterAttestor",
      "operator": "$ATT_OPER_ADDRESS",
      "id": "kyc-attestor-01",
      "name": "KYC Attestor 01",
      "authorized_schema_ids": ["kyc-v1"],
      "signer_keys": [$ATT_KEY_PK],
      "signature_threshold": 1,
      "bond_amount": "1000000",
      "credential_uri": "https://example.org/attestor.json"
    }],
    "memo": "",
    "timeout_height": "0",
    "extension_options": [],
    "non_critical_extension_options": []
  },
  "auth_info": {
    "signer_infos": [],
    "fee": {"amount": [], "gas_limit": "400000", "payer": "", "granter": ""}
  },
  "signatures": []
}
EOF

lucidchaind tx sign ~/tmp_workarea/json/register-attestor.json \
  --from attestor-operator --chain-id my-testnet-1 > ~/tmp_workarea/json/register-attestor.signed.json

lucidchaind tx broadcast ~/tmp_workarea/json/register-attestor.signed.json

#check
lucidchaind q attestor attestor kyc-attestor-01

```

Activate attestor


```bash
export GOV_ADDRESS=$(lucidchaind q auth module-account gov -o json | jq -r '.account.value.address')
cat << EOF > ~/tmp_workarea/json/activate-attestor.json
{
  "messages": [{
    "@type": "/lucidchain.attestor.v1.MsgActivateAttestor",
    "authority": "$GOV_ADDRESS",
    "id": "kyc-attestor-01"
  }],
  "metadata": "ipfs://test",
  "deposit": "10000000stake",
  "title": "Activate kyc-attestor-01",
  "summary": "Activate kyc-attestor-01"
}
EOF

lucidchaind tx gov submit-proposal ~/tmp_workarea/json/activate-attestor.json --from validator \
  --chain-id my-testnet-1 --gas auto --gas-adjustment 1.5 --gas-prices 0stake --yes
sleep 7
lucidchaind q gov proposals -o json | jq '.proposals[-1] | {id, status}'
PID=$(lucidchaind q gov proposals -o json | jq -r '.proposals[-1].id')
lucidchaind tx gov vote "$PID" yes --from validator \
  --chain-id my-testnet-1 --gas auto --gas-adjustment 1.5 --yes  # proposal id should be 1 

```

To validate

```bash

lucidchaind q gov proposal "$PID" -o json | jq '.proposal.messages[0]."@type", .proposal.title'
lucidchaind q attestor attestor kyc-attestor-01 | grep status


```




`submit-attestation.sh` expects schema `kyc-v1` and an ACTIVE attestor `acme` whose signer key is the `validator` key. Creating the schema and activating the attestor are governance-only, so set them up first.

**1. create the schema (governance)**

Skip this step if you added `kyc-v1` to genesis above (Add attestors schema). Otherwise the proposal fails when it executes, with `"kyc-v1": attestation schema already exists`.

```bash
export GOV_ADDRESS=$(lucidchaind q auth module-account gov -o json | jq -r '.account.value.address')
cat << EOF > ~/tmp_workarea/json/schema.json
{
  "messages": [{
    "@type": "/lucidchain.attestor.v1.MsgCreateAttestationSchema",
    "authority": "$GOV_ADDRESS",
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

sleep 7
lucidchaind q gov proposals -o json | jq '.proposals[-1] | {id, status}'
PID=$(lucidchaind q gov proposals -o json | jq -r '.proposals[-1].id')
lucidchaind tx gov vote "$PID" yes --from validator --chain-id my-testnet-1 --gas-prices 0stake --yes  # proposal ID should be 2 if this is your first proposal


```

**2. register the attestor, with the validator key as its signer**

```bash
lucidchaind tx attestor register-attestor \
  --id acme --name Acme --authorized-schema-ids kyc-v1 \
  --signer-keys "$(lucidchaind keys show attestorkey2 --pubkey)" \
  --signature-threshold 1 --bond-amount 1000000 \
  --credential-uri https://example.org/acme.json \
  --from attestor-operator --chain-id my-testnet-1 --gas auto --gas-adjustment 1.5 --gas-prices 0stake --yes


lucidchaind query attestor attestor acme   # status should be ATTESTOR_STATUS_PENDING

```

3. activate it (governance): same proposal format as step 1, with this message
   {"@type": "/lucidchain.attestor.v1.MsgActivateAttestor",
    "authority": "cosmos10d07y265gmmuvt4z0w9aw880jnsr700j6zn9kn", "id": "acme"}

```bash
export GOV_ADDRESS=$(lucidchaind q auth module-account gov -o json | jq -r '.account.value.address')
cat << EOF > ~/tmp_workarea/json/approve-attestor.json
{
  "messages": [{"@type": "/lucidchain.attestor.v1.MsgActivateAttestor",
    "authority": "$GOV_ADDRESS", "id": "acme"}
    ],
  "metadata": "ipfs://test",
  "deposit": "10000000stake",
  "title": "Aprove Attestor",
  "summary": "Proposal to approve attestor"
}
EOF
lucidchaind tx gov submit-proposal ~/tmp_workarea/json/approve-attestor.json --from validator \
    --chain-id my-testnet-1 --gas auto --gas-adjustment 1.5 --gas-prices 0stake --yes
sleep 7
lucidchaind q gov proposals -o json | jq '.proposals[-1] | {id, status}'
PID=$(lucidchaind q gov proposals -o json | jq -r '.proposals[-1].id')
lucidchaind tx gov vote "$PID" yes --from validator --chain-id my-testnet-1 --gas-prices 0stake --yes  # proposal ID should be 2 if this is your first proposal

```

```bash
#give it a couple minutes then check

lucidchaind query attestor attestor acme   # status should be ATTESTOR_STATUS_ACTIVE

#if it doesn't activate make sure your proposal-id is correct. You can list the proposals using
lucidchaind query gov proposals

```

The script is `tools/signbytes/submit-attestation.sh`. Copy it to the folder you work in and make it executable with `chmod +x submit-attestation.sh`.
  
First step is to create a claim  

```bash

printf '%s' '{"revenue":"1250000","period":"2026-09"}' > ~/tmp_workarea/json/claim.json

#run the shell script. shell script can be found under tools/signbytes
chmod +x tools/signbytes/submit-attestation.sh
#dry run
bash -n tools/signbytes/submit-attestation.sh
NO_SUBMIT=1 CLAIM_FILE=~/tmp_workarea/json/claim.json tools/signbytes/submit-attestation.sh
#actual run
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


**now run to verify**

```bash

lucidchaind query attestor checkpoint-attestations hospitality-platform-01 2

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
lucidchaind keys show challenger -a      # the address
#fund the wallet
lucidchaind tx bank send validator $(lucidchaind keys show challenger -a) 5000000stake \
  --chain-id my-testnet-1 --gas auto --gas-adjustment 1.5 --gas-prices 0stake --yes

#pull the atteestation id for the next command
export ATTESTATION_ID=$(lucidchaind query attestor checkpoint-attestations hospitality-platform-01 2 -o json | jq -r '.attestations[0].id')

#raise a dispute
lucidchaind tx attestor raise-dispute \
  $ATTESTATION_ID \
  ipfs://test-evidence 100000 \
  --from challenger --chain-id my-testnet-1 \
  --gas auto --gas-adjustment 1.5 --gas-prices 0stake --yes
```

**resolve it**


create proposal.json with the json data bellow. Get dispute_id from the raise dispute tx  

```bash

#pull the atteestation id for the next command
export ATTESTATION_ID=$(lucidchaind query attestor checkpoint-attestations hospitality-platform-01 2 -o json | jq -r '.attestations[0].id')
export DISPUTE_ID=$(lucidchaind q txs --query "dispute_raised.attestation_id='$ATTESTATION_ID'" -o json \
  | jq -r '.txs[0].events[] | select(.type=="dispute_raised") | .attributes[] | select(.key=="dispute_id") | .value')

export GOV_ADDRESS=$(lucidchaind q auth module-account gov -o json | jq -r '.account.value.address')
cat << EOF > ~/tmp_workarea/json/resolution-proposal.json
{
  "messages": [{
    "@type": "/lucidchain.attestor.v1.MsgResolveDispute",
    "authority": "$GOV_ADDRESS",
    "dispute_id": "$DISPUTE_ID",
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

lucidchaind tx gov submit-proposal ~/tmp_workarea/json/resolution-proposal.json --from validator \
  --chain-id my-testnet-1 --gas auto --gas-adjustment 1.5 --gas-prices 0stake --yes

sleep 7   # long enough for inclusion in a block

PID=$(lucidchaind q gov proposals -o json | jq -r '.proposals[-1].id')
lucidchaind q gov proposal "$PID" -o json | jq '.proposal | {id, status, voting_end_time}'
lucidchaind tx gov vote "$PID" yes --from validator --chain-id my-testnet-1 \
  --gas auto --gas-adjustment 1.5 --gas-prices 0stake --yes


#wait a minute or two then check the status of the proposal

PID=$(lucidchaind q gov proposals -o json | jq -r '.proposals[-1].id')
lucidchaind q gov proposal "$PID" -o json | jq '.proposal | {status, failed_reason, voting_end_time}'


```

lucidchaind query attestor -h

**How to suspend an attestor**

```bash
export GOV_ADDRESS=$(lucidchaind q auth module-account gov -o json | jq -r '.account.value.address')
cat << EOF > ~/tmp_workarea/json/suspend-attestor-proposal.json
{
  "messages": [{
    "@type": "/lucidchain.attestor.v1.MsgSuspendAttestor",
    "authority": "$GOV_ADDRESS",
    "id": "kyc-attestor-01",
    "reason": "dispute 19b6c8e1..."
  }],
  "metadata": "ipfs://test",
  "deposit": "10000000stake",
  "title": "Suspend kyc-attestor-01",
  "summary": "Suspend kyc-attestor-01"
}
EOF

lucidchaind tx gov submit-proposal ~/tmp_workarea/json/suspend-attestor-proposal.json --from validator \
  --chain-id my-testnet-1 --gas auto --gas-adjustment 1.5 --gas-prices 0stake --yes

sleep 7   # long enough for inclusion in a block

PID=$(lucidchaind q gov proposals -o json | jq -r '.proposals[-1].id')
lucidchaind q gov proposal "$PID" -o json | jq '.proposal | {id, status, voting_end_time}'
lucidchaind tx gov vote "$PID" yes --from validator --chain-id my-testnet-1 \
  --gas auto --gas-adjustment 1.5 --gas-prices 0stake --yes

#give it a minute or two then check
lucidchaind q attestor attestor kyc-attestor-01 | grep status

```

**How to reinstate a suspended attestor**

Only a SUSPENDED attestor can be reinstated. It becomes ACTIVE again.

```bash
export GOV_ADDRESS=$(lucidchaind q auth module-account gov -o json | jq -r '.account.value.address')
cat << EOF > ~/tmp_workarea/json/reinstate-attestor-proposal.json
{
  "messages": [{
    "@type": "/lucidchain.attestor.v1.MsgReinstateAttestor",
    "authority": "$GOV_ADDRESS",
    "id": "kyc-attestor-01"
  }],
  "metadata": "ipfs://test",
  "deposit": "10000000stake",
  "title": "Reinstate kyc-attestor-01",
  "summary": "Reinstate kyc-attestor-01"
}
EOF

lucidchaind tx gov submit-proposal ~/tmp_workarea/json/reinstate-attestor-proposal.json --from validator \
  --chain-id my-testnet-1 --gas auto --gas-adjustment 1.5 --gas-prices 0stake --yes

sleep 7   # long enough for inclusion in a block

PID=$(lucidchaind q gov proposals -o json | jq -r '.proposals[-1].id')
lucidchaind tx gov vote "$PID" yes --from validator --chain-id my-testnet-1 \
  --gas auto --gas-adjustment 1.5 --gas-prices 0stake --yes

#give it a minute or two to check (after the voting period)
lucidchaind q attestor attestor kyc-attestor-01 | grep status

```


**How to suspend a sidechain**

You will need to create a proposal suspend.json

```bash
export GOV_ADDRESS=$(lucidchaind q auth module-account gov -o json | jq -r '.account.value.address')
cat << EOF > ~/tmp_workarea/json/sidechain-suspension-proposal.json
{
  "messages": [
    {
      "@type": "/lucidchain.sidechain.v1.MsgSuspendSidechain",
      "authority": "$GOV_ADDRESS",
      "id": "hospitality-platform-01"
    }
  ],
  "metadata": "",
  "deposit": "10000000stake",
  "title": "Suspend hospitality-platform-01",
  "summary": "Temporarily halt checkpoint submission for this sidechain."
}
EOF

lucidchaind tx gov submit-proposal ~/tmp_workarea/json/sidechain-suspension-proposal.json --from validator --chain-id my-testnet-1 -y; 
sleep 7; 
PID=$(lucidchaind q gov proposals -o json | jq -r '.proposals[-1].id')
lucidchaind tx gov vote "$PID" yes --from validator --chain-id my-testnet-1 -y

#wait for a minute or two and then query to verify
lucidchaind query sidechain sidechain hospitality-platform-01

```



**How to slash a sidechain**
You will need to create a proposal slash.json

```bash
export GOV_ADDRESS=$(lucidchaind q auth module-account gov -o json | jq -r '.account.value.address')
cat << EOF > ~/tmp_workarea/json/sidechain-slash-proposal.json
{
  "messages": [
    {
      "@type": "/lucidchain.sidechain.v1.MsgSlashSidechain",
      "authority": "$GOV_ADDRESS",
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
lucidchaind tx gov submit-proposal ~/tmp_workarea/json/sidechain-slash-proposal.json  --from validator --chain-id my-testnet-1 -y
sleep 7
PID=$(lucidchaind q gov proposals -o json | jq -r '.proposals[-1].id')
lucidchaind tx gov vote "$PID" yes --from validator --chain-id my-testnet-1 -y

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

No proposals or voting is needed for this. There is a cool of period of 14 days, and the bond value returns to the sidechain owner

```bash
lucidchaind tx sidechain exit hospitality-platform-01 --from validator --chain-id my-testnet-1 -y

```
