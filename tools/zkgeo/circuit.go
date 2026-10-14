// Package zkgeo implements the operator-side "batch geofence" circuit.
//
// Statement (curve-agnostic: compiles for groth16/plonk on BN254 or BLS12-381):
//
//	"Every record committed under RecordsRoot (there are exactly RecordCount of
//	 them, at leaf positions 0..RecordCount-1) lies in a grid cell of the area
//	 committed to by AreaCommitment, and has a timestamp in
//	 [WindowStart, WindowEnd]."
//
// Revealed: the 7 public inputs below. Hidden: every X, Y, T, the area, the cells.
//
// The public-input ORDER is an on-chain contract: x/proofs reads inputs 0 and 1
// as the checkpoint binding limbs, and (with the integration patch) checks
// RecordsRoot and RecordCount against the checkpoint.
package zkgeo

import (
	"errors"
	"math/big"

	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/std/hash"
	"github.com/consensys/gnark/std/hash/mimc"
	"github.com/consensys/gnark/std/rangecheck"
)

const (
	// CoordBits is the fixed-point width of X and Y (see EncodeLonLat).
	CoordBits = 32
	// TimeBits bounds unix timestamps. The window checks rely on 2^TimeBits
	// being tiny compared to the field modulus.
	TimeBits = 40
)

// Domain-separation tags. Part of the circuit: never reorder or reuse.
const (
	tagRecord uint64 = iota + 1
	tagCell
	tagNode
	tagArea
)

// Public input indices (the declaration order of the public fields below).
const (
	PubBindingHi = iota
	PubBindingLo
	PubRecordsRoot
	PubAreaCommitment
	PubWindowStart
	PubWindowEnd
	PubRecordCount
	NumPublic
)

// RecordWitness is one batch slot. Inactive slots (Active == 0) are padding and
// must be all-zero.
type RecordWitness struct {
	Active        frontend.Variable
	X, Y, T, Salt frontend.Variable
	AreaSiblings  []frontend.Variable
	AreaBits      []frontend.Variable // direction bits; soundness comes from the root
}

// BatchCircuit is the circuit. Capacity = len(Records) (a power of two).
type BatchCircuit struct {
	CellShift int `gnark:"-"` // compile-time constant, not part of the witness

	// ---- public (ORDER MATTERS, see Pub* constants) ----
	BindingHi, BindingLo frontend.Variable `gnark:",public"` // checkpoint binding limbs, set by x/proofs
	RecordsRoot          frontend.Variable `gnark:",public"` // ZK-friendly records root from the checkpoint
	AreaCommitment       frontend.Variable `gnark:",public"` // MiMC(tagArea, AreaRoot, AreaSalt)
	WindowStart          frontend.Variable `gnark:",public"`
	WindowEnd            frontend.Variable `gnark:",public"`
	RecordCount          frontend.Variable `gnark:",public"` // must equal the checkpoint's record_count

	// ---- private ----
	AreaRoot, AreaSalt frontend.Variable
	Records            []RecordWitness
	// Siblings above the first-Capacity-leaves subtree (chunk 0): len = RecordsDepth - log2(Capacity).
	UpperSiblings []frontend.Variable
}

func hashTagged(h hash.FieldHasher, tag uint64, in ...frontend.Variable) frontend.Variable {
	h.Reset()
	h.Write(tag)
	h.Write(in...)
	return h.Sum()
}

// computeRoot folds a Merkle path. Direction bits are private witnesses.
func computeRoot(api frontend.API, h hash.FieldHasher, leaf frontend.Variable, siblings, dirs []frontend.Variable) frontend.Variable {
	cur := leaf
	for i := range siblings {
		api.AssertIsBoolean(dirs[i])
		left := api.Select(dirs[i], siblings[i], cur)
		right := api.Select(dirs[i], cur, siblings[i])
		cur = hashTagged(h, tagNode, left, right)
	}
	return cur
}

