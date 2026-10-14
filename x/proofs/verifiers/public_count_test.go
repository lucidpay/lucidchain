package verifiers

import (
	"bytes"
	"io"
	"testing"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark/backend/groth16"
	"github.com/consensys/gnark/backend/plonk"
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/frontend/cs/r1cs"
	"github.com/consensys/gnark/frontend/cs/scs"
	"github.com/consensys/gnark/std/rangecheck"
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

// commitCountCircuit is countCircuit plus a range check, which makes gnark add
// a commitment (as the zkgeo circuit does).
type commitCountCircuit struct {
	A, B, C frontend.Variable `gnark:",public"`
	X       frontend.Variable
}

func (c *commitCountCircuit) Define(api frontend.API) error {
	rangecheck.New(api).Check(c.X, 16)
	api.AssertIsEqual(c.X, api.Add(c.A, c.B, c.C))
	return nil
}

func keyBytes(t *testing.T, vk io.WriterTo) []byte {
	t.Helper()
	var buf bytes.Buffer
	_, err := vk.WriteTo(&buf)
	require.NoError(t, err)
	return buf.Bytes()
}

// TestPublicInputCountConvention checks that a key for 3 public inputs counts
// as 3, read back from its bytes as the chain does, with and without a
// commitment in the circuit.
func TestPublicInputCountConvention(t *testing.T) {
	circuits := map[string]frontend.Circuit{
		"no commitment":   &countCircuit{},
		"with commitment": &commitCountCircuit{},
	}
	for _, curve := range []ecc.ID{ecc.BN254, ecc.BLS12_381} {
		field := curve.ScalarField()
		for name, circuit := range circuits {
			t.Run(curve.String()+"/groth16/"+name, func(t *testing.T) {
				ccs, err := frontend.Compile(field, r1cs.NewBuilder, circuit)
				require.NoError(t, err)
				_, vk, err := groth16.Setup(ccs)
				require.NoError(t, err)

				n, err := newGnark(curve, schemeGroth16).PublicInputCount(keyBytes(t, vk))
				require.NoError(t, err)
				require.Equal(t, 3, n)
			})
			t.Run(curve.String()+"/plonk/"+name, func(t *testing.T) {
				ccs, err := frontend.Compile(field, scs.NewBuilder, circuit)
				require.NoError(t, err)
				srs, lag, err := unsafekzg.NewSRS(ccs)
				require.NoError(t, err)
				_, vk, err := plonk.Setup(ccs, srs, lag)
				require.NoError(t, err)

				n, err := newGnark(curve, schemePlonk).PublicInputCount(keyBytes(t, vk))
				require.NoError(t, err)
				require.Equal(t, 3, n)
			})
		}
	}
}
