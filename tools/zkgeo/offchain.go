package zkgeo

import (
	"errors"
	"fmt"
	"math"

	"github.com/consensys/gnark-crypto/ecc/bn254/fr"
	"github.com/consensys/gnark-crypto/ecc/bn254/fr/mimc"
)

// Everything in this file runs OFF-chain (operator prover, sidechain, tests).
// It is BN254-only; the circuit itself is curve-agnostic. The sidechain MUST
// build its ZK-friendly records tree with exactly these functions (see
// INTEGRATION.md, "Sidechain contract").

func hashTaggedFr(tag uint64, in ...fr.Element) fr.Element {
	h := mimc.NewMiMC()
	var t fr.Element
	t.SetUint64(tag)
	b := t.Bytes()
	h.Write(b[:])
	for i := range in {
		b = in[i].Bytes()
		h.Write(b[:])
	}
	var out fr.Element
	out.SetBytes(h.Sum(nil))
	return out
}

// RecordLeaf is the sidechain records-tree leaf. Salt is a per-record random
// field element chosen by the sidechain; it keeps leaves from being guessable.
func RecordLeaf(x, y uint32, t uint64, salt fr.Element) fr.Element {
	var fx, fy, ft fr.Element
	fx.SetUint64(uint64(x))
	fy.SetUint64(uint64(y))
	ft.SetUint64(t)
	return hashTaggedFr(tagRecord, fx, fy, ft, salt)
}

// CellID mirrors the circuit's cell derivation.
func CellID(x, y uint32, shift int) uint64 {
	w := uint(CoordBits - shift)
	return uint64(x>>uint(shift))<<w | uint64(y>>uint(shift))
}

func CellLeaf(cell uint64) fr.Element {
	var c fr.Element
	c.SetUint64(cell)
	return hashTaggedFr(tagCell, c)
}

func AreaCommit(root, salt fr.Element) fr.Element { return hashTaggedFr(tagArea, root, salt) }

// EncodeLonLat maps degrees to 32-bit fixed point (floats: off-chain only).
// With CellShift=14 a cell is about 153 m (lon) x 76 m (lat) at the equator.
func EncodeLonLat(lon, lat float64) (x, y uint32) {
	enc := func(v, span float64) uint32 {
		f := v / span * 4294967296.0
		return uint32(math.Min(math.Max(f, 0), math.MaxUint32))
	}
	return enc(lon+180, 360), enc(lat+90, 180)
}

// Tree is a sparse binary Merkle tree: leaves fill positions 0..n-1 and every
// other leaf is 0. Only populated nodes are stored, so depth 20+ is cheap.
type Tree struct {
	depth  int
	layers [][]fr.Element
	zero   []fr.Element // zero[d] = root of an all-padding subtree of height d
}

func BuildTree(leaves []fr.Element, depth int) (*Tree, error) {
	if depth < 1 || depth > 32 {
		return nil, errors.New("zkgeo: unsupported tree depth")
	}
	if uint64(len(leaves)) > uint64(1)<<uint(depth) {
		return nil, fmt.Errorf("zkgeo: %d leaves exceed depth %d", len(leaves), depth)
	}
	t := &Tree{depth: depth, layers: make([][]fr.Element, depth+1), zero: make([]fr.Element, depth+1)}
	for d := 0; d < depth; d++ {
		t.zero[d+1] = hashTaggedFr(tagNode, t.zero[d], t.zero[d])
	}
	t.layers[0] = append([]fr.Element(nil), leaves...)
	for d := 0; d < depth; d++ {
		prev := t.layers[d]
		next := make([]fr.Element, (len(prev)+1)/2)
		for i := range next {
			l, r := prev[2*i], t.zero[d]
			if 2*i+1 < len(prev) {
				r = prev[2*i+1]
			}
			next[i] = hashTaggedFr(tagNode, l, r)
		}
		t.layers[d+1] = next
	}
	return t, nil
}

func (t *Tree) at(d, i int) fr.Element {
	if i < len(t.layers[d]) {
		return t.layers[d][i]
	}
	return t.zero[d]
}

func (t *Tree) Root() fr.Element { return t.at(t.depth, 0) }

// Prove returns siblings and direction bits (1 = current node is the right child).
func (t *Tree) Prove(idx int) (siblings []fr.Element, dirs []uint64) {
	for d := 0; d < t.depth; d++ {
		siblings = append(siblings, t.at(d, idx^1))
		dirs = append(dirs, uint64(idx&1))
		idx >>= 1
	}
	return
}

// Area is the hidden set of allowed cells with its salted commitment.
type Area struct {
	Cells []uint64
	Salt  fr.Element
	tree  *Tree
	idx   map[uint64]int
}

func NewArea(cells []uint64, depth int, salt fr.Element) (*Area, error) {
	leaves := make([]fr.Element, len(cells))
	idx := make(map[uint64]int, len(cells))
	for i, c := range cells {
		leaves[i] = CellLeaf(c)
		if _, dup := idx[c]; !dup {
			idx[c] = i
		}
	}
	tree, err := BuildTree(leaves, depth)
	if err != nil {
		return nil, err
	}
	return &Area{Cells: cells, Salt: salt, tree: tree, idx: idx}, nil
}

func (a *Area) Root() fr.Element       { return a.tree.Root() }
func (a *Area) Commitment() fr.Element { return AreaCommit(a.Root(), a.Salt) }

// Prove returns a membership path for cell, or ok=false if the cell is not in the area.
func (a *Area) Prove(cell uint64) (siblings []fr.Element, dirs []uint64, ok bool) {
	i, ok := a.idx[cell]
	if !ok {
		return nil, nil, false
	}
	siblings, dirs = a.tree.Prove(i)
	return siblings, dirs, true
}
