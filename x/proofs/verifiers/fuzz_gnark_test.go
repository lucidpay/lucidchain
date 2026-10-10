package verifiers

import (
	"bytes"
	"io"
	"runtime"
	"testing"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark/backend/groth16"
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/frontend/cs/r1cs"
	"github.com/stretchr/testify/require"

	"github.com/lucidpay/lucidchain/x/proofs/types"
)

// maxFuzzAlloc bounds the memory one verifier call may allocate for a <=1 MiB
// input. Header fields in gnark encodings are attacker-controlled lengths.
const maxFuzzAlloc = 64 << 20

type fuzzCircuit struct {
	Hi, Lo frontend.Variable `gnark:",public"`
	X      frontend.Variable
}

func (c *fuzzCircuit) Define(api frontend.API) error {
	api.AssertIsEqual(api.Mul(c.X, c.X), api.Add(c.Hi, c.Lo))
	return nil
}

// groth16Seeds builds a real vk, public witness and proof (BN254).
func groth16Seeds(tb testing.TB) (vk, pub, proof []byte) {
	tb.Helper()
	field := ecc.BN254.ScalarField()
	ccs, err := frontend.Compile(field, r1cs.NewBuilder, &fuzzCircuit{})
	require.NoError(tb, err)
	pk, vkey, err := groth16.Setup(ccs)
	require.NoError(tb, err)
	w, err := frontend.NewWitness(&fuzzCircuit{Hi: 3, Lo: 6, X: 3}, field)
	require.NoError(tb, err)
	pw, err := w.Public()
	require.NoError(tb, err)
	p, err := groth16.Prove(ccs, pk, w)
	require.NoError(tb, err)

	var vb, pb bytes.Buffer
	_, err = vkey.WriteTo(&vb)
	require.NoError(tb, err)
	_, err = p.WriteTo(&pb)
	require.NoError(tb, err)
	pubBytes, err := pw.MarshalBinary()
	require.NoError(tb, err)
	return vb.Bytes(), pubBytes, pb.Bytes()
}

func allocDelta(fn func()) uint64 {
	var a, b runtime.MemStats
	runtime.ReadMemStats(&a)
	fn()
	runtime.ReadMemStats(&b)
	return b.TotalAlloc - a.TotalAlloc
}

func TestGroth16Sanity(t *testing.T) {
	vk, pub, proof := groth16Seeds(t)
	v := newGnark(ecc.BN254, schemeGroth16)
	require.NoError(t, v.ValidateKey(vk))
	require.NoError(t, v.Verify(vk, pub, proof))
	require.Error(t, v.Verify(vk, append(bytes.Clone(pub), 0), proof), "trailing public-input byte")
	require.Error(t, v.Verify(vk, pub, append(bytes.Clone(proof), 0)), "trailing proof byte")
	bad := bytes.Clone(proof)
	bad[len(bad)-1] ^= 1
	require.Error(t, v.Verify(vk, pub, bad))
}

// The doc comment promises a single valid encoding per proof. gnark's reader
// may also accept the uncompressed form; if this fails, re-encode with
// proof.WriteTo after readExact in Verify and require equality with the input.
func TestGroth16ProofEncodingIsCanonical(t *testing.T) {
	vk, pub, proof := groth16Seeds(t)
	p := groth16.NewProof(ecc.BN254)
	require.NoError(t, readExact(p, proof))
	raw, ok := p.(interface {
		WriteRawTo(io.Writer) (int64, error)
	})
	if !ok {
		t.Skip("proof type has no raw (uncompressed) encoding")
	}
	var buf bytes.Buffer
	_, err := raw.WriteRawTo(&buf)
	require.NoError(t, err)
	if bytes.Equal(buf.Bytes(), proof) {
		t.Skip("raw and compressed encodings coincide")
	}
	err = newGnark(ecc.BN254, schemeGroth16).Verify(vk, pub, buf.Bytes())
	require.Error(t, err, "alternate encoding of a valid proof was accepted")
}

var gnarkVerifiers = []gnarkVerifier{
	newGnark(ecc.BN254, schemeGroth16),
	newGnark(ecc.BLS12_381, schemeGroth16),
	newGnark(ecc.BN254, schemePlonk),
	newGnark(ecc.BLS12_381, schemePlonk),
}

// No input may panic, over-allocate, or be accepted in a non-canonical form.
func FuzzGnarkVerifier(f *testing.F) {
	vk, pub, proof := groth16Seeds(f)
	f.Add(uint8(0), vk, pub, proof)
	f.Add(uint8(0), []byte{}, []byte{}, []byte{})
	f.Fuzz(func(t *testing.T, which uint8, vkB, pubB, proofB []byte) {
		if len(vkB)+len(pubB)+len(proofB) > 1<<20 {
			t.Skip()
		}
		v := gnarkVerifiers[int(which)%len(gnarkVerifiers)]
		var verr error
		alloc := allocDelta(func() {
			_ = v.ValidateKey(vkB)
			_ = v.CheckBinding(pubB, types.Binding{})
			verr = v.Verify(vkB, pubB, proofB)
		})
		if alloc > maxFuzzAlloc {
			t.Fatalf("allocated %d bytes for a %d byte input", alloc, len(vkB)+len(pubB)+len(proofB))
		}
		if verr == nil && v.scheme == schemeGroth16 {
			p := groth16.NewProof(v.curve)
			require.NoError(t, readExact(p, proofB))
			var buf bytes.Buffer
			_, err := p.WriteTo(&buf)
			require.NoError(t, err)
			require.Equal(t, buf.Bytes(), proofB, "accepted a non-canonical proof encoding")
		}
	})
}
