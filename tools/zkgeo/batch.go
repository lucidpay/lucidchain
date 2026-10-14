package zkgeo

import (
	"errors"
	"fmt"
	"math/big"
	"math/bits"

	"github.com/consensys/gnark-crypto/ecc/bn254/fr"
	"github.com/consensys/gnark/frontend"
)

// Config holds compile-time parameters. Any change yields a different circuit
// and therefore a new verifying key (and, for Groth16, a new ceremony).
type Config struct {
	CellShift    int // low bits of X/Y dropped to form the grid cell
	Capacity     int // max records per proof; power of two
	RecordsDepth int // depth of the sidechain's records tree (>= log2(Capacity))
	AreaDepth    int // depth of the allowed-cell tree
}

// DefaultConfig is a starting point. Area membership dominates the cost
// (about Capacity * AreaDepth * 3 MiMC permutations): measure before raising it.
func DefaultConfig() Config {
	return Config{CellShift: 14, Capacity: 32, RecordsDepth: 20, AreaDepth: 16}
}

func (c Config) log2Capacity() int { return bits.Len(uint(c.Capacity)) - 1 }

func (c Config) Validate() error {
	switch {
	case c.CellShift < 0 || c.CellShift >= CoordBits:
		return errors.New("zkgeo: CellShift out of range")
	case c.Capacity < 1 || c.Capacity&(c.Capacity-1) != 0:
		return errors.New("zkgeo: Capacity must be a power of two")
	case c.RecordsDepth < c.log2Capacity() || c.RecordsDepth > 32:
		return errors.New("zkgeo: RecordsDepth must be in [log2(Capacity), 32]")
	case c.AreaDepth < 1 || c.AreaDepth > 32:
		return errors.New("zkgeo: AreaDepth out of range")
	}
	return nil
}

// NewCircuit allocates a circuit (or an assignment skeleton) for cfg.
func NewCircuit(cfg Config) *BatchCircuit {
	c := &BatchCircuit{
		CellShift:     cfg.CellShift,
		Records:       make([]RecordWitness, cfg.Capacity),
		UpperSiblings: make([]frontend.Variable, cfg.RecordsDepth-cfg.log2Capacity()),
	}
	for i := range c.Records {
		c.Records[i].AreaSiblings = make([]frontend.Variable, cfg.AreaDepth)
		c.Records[i].AreaBits = make([]frontend.Variable, cfg.AreaDepth)
	}
	return c
}

// Record is one sidechain record in the checkpoint's batch.
type Record struct {
	X, Y uint32
	T    uint64
	Salt fr.Element
}

// BatchInputs is everything the operator's prover needs.
type BatchInputs struct {
	Records                []Record // ALL records of the checkpoint, in sidechain order
	Area                   *Area
	WindowStart, WindowEnd uint64
	BindingHi, BindingLo   *big.Int // from `q proofs binding`

	// AllowOutOfArea substitutes a dummy path for cells outside the area instead
	// of failing. Tests only: it produces unsatisfiable witnesses.
	AllowOutOfArea bool
}

func bigOf(e fr.Element) *big.Int { return e.BigInt(new(big.Int)) }

// BuildAssignment builds the full witness. Public inputs are obtained with
// frontend.NewWitness(a, field, frontend.PublicOnly()).
func BuildAssignment(cfg Config, in BatchInputs) (*BatchCircuit, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if len(in.Records) > cfg.Capacity {
		return nil, fmt.Errorf("zkgeo: %d records exceed capacity %d", len(in.Records), cfg.Capacity)
	}
	if in.Area == nil || in.BindingHi == nil || in.BindingLo == nil {
		return nil, errors.New("zkgeo: Area and binding limbs are required")
	}

	leaves := make([]fr.Element, len(in.Records))
	for i, r := range in.Records {
		leaves[i] = RecordLeaf(r.X, r.Y, r.T, r.Salt)
	}
	tree, err := BuildTree(leaves, cfg.RecordsDepth)
	if err != nil {
		return nil, err
	}
	sibs, _ := tree.Prove(0) // leftmost path; the first log2(Capacity) levels are recomputed in-circuit
	upper := sibs[cfg.log2Capacity():]

	a := NewCircuit(cfg)
	for i := range upper {
		a.UpperSiblings[i] = bigOf(upper[i])
	}
	a.BindingHi, a.BindingLo = in.BindingHi, in.BindingLo
	a.RecordsRoot = bigOf(tree.Root())
	a.AreaCommitment = bigOf(in.Area.Commitment())
	a.WindowStart, a.WindowEnd = in.WindowStart, in.WindowEnd
	a.RecordCount = uint64(len(in.Records))
	a.AreaRoot, a.AreaSalt = bigOf(in.Area.Root()), bigOf(in.Area.Salt)

	for j := range a.Records {
		w := &a.Records[j]
		if j >= len(in.Records) { // padding slot: all zero
			w.Active, w.X, w.Y, w.T, w.Salt = 0, 0, 0, 0, 0
			for d := range w.AreaSiblings {
				w.AreaSiblings[d], w.AreaBits[d] = 0, 0
			}
			continue
		}
		r := in.Records[j]
		w.Active, w.X, w.Y, w.T, w.Salt = 1, r.X, r.Y, r.T, bigOf(r.Salt)

		asib, adirs, ok := in.Area.Prove(CellID(r.X, r.Y, cfg.CellShift))
		if !ok {
			if !in.AllowOutOfArea {
				return nil, fmt.Errorf("zkgeo: record %d is outside the area", j)
			}
			asib, adirs = in.Area.tree.Prove(0)
		}
		for d := range asib {
			w.AreaSiblings[d], w.AreaBits[d] = bigOf(asib[d]), adirs[d]
		}
	}
	return a, nil
}
