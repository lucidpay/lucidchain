// Command batchroot prints the records root, record count and tree depth of a
// batch.json, in the encodings used by the checkpoint's zk_anchor, so a
// checkpoint can be built from (or compared with) the data the prover uses.
//
// Place at cmd/batchroot/main.go in the repo and run from the repo root:
//
//	go run ./cmd/batchroot -batch ~/tmp_workarea/json/batch.json -records-depth 5
//
// NOT COMPILED: written against tools/zkgeo (RecordLeaf, BuildTree, Tree.Root)
// as uploaded. Fix imports if your module path differs.
package main

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"math/big"
	"os"
	"strings"

	"github.com/consensys/gnark-crypto/ecc/bn254/fr"

	"github.com/lucidpay/lucidchain/tools/zkgeo"
)

type batchFile struct {
	Records []struct {
		X    uint32 `json:"x"`
		Y    uint32 `json:"y"`
		T    uint64 `json:"t"`
		Salt string `json:"salt"`
	} `json:"records"`
}

func parseSalt(s string) (fr.Element, error) {
	var e fr.Element
	n, ok := new(big.Int).SetString(strings.TrimPrefix(s, "0x"), 16)
	if !ok {
		return e, fmt.Errorf("salt %q is not hex", s)
	}
	if n.Cmp(fr.Modulus()) >= 0 {
		return e, fmt.Errorf("salt %q is not below the BN254 modulus", s)
	}
	e.SetBigInt(n)
	return e, nil
}

func main() {
	path := flag.String("batch", "batch.json", "batch file")
	depth := flag.Int("records-depth", 5, "records tree depth (must match the circuit)")
	flag.Parse()

	bz, err := os.ReadFile(*path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var b batchFile
	if err := json.Unmarshal(bz, &b); err != nil {
		fmt.Fprintln(os.Stderr, "batch:", err)
		os.Exit(1)
	}

	leaves := make([]fr.Element, len(b.Records))
	for i, r := range b.Records {
		salt, err := parseSalt(r.Salt)
		if err != nil {
			fmt.Fprintf(os.Stderr, "record %d: %v\n", i, err)
			os.Exit(1)
		}
		leaves[i] = zkgeo.RecordLeaf(r.X, r.Y, r.T, salt)
	}
	tree, err := zkgeo.BuildTree(leaves, *depth)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	root := tree.Root()
	rb := root.Bytes() // 32 bytes, big-endian

	fmt.Printf("record_count:       %d\n", len(b.Records))
	fmt.Printf("records_tree_depth: %d\n", *depth)
	fmt.Printf("records_root (hex): %s\n", hex.EncodeToString(rb[:]))
	fmt.Printf("records_root (b64): %s\n", base64.StdEncoding.EncodeToString(rb[:]))
}
