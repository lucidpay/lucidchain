// signrotation produces the current-signer-set approvals that
// MsgUpdateSidechainSigners requires (finding N9 of the v0.4-Beta review).
//
// Usage (dev keys are secp256k1 like tools/signcheckpoint; swap the key type
// once ML-DSA-65 keys are usable from the CLI, see H3):
//
//	signrotation -chain-id my-testnet-1 -sidechain-id sc-1 -current-version 1 \
//	  -not-after-height <current height + a few hundred> \
//	  -new-threshold 2 -new-key <hex pubkey> -new-key <hex pubkey> -new-key <hex pubkey> \
//	  -key 0=<hex priv of CURRENT signer 0> -key 2=<hex priv of CURRENT signer 2>
//
// It prints the exact sign bytes to stderr and one
// `--current-set-signatures '{...}'` CLI snippet per approving signer to stdout.
package main

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"

	"github.com/lucidpay/lucidchain/x/sidechain/types"
)

type list []string

func (l *list) String() string     { return strings.Join(*l, ",") }
func (l *list) Set(v string) error { *l = append(*l, v); return nil }

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func main() {
	chainID := flag.String("chain-id", "my-testnet-1", "chain id")
	sidechain := flag.String("sidechain-id", "", "sidechain id")
	version := flag.Uint64("current-version", 1, "CURRENT signer_set_version of the sidechain")
	threshold := flag.Uint("new-threshold", 1, "signature threshold of the NEW set")
	notAfter := flag.Uint64("not-after-height", 0, "last block height at which the rotation may execute (required)")
	var newKeys, keys list
	flag.Var(&newKeys, "new-key", "NEW signer public key (secp256k1, hex); repeat in order")
	flag.Var(&keys, "key", "CURRENT signer as <index>=<private key hex>; repeat per approving signer")
	flag.Parse()

	if *sidechain == "" || len(newKeys) == 0 || len(keys) == 0 || *notAfter == 0 {
		must(fmt.Errorf("-sidechain-id, -not-after-height, at least one -new-key and at least one -key are required"))
	}

	anys := make([]*codectypes.Any, 0, len(newKeys))
	for _, h := range newKeys {
		raw, err := hex.DecodeString(h)
		must(err)
		a, err := codectypes.NewAnyWithValue(&secp256k1.PubKey{Key: raw})
		must(err)
		anys = append(anys, a)
	}

	sc := types.Sidechain{Id: *sidechain, SignerSetVersion: *version}
	signBytes, err := types.SignerRotationSignBytes(types.RotationSignDocFor(*chainID, sc, anys, uint32(*threshold), *notAfter))
	must(err)
	fmt.Fprintf(os.Stderr, "chain=%s sc=%s current_version=%d new_keys=%d new_threshold=%d not_after_height=%d\nsignbytes=%x\n",
		*chainID, *sidechain, *version, len(anys), *threshold, *notAfter, signBytes)
	fmt.Printf("--not-after-height %d \\\n", *notAfter)

	for _, k := range keys {
		idxStr, privHex, ok := strings.Cut(k, "=")
		if !ok {
			must(fmt.Errorf("bad -key %q, want <index>=<hex>", k))
		}
		idx, err := strconv.ParseUint(idxStr, 10, 32)
		must(err)
		raw, err := hex.DecodeString(privHex)
		must(err)
		priv := &secp256k1.PrivKey{Key: raw}
		sig, err := priv.Sign(signBytes)
		must(err)
		js, _ := json.Marshal(map[string]any{
			"signer_index": idx,
			"signature":    base64.StdEncoding.EncodeToString(sig),
		})
		fmt.Printf("--current-set-signatures '%s' \\\n", js)
	}
}
