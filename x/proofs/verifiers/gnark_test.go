package verifiers_test

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark/backend/groth16"
	"github.com/consensys/gnark/backend/plonk"
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/frontend/cs/r1cs"
	"github.com/consensys/gnark/frontend/cs/scs"
	"github.com/consensys/gnark/test/unsafekzg"
	"github.com/stretchr/testify/require"

	"github.com/lucidpay/lucidchain/x/proofs/types"
	"github.com/lucidpay/lucidchain/x/proofs/verifiers"
)

// bindingCircuit is the smallest circuit that follows the module's convention:
// the first two public variables are the binding limbs. A real circuit would
// also constrain those limbs to the state it proves things about.
type bindingCircuit struct {
	Hi     frontend.Variable `gnark:",public"`
	Lo     frontend.Variable `gnark:",public"`
	Secret frontend.Variable
}

func (c *bindingCircuit) Define(api frontend.API) error {
	api.AssertIsEqual(c.Secret, api.Add(c.Hi, c.Lo))
	return nil
}

type material struct {
	vk, proof, pub []byte
}

func assignment(b types.Binding) *bindingCircuit {
	hi, lo := b.Limbs()
	return &bindingCircuit{Hi: hi, Lo: lo, Secret: new(big.Int).Add(hi, lo)}
}

func publicInputs(t *testing.T, curve ecc.ID, b types.Binding) []byte {
	t.Helper()
	w, err := frontend.NewWitness(assignment(b), curve.ScalarField(), frontend.PublicOnly())
	require.NoError(t, err)
	bz, err := w.MarshalBinary()
	require.NoError(t, err)
	return bz
}

func proveGroth16(t *testing.T, curve ecc.ID, b types.Binding) material {
	t.Helper()
	ccs, err := frontend.Compile(curve.ScalarField(), r1cs.NewBuilder, &bindingCircuit{})
	require.NoError(t, err)
	pk, vk, err := groth16.Setup(ccs)
	require.NoError(t, err)
	full, err := frontend.NewWitness(assignment(b), curve.ScalarField())
	require.NoError(t, err)
	proof, err := groth16.Prove(ccs, pk, full)
	require.NoError(t, err)

	var vkBuf, proofBuf bytes.Buffer
	_, err = vk.WriteTo(&vkBuf)
	require.NoError(t, err)
	_, err = proof.WriteTo(&proofBuf)
	require.NoError(t, err)
	return material{vk: vkBuf.Bytes(), proof: proofBuf.Bytes(), pub: publicInputs(t, curve, b)}
}

func provePlonk(t *testing.T, curve ecc.ID, b types.Binding) material {
	t.Helper()
	ccs, err := frontend.Compile(curve.ScalarField(), scs.NewBuilder, &bindingCircuit{})
	require.NoError(t, err)
	// Test-only SRS: its trapdoor is known, so never use unsafekzg outside tests.
	srs, srsLagrange, err := unsafekzg.NewSRS(ccs)
	require.NoError(t, err)
	pk, vk, err := plonk.Setup(ccs, srs, srsLagrange)
	require.NoError(t, err)
	full, err := frontend.NewWitness(assignment(b), curve.ScalarField())
	require.NoError(t, err)
	proof, err := plonk.Prove(ccs, pk, full)
	require.NoError(t, err)

	var vkBuf, proofBuf bytes.Buffer
	_, err = vk.WriteTo(&vkBuf)
	require.NoError(t, err)
	_, err = proof.WriteTo(&proofBuf)
	require.NoError(t, err)
	return material{vk: vkBuf.Bytes(), proof: proofBuf.Bytes(), pub: publicInputs(t, curve, b)}
}

func TestDefaultRegistry(t *testing.T) {
	all := verifiers.Default()
	for _, id := range []string{
		verifiers.Groth16BN254, verifiers.Groth16BLS12381,
		verifiers.PlonkBN254, verifiers.PlonkBLS12381,
	} {
		_, ok := all[id]
		require.True(t, ok, id)
		require.NoError(t, types.ValidateID("proof_system_id", id), id)
	}
}

func TestGnarkVerifiers(t *testing.T) {
	cases := []struct {
		id    string
		curve ecc.ID
		prove func(t *testing.T, curve ecc.ID, b types.Binding) material
	}{
		{verifiers.Groth16BN254, ecc.BN254, proveGroth16},
		{verifiers.Groth16BLS12381, ecc.BLS12_381, proveGroth16},
		{verifiers.PlonkBN254, ecc.BN254, provePlonk},
		{verifiers.PlonkBLS12381, ecc.BLS12_381, provePlonk},
	}

	binding := types.ComputeBinding("lucidchain-1", "sc-1", 7, []byte("state-root"), []byte("checkpoint-hash"), "geofence.v1")
	other := types.ComputeBinding("lucidchain-1", "sc-1", 8, []byte("state-root"), []byte("checkpoint-hash"), "geofence.v1")

	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			v := verifiers.Default()[tc.id]
			m := tc.prove(t, tc.curve, binding)

			t.Run("verification key", func(t *testing.T) {
				require.NoError(t, v.ValidateKey(m.vk))
				require.Error(t, v.ValidateKey(nil))
				require.Error(t, v.ValidateKey([]byte("not a key")))
				require.Error(t, v.ValidateKey(append(bytes.Clone(m.vk), 0)), "trailing bytes")
				require.Error(t, v.ValidateKey(m.vk[:len(m.vk)/2]), "truncated")
			})

			t.Run("binding", func(t *testing.T) {
				require.NoError(t, v.CheckBinding(m.pub, binding))
				require.Error(t, v.CheckBinding(m.pub, other), "different checkpoint")
				require.Error(t, v.CheckBinding(nil, binding))
				require.Error(t, v.CheckBinding(append(bytes.Clone(m.pub), 0), binding), "non-canonical encoding")
			})

			t.Run("valid proof", func(t *testing.T) {
				require.NoError(t, v.Verify(m.vk, m.pub, m.proof))
			})

			t.Run("rejects", func(t *testing.T) {
				tampered := bytes.Clone(m.proof)
				tampered[len(tampered)/2] ^= 0xff
				require.Error(t, v.Verify(m.vk, m.pub, tampered), "tampered proof")

				require.Error(t, v.Verify(m.vk, m.pub, append(bytes.Clone(m.proof), 0)), "trailing proof bytes")
				require.Error(t, v.Verify(m.vk, m.pub, m.proof[:len(m.proof)/2]), "truncated proof")
				require.Error(t, v.Verify(m.vk, m.pub, nil), "empty proof")

				// A proof made for one binding does not verify under another's public inputs.
				require.Error(t, v.Verify(m.vk, publicInputs(t, tc.curve, other), m.proof), "wrong public inputs")

				require.Error(t, v.Verify(m.vk, append(bytes.Clone(m.pub), 0), m.proof), "non-canonical public inputs")
				require.Error(t, v.Verify([]byte("not a key"), m.pub, m.proof), "bad key")
			})
		})
	}
}
