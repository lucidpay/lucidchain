package verifiers

import (
	"github.com/consensys/gnark-crypto/ecc"

	"github.com/lucidpay/lucidchain/x/proofs/types"
)

// Proof system ids of the verifiers compiled in by Default.
const (
	Groth16BN254    = "groth16-bn254"
	Groth16BLS12381 = "groth16-bls12-381"
	PlonkBN254      = "plonk-bn254"
	PlonkBLS12381   = "plonk-bls12-381"
)

// Default returns the verifiers compiled into the binary, keyed by
// proof_system_id. Governance can only register these ids.
//
// RISC Zero (or another zkVM) is deliberately not listed: its Groth16 receipts
// can be checked with the Groth16 verifier here, but only once an adapter
// derives the public inputs from the receipt claim (image id and journal). See
// the notes delivered with this module.
//
// To add a proof system, implement types.ProofVerifier and add it here (or
// build your own map and pass it to keeper.NewKeeper).
func Default() map[string]types.ProofVerifier {
	return map[string]types.ProofVerifier{
		Groth16BN254:    newGnark(ecc.BN254, schemeGroth16),
		Groth16BLS12381: newGnark(ecc.BLS12_381, schemeGroth16),
		PlonkBN254:      newGnark(ecc.BN254, schemePlonk),
		PlonkBLS12381:   newGnark(ecc.BLS12_381, schemePlonk),
	}
}
