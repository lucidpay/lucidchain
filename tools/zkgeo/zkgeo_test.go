package zkgeo

import (
	"bytes"
	crand "crypto/rand"
	"math/big"
	"testing"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark-crypto/ecc/bn254/fr"
	"github.com/consensys/gnark/backend/groth16"
	"github.com/consensys/gnark/backend/plonk"
	"github.com/consensys/gnark/backend/witness"
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/frontend/cs/r1cs"
	"github.com/consensys/gnark/frontend/cs/scs"
	"github.com/consensys/gnark/test"
	"github.com/consensys/gnark/test/unsafekzg"
	"github.com/stretchr/testify/require"
)

var testCfg = Config{CellShift: 14, Capacity: 8, RecordsDepth: 5, AreaDepth: 4}

const (
	winStart = uint64(1_700_000_000)
	winEnd   = uint64(1_700_003_600)
)

func randFr() fr.Element {
	var e fr.Element
	_, _ = e.SetRandom()
	return e
}

func rand128(t *testing.T) *big.Int {
	t.Helper()
	v, err := crand.Int(crand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	require.NoError(t, err)
	return v
}

// fixture returns inputs with n records, all inside a 2x2-cell area and the window.
func fixture(t *testing.T, n int) (BatchInputs, [4][2]uint32) {
	t.Helper()
	ax, ay := EncodeLonLat(55.27, 25.20)
	step := uint32(1) << testCfg.CellShift
	pts := [4][2]uint32{{ax, ay}, {ax + step, ay}, {ax, ay + step}, {ax + step, ay + step}}
	cells := make([]uint64, len(pts))
	for i, p := range pts {
		cells[i] = CellID(p[0], p[1], testCfg.CellShift)
	}
	area, err := NewArea(cells, testCfg.AreaDepth, randFr())
	require.NoError(t, err)

	recs := make([]Record, n)
	for i := range recs {
		p := pts[i%4]
		recs[i] = Record{X: p[0], Y: p[1], T: winStart + 10*uint64(i+1), Salt: randFr()}
	}
	return BatchInputs{
		Records: recs, Area: area, WindowStart: winStart, WindowEnd: winEnd,
		BindingHi: rand128(t), BindingLo: rand128(t),
	}, pts
}

func build(t *testing.T, in BatchInputs) *BatchCircuit {
	t.Helper()
	a, err := BuildAssignment(testCfg, in)
	require.NoError(t, err)
	return a
}

func solve(a *BatchCircuit) error {
	return test.IsSolved(NewCircuit(testCfg), a, ecc.BN254.ScalarField())
}

func TestValidBatches(t *testing.T) {
	for _, n := range []int{0, 1, 5, 8} {
		in, _ := fixture(t, n)
		require.NoError(t, solve(build(t, in)), "n=%d", n)
	}
}

func TestInvalidBatches(t *testing.T) {
	step := uint32(1) << testCfg.CellShift

	t.Run("record outside area", func(t *testing.T) {
		in, pts := fixture(t, 5)
		in.Records[2].X = pts[0][0] + 100*step
		in.AllowOutOfArea = true
		require.Error(t, solve(build(t, in)))
	})
	t.Run("record before window", func(t *testing.T) {
		in, _ := fixture(t, 5)
		in.Records[1].T = winStart - 1
		require.Error(t, solve(build(t, in)))
	})
	t.Run("record after window", func(t *testing.T) {
		in, _ := fixture(t, 5)
		in.Records[4].T = winEnd + 1
		require.Error(t, solve(build(t, in)))
	})

	tampers := map[string]func(a *BatchCircuit){
		// operator claims fewer records than the checkpoint holds: leaf 4 becomes padding
		"understated count":  func(a *BatchCircuit) { a.Records[4].Active = 0; a.RecordCount = 4 },
		"overstated count":   func(a *BatchCircuit) { a.RecordCount = 6 },
		"non-prefix active":  func(a *BatchCircuit) { a.Records[1].Active = 0 },
		"wrong records root": func(a *BatchCircuit) { a.RecordsRoot = big.NewInt(12345) },
		"wrong area commit":  func(a *BatchCircuit) { a.AreaCommitment = big.NewInt(12345) },
		"altered record":     func(a *BatchCircuit) { a.Records[0].T = winStart + 999 },
	}
	for name, tamper := range tampers {
		t.Run(name, func(t *testing.T) {
			in, _ := fixture(t, 5)
			a := build(t, in)
			tamper(a)
			require.Error(t, solve(a))
		})
	}
}

// checkLayout mirrors what x/proofs' gnark verifier does with the public inputs.
func checkLayout(t *testing.T, pub witness.Witness, in BatchInputs, count int) {
	t.Helper()
	bz, err := pub.MarshalBinary()
	require.NoError(t, err)
	require.Len(t, bz, 12+NumPublic*32) // header + 7 elements

	w, err := witness.New(ecc.BN254.ScalarField())
	require.NoError(t, err)
	require.NoError(t, w.UnmarshalBinary(bz))
	again, err := w.MarshalBinary()
	require.NoError(t, err)
	require.True(t, bytes.Equal(bz, again), "public inputs must re-encode identically")

	vec := w.Vector().(fr.Vector)
	require.Equal(t, in.BindingHi, vec[PubBindingHi].BigInt(new(big.Int)))
	require.Equal(t, in.BindingLo, vec[PubBindingLo].BigInt(new(big.Int)))
	require.Equal(t, uint64(count), vec[PubRecordCount].Uint64())
	require.Equal(t, winStart, vec[PubWindowStart].Uint64())
	require.Equal(t, winEnd, vec[PubWindowEnd].Uint64())
}

func TestGroth16EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a (dev-only) trusted setup")
	}
	in, _ := fixture(t, 5)
	a := build(t, in)

	ccs, err := frontend.Compile(ecc.BN254.ScalarField(), r1cs.NewBuilder, NewCircuit(testCfg))
	require.NoError(t, err)
	t.Logf("groth16 constraints: %d", ccs.GetNbConstraints())

	pk, vk, err := groth16.Setup(ccs) // INSECURE single-party setup
	require.NoError(t, err)
	// gnark's Groth16 key counts the implicit constant-one wire too; PLONK does not.
	require.Equal(t, NumPublic+1, vk.NbPublicWitness())
	full, err := frontend.NewWitness(a, ecc.BN254.ScalarField())
	require.NoError(t, err)
	pub, err := full.Public()
	require.NoError(t, err)
	proof, err := groth16.Prove(ccs, pk, full)
	require.NoError(t, err)
	require.NoError(t, groth16.Verify(proof, vk, pub))
	checkLayout(t, pub, in, 5)

	// The proof must have a single canonical encoding (the chain re-encodes and compares).
	var b1, b2 bytes.Buffer
	_, err = proof.WriteTo(&b1)
	require.NoError(t, err)
	p2 := groth16.NewProof(ecc.BN254)
	_, err = p2.ReadFrom(bytes.NewReader(b1.Bytes()))
	require.NoError(t, err)
	_, err = p2.WriteTo(&b2)
	require.NoError(t, err)
	require.Equal(t, b1.Bytes(), b2.Bytes())

	// Every public input must be bound to the proof.
	mutations := map[string]func(a *BatchCircuit){
		"binding hi":   func(a *BatchCircuit) { a.BindingHi = new(big.Int).Add(in.BindingHi, big.NewInt(1)) },
		"binding lo":   func(a *BatchCircuit) { a.BindingLo = new(big.Int).Add(in.BindingLo, big.NewInt(1)) },
		"records root": func(a *BatchCircuit) { a.RecordsRoot = big.NewInt(1) },
		"area":         func(a *BatchCircuit) { a.AreaCommitment = big.NewInt(1) },
		"window start": func(a *BatchCircuit) { a.WindowStart = winStart + 1 },
		"window end":   func(a *BatchCircuit) { a.WindowEnd = winEnd + 1 },
		"count":        func(a *BatchCircuit) { a.RecordCount = uint64(4) },
	}
	for name, mutate := range mutations {
		bad := build(t, in)
		mutate(bad)
		badPub, err := frontend.NewWitness(bad, ecc.BN254.ScalarField(), frontend.PublicOnly())
		require.NoError(t, err)
		require.Error(t, groth16.Verify(proof, vk, badPub), name)
	}
}

func TestPlonkEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a (dev-only) KZG setup")
	}
	in, _ := fixture(t, 5)
	a := build(t, in)

	ccs, err := frontend.Compile(ecc.BN254.ScalarField(), scs.NewBuilder, NewCircuit(testCfg))
	require.NoError(t, err)
	t.Logf("plonk constraints: %d", ccs.GetNbConstraints())

	srs, srsLagrange, err := unsafekzg.NewSRS(ccs) // INSECURE: dev only
	require.NoError(t, err)
	pk, vk, err := plonk.Setup(ccs, srs, srsLagrange)
	require.NoError(t, err)
	require.Equal(t, NumPublic, vk.NbPublicWitness())

	full, err := frontend.NewWitness(a, ecc.BN254.ScalarField())
	require.NoError(t, err)
	pub, err := full.Public()
	require.NoError(t, err)
	proof, err := plonk.Prove(ccs, pk, full)
	require.NoError(t, err)
	require.NoError(t, plonk.Verify(proof, vk, pub))
	checkLayout(t, pub, in, 5)

	bad := build(t, in)
	bad.BindingHi = new(big.Int).Add(in.BindingHi, big.NewInt(1))
	badPub, err := frontend.NewWitness(bad, ecc.BN254.ScalarField(), frontend.PublicOnly())
	require.NoError(t, err)
	require.Error(t, plonk.Verify(proof, vk, badPub))
}
