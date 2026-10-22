// Command signcheckpoint signs a checkpoint with ML-DSA-65 signer keys.
//
// DEV/TESTNET ONLY: private keys are passed on the command line as the hex
// produced by `lucidchaind keys export <name> --unarmored-hex --unsafe`. For an
// ML-DSA-65 key that hex is the 4032-byte packed FIPS 204 private key
// (8064 hex characters), not a 32-byte seed.
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

	"github.com/cosmos/cosmos-sdk/crypto/keys/mldsa65"
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
	zkRoot := flag.String("zk-records-root", "", "records root, 32-byte hex (optional; NOT part of the quantum-resistant profile)")
	zkDepth := flag.Uint("zk-tree-depth", 0, "records tree depth (required with -zk-records-root)")
	derive := flag.Bool("derive-state-root", false, "derive state_root from the zk anchor and record count (required by the chain when an anchor is present); overrides -state-root")
	printRoot := flag.Bool("print-state-root", false, "with -derive-state-root: print the derived state_root (hex) and exit")
	var keys keyList
	flag.Var(&keys, "key", "signer as <index>=<ML-DSA-65 private key hex, 8064 chars>; repeat per signer")
	flag.Parse()

	sr, err := hex.DecodeString(*stateRoot)
	must(err)
	ph, err := hex.DecodeString(*prevHash)
	must(err)

	var anchor *types.ZkAnchor
	if *zkRoot != "" {
		fmt.Fprintln(os.Stderr, "warning: the MiMC-BN254 ZK anchor belongs to the removed Groth16/PLONK flow; "+
			"it is not part of the quantum-resistant profile")
		rr, err := hex.DecodeString(*zkRoot)
		must(err)
		anchor = &types.ZkAnchor{
			Scheme:           types.ZkHashScheme_ZK_HASH_SCHEME_MIMC_BN254,
			RecordsRoot:      rr,
			RecordsTreeDepth: uint32(*zkDepth),
		}
		must(types.ValidateZkAnchor(anchor)) // fail early, off-chain
	}
	if *printRoot && !*derive {
		must(fmt.Errorf("-print-state-root requires -derive-state-root"))
	}
	if *derive {
		if anchor == nil {
			must(fmt.Errorf("-derive-state-root needs -zk-records-root and -zk-tree-depth"))
		}
		sr = types.DeriveStateRoot(anchor, *records)
		if *printRoot {
			fmt.Println(hex.EncodeToString(sr))
			return
		}
		fmt.Fprintf(os.Stderr, "derived state_root=%x\n", sr)
	}
	must(types.ValidateAnchorBinding(anchor, sr, *records)) // same rule the chain applies

	signBytes, err := types.CheckpointSignBytes(&types.CheckpointSignDoc{
		ChainId:                *chainID,
		SidechainId:            *sidechain,
		LcSequence:             *seq,
		StateRoot:              sr,
		PreviousCheckpointHash: ph,
		RecordCount:            *records,
		SignerSetVersion:       *version,
		DataPointer:            *pointer,
		ZkAnchor:               anchor,
	})
	must(err)

	fmt.Fprintf(os.Stderr, "chain=%s sc=%s seq=%d root=%x prev=%x rec=%d ver=%d ptr=%q\nsignbytes=%x\n",
		*chainID, *sidechain, *seq, sr, ph, *records, *version, *pointer, signBytes)

	for _, k := range keys {
		idxStr, privHex, ok := strings.Cut(k, "=")
		if !ok {
			must(fmt.Errorf("bad -key %q, want <index>=<hex>", k))
		}
		idx, err := strconv.ParseUint(idxStr, 10, 32)
		must(err)
		raw, err := hex.DecodeString(privHex)
		must(err)

		// Validates the length (4032 bytes) and the FIPS 204 packed encoding.
		priv, err := mldsa65.NewPrivateKeyFromBytes(raw)
		must(err)

		// ML-DSA signs the raw message (no pre-hash), deterministic, empty context.
		sig, err := priv.Sign(signBytes)
		must(err)

		// Fail early, off-chain: the signature must verify under the derived pubkey.
		pub := priv.PubKey()
		if pub == nil || !pub.VerifySignature(signBytes, sig) {
			must(fmt.Errorf("signer %d: signature does not verify against its own public key", idx))
		}

		fmt.Fprintf(os.Stderr, "signer %d pubkey (%d bytes): %s\nsigner %d signature: %d bytes\n",
			idx, len(pub.Bytes()), base64.StdEncoding.EncodeToString(pub.Bytes()), idx, len(sig))

		js, _ := json.Marshal(map[string]any{
			"signer_index": idx,
			"signature":    base64.StdEncoding.EncodeToString(sig),
		})
		fmt.Printf("--signatures '%s' \\\n", js)
	}

	if anchor != nil {
		js, _ := json.Marshal(map[string]any{
			"scheme":             "ZK_HASH_SCHEME_MIMC_BN254",
			"records_root":       base64.StdEncoding.EncodeToString(anchor.RecordsRoot),
			"records_tree_depth": anchor.RecordsTreeDepth,
		})
		fmt.Printf("--zk-anchor '%s' \\\n", js)
	}
}
