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

	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	"github.com/lucidpay/lucidchain/x/checkpoint/types"
)

type keyList []string

func (k *keyList) String() string     { return strings.Join(*k, ",") }
func (k *keyList) Set(v string) error { *k = append(*k, v); return nil }

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func main() {
	chainID := flag.String("chain-id", "my-testnet-1", "chain id")
	sidechain := flag.String("sidechain-id", "", "sidechain id")
	seq := flag.Uint64("lc-sequence", 1, "lc_sequence")
	stateRoot := flag.String("state-root", "", "state root (hex)")
	prevHash := flag.String("previous-hash", "", "previous checkpoint hash (hex, empty for seq 1)")
	records := flag.Uint64("record-count", 0, "record count")
	pointer := flag.String("data-pointer", "", "data pointer")
	version := flag.Uint64("signer-set-version", 1, "signer set version")
	var keys keyList
	flag.Var(&keys, "key", "signer as <index>=<private key hex>; repeat per signer")
	flag.Parse()

	sr, err := hex.DecodeString(*stateRoot)
	must(err)
	ph, err := hex.DecodeString(*prevHash)
	must(err)

	signBytes, err := types.CheckpointSignBytes(&types.CheckpointSignDoc{
		ChainId:                *chainID,
		SidechainId:            *sidechain,
		LcSequence:             *seq,
		StateRoot:              sr,
		PreviousCheckpointHash: ph,
		RecordCount:            *records,
		SignerSetVersion:       *version,
		DataPointer:            *pointer,
	})

	fmt.Fprintf(os.Stderr, "chain=%s sc=%s seq=%d root=%x prev=%x rec=%d ver=%d ptr=%q\nsignbytes=%x\n",
		*chainID, *sidechain, *seq, sr, ph, *records, *version, *pointer, signBytes)
	must(err)

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

		fmt.Fprintf(os.Stderr, "signer %d pubkey: %s\n", idx,
			base64.StdEncoding.EncodeToString(priv.PubKey().Bytes()))

		js, _ := json.Marshal(map[string]any{
			"signer_index": idx,
			"signature":    base64.StdEncoding.EncodeToString(sig),
		})
		fmt.Printf("--signatures '%s' \\\n", js)
	}
}
