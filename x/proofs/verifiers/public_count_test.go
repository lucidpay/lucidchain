package verifiers

import (
	"testing"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark/backend/groth16"
	"github.com/consensys/gnark/backend/plonk"
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/frontend/cs/r1cs"
	"github.com/consensys/gnark/frontend/cs/scs"
	"github.com/consensys/gnark/test/unsafekzg"
	"github.com/stretchr/testify/require"
)

type countCircuit struct {
	A, B, C frontend.Variable `gnark:",public"`
	X       frontend.Variable
}

func (c *countCircuit) Define(api frontend.API) error {
	api.AssertIsEqual(c.X, api.Add(c.A, c.B, c.C))
	return nil
}

func TestPublicInputCountConvention(t *testing.T) {
	field := ecc.BN254.ScalarField()

	g16, err := frontend.Compile(field, r1cs.NewBuilder, &countCircuit{})
	require.NoError(t, err)
	_, gvk, err := groth16.Setup(g16)
	require.NoError(t, err)
	t.Logf("groth16 NbPublicWitness for 3 public inputs: %d", gvk.NbPublicWitness())
	require.Equal(t, 3, publicInputCount(gvk, schemeGroth16))

	pl, err := frontend.Compile(field, scs.NewBuilder, &countCircuit{})
	require.NoError(t, err)
	srs, lag, err := unsafekzg.NewSRS(pl)
	require.NoError(t, err)
	_, pvk, err := plonk.Setup(pl, srs, lag)
	require.NoError(t, err)
	t.Logf("plonk NbPublicWitness for 3 public inputs: %d", pvk.NbPublicWitness())
	require.Equal(t, 3, publicInputCount(pvk, schemePlonk))
}
