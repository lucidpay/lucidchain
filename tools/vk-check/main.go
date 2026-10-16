// cmd/vkcheck/main.go (temporary)
package main

import (
	"bytes"
	"fmt"
	"os"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark/backend/groth16"
	"github.com/consensys/gnark/backend/plonk"
)

func main() {
	bz, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	g := groth16.NewVerifyingKey(ecc.BN254)
	if n, err := g.ReadFrom(bytes.NewReader(bz)); err == nil && n == int64(len(bz)) {
		fmt.Println("groth16, public witness count:", g.NbPublicWitness())
	} else {
		fmt.Println("not groth16:", err)
	}
	p := plonk.NewVerifyingKey(ecc.BN254)
	if n, err := p.ReadFrom(bytes.NewReader(bz)); err == nil && n == int64(len(bz)) {
		fmt.Println("plonk, public witness count:", p.NbPublicWitness())
	} else {
		fmt.Println("not plonk:", err)
	}
}
