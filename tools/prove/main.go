package main

import (
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark/backend/groth16"
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/frontend/cs/r1cs"
)

// MUST be identical to the circuit in cmd/gen-vk.
type Circuit struct {
	Hi frontend.Variable `gnark:",public"`
	Lo frontend.Variable `gnark:",public"`
	X  frontend.Variable
}

func (c *Circuit) Define(api frontend.API) error {
	api.AssertIsEqual(c.X, api.Add(c.Hi, c.Lo))
	return nil
}

func main() {
	pkPath := flag.String("pk", "keys/pk.bin", "proving key")
	vkPath := flag.String("vk", "keys/vk.bin", "verifying key (for a local self-check)")
	hiB64 := flag.String("hi", "", "binding hi, base64 from `query proofs binding`")
	loB64 := flag.String("lo", "", "binding lo, base64 from `query proofs binding`")
	out := flag.String("out", "keys", "output directory")
	flag.Parse()

	hi, lo := bigFromB64(*hiB64), bigFromB64(*loB64)
	field := ecc.BN254.ScalarField()

	ccs, err := frontend.Compile(field, r1cs.NewBuilder, &Circuit{})
	check(err)

	pk := groth16.NewProvingKey(ecc.BN254)
	readFile(*pkPath, pk.ReadFrom)
	vk := groth16.NewVerifyingKey(ecc.BN254)
	readFile(*vkPath, vk.ReadFrom)

	assignment := &Circuit{Hi: hi, Lo: lo, X: new(big.Int).Add(hi, lo)}
	w, err := frontend.NewWitness(assignment, field)
	check(err)
	pub, err := w.Public()
	check(err)

	proof, err := groth16.Prove(ccs, pk, w)
	check(err)
	// Fail here, not on chain, if pk and vk come from different setups.
	check(groth16.Verify(proof, vk, pub))

	var proofBuf = new(bytesBuffer)
	_, err = proof.WriteTo(proofBuf)
	check(err)
	pubBytes, err := pub.MarshalBinary() // the public witness only
	check(err)

	check(os.WriteFile(filepath.Join(*out, "proof.b64"), []byte(base64.StdEncoding.EncodeToString(proofBuf.b)), 0o644))
	check(os.WriteFile(filepath.Join(*out, "public.b64"), []byte(base64.StdEncoding.EncodeToString(pubBytes)), 0o644))
	fmt.Printf("proof %d bytes, public inputs %d bytes, local verify ok\n", len(proofBuf.b), len(pubBytes))
}

type bytesBuffer struct{ b []byte }

func (w *bytesBuffer) Write(p []byte) (int, error) { w.b = append(w.b, p...); return len(p), nil }

func bigFromB64(s string) *big.Int {
	raw, err := base64.StdEncoding.DecodeString(s)
	check(err)
	return new(big.Int).SetBytes(raw) // big-endian, as the binding query returns it
}

func readFile(path string, read func(io.Reader) (int64, error)) {
	f, err := os.Open(path)
	check(err)
	defer f.Close()
	_, err = read(f)
	check(err)
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