// Define declares the circuit logic.
func (c *BatchCircuit) Define(api frontend.API) error {
	n := len(c.Records)
	if n == 0 || n&(n-1) != 0 {
		return errors.New("zkgeo: capacity must be a power of two")
	}
	if c.CellShift < 0 || c.CellShift >= CoordBits {
		return errors.New("zkgeo: CellShift out of range")
	}
	areaDepth := len(c.Records[0].AreaSiblings)
	for i := range c.Records {
		if len(c.Records[i].AreaSiblings) != areaDepth || len(c.Records[i].AreaBits) != areaDepth {
			return errors.New("zkgeo: inconsistent area path length")
		}
	}

	h, err := mimc.NewMiMC(api)
	if err != nil {
		return err
	}
	rc := rangecheck.New(api)
	// WindowStart/End < 2^TimeBits is what makes "negative difference wraps and fails" sound.
	rc.Check(c.WindowStart, TimeBits)
	rc.Check(c.WindowEnd, TimeBits)

	cellMul := new(big.Int).Lsh(big.NewInt(1), uint(CoordBits-c.CellShift))
	leaves := make([]frontend.Variable, n)
	var count frontend.Variable = 0

	for j := range c.Records {
		r := &c.Records[j]

		// Active slots form a prefix: active[j+1] <= active[j]. Together with
		// sum(active) == RecordCount this pins exactly the first RecordCount leaves.
		api.AssertIsBoolean(r.Active)
		if j > 0 {
			api.AssertIsEqual(api.Mul(r.Active, api.Sub(1, c.Records[j-1].Active)), 0)
		}
		count = api.Add(count, r.Active)

		// Leaf of the sidechain's records tree; padding leaves are 0.
		rec := hashTagged(&h, tagRecord, r.X, r.Y, r.T, r.Salt)
		leaves[j] = api.Select(r.Active, rec, 0)

		// Grid cell: drop the low CellShift bits (ToBinary also bounds X, Y < 2^CoordBits).
		xb := api.ToBinary(r.X, CoordBits)
		yb := api.ToBinary(r.Y, CoordBits)
		cx := api.FromBinary(xb[c.CellShift:]...)
		cy := api.FromBinary(yb[c.CellShift:]...)
		cell := api.Add(api.Mul(cx, cellMul), cy)

		// The cell is in the (hidden) area, enforced for active slots only.
		areaRoot := computeRoot(api, &h, hashTagged(&h, tagCell, cell), r.AreaSiblings, r.AreaBits)
		api.AssertIsEqual(api.Mul(r.Active, api.Sub(areaRoot, c.AreaRoot)), 0)

		// WindowStart <= T <= WindowEnd, enforced for active slots (inactive use a dummy in range).
		tEff := api.Select(r.Active, r.T, c.WindowStart)
		rc.Check(api.Sub(tEff, c.WindowStart), TimeBits)
		rc.Check(api.Sub(c.WindowEnd, tEff), TimeBits)
	}
	api.AssertIsEqual(count, c.RecordCount)

	// Records root: the Capacity leaves form the leftmost subtree; the upper
	// siblings (supplied by the operator) lead to the checkpoint's root.
	layer := leaves
	for len(layer) > 1 {
		next := make([]frontend.Variable, len(layer)/2)
		for i := range next {
			next[i] = hashTagged(&h, tagNode, layer[2*i], layer[2*i+1])
		}
		layer = next
	}
	cur := layer[0]
	for _, sib := range c.UpperSiblings {
		cur = hashTagged(&h, tagNode, cur, sib) // chunk 0 is always the left child
	}
	api.AssertIsEqual(cur, c.RecordsRoot)

	// The area root opens the public commitment.
	api.AssertIsEqual(hashTagged(&h, tagArea, c.AreaRoot, c.AreaSalt), c.AreaCommitment)

	// Force the binding limbs into the constraint system. In Groth16 an
	// unconstrained public input gets a zero coefficient in the VK and would NOT
	// be bound to the proof. (The chain checks their values; this binds them.)
	_ = api.Mul(c.BindingHi, c.BindingHi)
	_ = api.Mul(c.BindingLo, c.BindingLo)

	return nil
}
