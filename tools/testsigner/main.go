package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	"github.com/lucidpay/lucidchain/x/checkpoint/types"
)

// Registered signer_keys for the sidechain (index order).
var signerKeys = []string{
	"AjvrYZKX4W2FxCV6Zu32SS9CsMoxTBv4h0Y3MiIMn4Ik",
	"A8qr2ZvxLnJBYMkPxgw8x8gGd3COYrZWBVZ59qNrQohC",
	"A2lxiK3BqOIUYmKDBWfV2YRIs+55Vra1Czu0JynlTCHt",
}

type cliSig struct {
	Signature   string `json:"signature"`
	SignerIndex int    `json:"signer_index"`
}

func main() {
	fmt.Printf("testsigner: %s\n", time.Now().Format(time.RFC3339))

	root, _ := hex.DecodeString(os.Getenv("ZK_ROOT"))
	sr, _ := base64.StdEncoding.DecodeString(os.Getenv("STATE_ROOT_B64"))
	prev, _ := base64.StdEncoding.DecodeString(os.Getenv("PREV_B64"))

	doc := &types.CheckpointSignDoc{
		ChainId: "my-testnet-1", SidechainId: os.Getenv("SC"), LcSequence: 4,
		StateRoot: sr, PreviousCheckpointHash: prev, RecordCount: 1,
		SignerSetVersion: 1, DataPointer: "ipfs://pq-trip-1",
		ZkAnchor: &types.ZkAnchor{
			Scheme:      types.ZkHashScheme_ZK_HASH_SCHEME_POSEIDON2_BABYBEAR,
			RecordsRoot: root,
		},
	}
	bz, err := types.CheckpointSignBytes(doc)
	if err != nil {
		panic(err)
	}
	fmt.Printf("signbytes=%x\nsha256=%x\n", bz, sha256.Sum256(bz))

	// Verify the signatures exported in the shell against the registered keys.
	for _, env := range []string{"SIG0", "SIG1"} {
		raw := os.Getenv(env)
		if raw == "" {
			fmt.Printf("%s: not set, skipping\n", env)
			continue
		}
		var cs cliSig
		if err := json.Unmarshal([]byte(raw), &cs); err != nil {
			fmt.Printf("%s: bad JSON: %v\n", env, err)
			continue
		}
		sig, err := base64.StdEncoding.DecodeString(cs.Signature)
		if err != nil {
			fmt.Printf("%s: bad base64: %v\n", env, err)
			continue
		}
		if cs.SignerIndex < 0 || cs.SignerIndex >= len(signerKeys) {
			fmt.Printf("%s: signer_index %d out of range\n", env, cs.SignerIndex)
			continue
		}
		pkb, _ := base64.StdEncoding.DecodeString(signerKeys[cs.SignerIndex])
		pk := &secp256k1.PubKey{Key: pkb}
		fmt.Printf("%s: index=%d len(sig)=%d valid_for_seq4=%v\n",
			env, cs.SignerIndex, len(sig), pk.VerifySignature(bz, sig))
	}
}
