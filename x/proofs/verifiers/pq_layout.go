package verifiers

import (
	"fmt"

	"github.com/lucidpay/lucidchain/x/proofs/types"
)

// Public-value layout of the av-zone statement. This is the Go side's single
// source of truth; it mirrors av-zone/src/statement.rs (PV_* constants) and the
// "Public values" table in av-zone/README.md. Change all three together.
const (
	// PQNumPublicValues: 16 binding limbs + zone_id + t_start + t_end
	// + 2*8 polygon values + 8 trip digest elements.
	PQNumPublicValues = 43

	PQPVZoneID    = bindingLimbs             // 16
	PQPVTStart    = PQPVZoneID + 1           // 17
	PQPVTEnd      = PQPVTStart + 1           // 18
	PQPVPoly      = PQPVTEnd + 1             // 19..35, (x, y) per vertex slot
	PQPolySlots   = 8                        // unused trailing slots repeat the last vertex
	PQPVDigest    = PQPVPoly + 2*PQPolySlots // 35..43
	PQDigestElems = 8
)

// Compile-time check that the layout adds up to PQNumPublicValues exactly
// (each array length is negative, so a compile error, if the sum is off).
var (
	_ [PQNumPublicValues - (PQPVDigest + PQDigestElems)]struct{}
	_ [(PQPVDigest + PQDigestElems) - PQNumPublicValues]struct{}
)

// PQStatement is the Go mirror of av_zone::statement::Statement without the
// checkpoint binding (which PQPublicInputs derives from types.Binding).
type PQStatement struct {
	ZoneID  uint32
	TStart  uint32
	TEnd    uint32
	Polygon [PQPolySlots][2]uint32
	// TripDigest is the Poseidon2 digest of the hidden sample chain (see
	// av-zone/src/commit.rs): 8 canonical BabyBear elements.
	TripDigest [PQDigestElems]uint32
}

// Values returns the statement values in layout order: everything after the
// binding limbs (27 values).
func (s PQStatement) Values() []uint32 {
	out := make([]uint32, 0, PQNumPublicValues-bindingLimbs)
	out = append(out, s.ZoneID, s.TStart, s.TEnd)
	for _, p := range s.Polygon {
		out = append(out, p[0], p[1])
	}
	return append(out, s.TripDigest[:]...)
}

// PQStatementPublicInputs encodes the full canonical public inputs for a
// statement bound to a checkpoint.
func PQStatementPublicInputs(binding types.Binding, s PQStatement) ([]byte, error) {
	return PQPublicInputs(binding, s.Values())
}

// ParsePQStatement decodes the statement part of canonical public inputs, for
// example from a stored ProofRecord. It does not check the binding.
func ParsePQStatement(publicInputs []byte) (PQStatement, error) {
	vals, err := parsePQPublicInputs(publicInputs)
	if err != nil {
		return PQStatement{}, err
	}
	if len(vals) != PQNumPublicValues {
		return PQStatement{}, fmt.Errorf("av-zone statement has %d public values, got %d", PQNumPublicValues, len(vals))
	}
	var s PQStatement
	s.ZoneID, s.TStart, s.TEnd = vals[PQPVZoneID], vals[PQPVTStart], vals[PQPVTEnd]
	for i := range s.Polygon {
		s.Polygon[i] = [2]uint32{vals[PQPVPoly+2*i], vals[PQPVPoly+2*i+1]}
	}
	copy(s.TripDigest[:], vals[PQPVDigest:])
	return s, nil
}
