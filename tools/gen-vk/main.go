package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark/backend/groth16"
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/frontend/cs/r1cs"
)

// Hi and Lo MUST be the first two public fields (the checkpoint binding).
type Circuit struct {
	Hi frontend.Variable `gnark:",public"`
	Lo frontend.Variable `gnark:",public"`
	X  frontend.Variable // secret; placeholder statement X == Hi + Lo
}

func (c *Circuit) Define(api frontend.API) error {
	api.AssertIsEqual(c.X, api.Add(c.Hi, c.Lo))
	return nil
}

func main() {
	ccs, err := frontend.Compile(ecc.BN254.ScalarField(), r1cs.NewBuilder, &Circuit{})
	check(err)
	// DEV ONLY: this setup is single-party. Anyone holding its secrets can forge proofs.
	pk, vk, err := groth16.Setup(ccs)
	check(err)

	var vkBuf, pkBuf bytes.Buffer
	_, err = vk.WriteTo(&vkBuf) // the exact encoding the chain expects
	check(err)
	_, err = pk.WriteTo(&pkBuf)
	check(err)
	check(os.WriteFile("vk.bin", vkBuf.Bytes(), 0o600))
	check(os.WriteFile("pk.bin", pkBuf.Bytes(), 0o600))
	fmt.Println(base64.StdEncoding.EncodeToString(vkBuf.Bytes()))
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
