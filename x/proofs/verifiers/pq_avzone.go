package verifiers

import (
	"errors"
	"fmt"

	"github.com/lucidpay/lucidchain/x/proofs/types"
)

// Layout of the av-zone statement's public values (rust_reference av-zone README):
//
//	0..16   checkpoint binding limbs (from types.Binding)
//	16      zone_id
//	17, 18  t_start, t_end
//	19..35  8 polygon vertex slots (x, y), counter-clockwise, padded by repeating the last
const (
	AVZoneMaxVertices = 8
	AVZoneNumPublic   = bindingLimbs + 3 + 2*AVZoneMaxVertices // 35

	AVZoneIdxZoneID = bindingLimbs
	AVZoneIdxTStart = bindingLimbs + 1
	AVZoneIdxTEnd   = bindingLimbs + 2
	AVZoneIdxPoly   = bindingLimbs + 3

	avZoneCoordBits = 14
	avZoneTimeBits  = 30
)

// AVZoneStatement is the public, non-binding part of an av-zone proof.
type AVZoneStatement struct {
	ZoneID  uint32
	TStart  uint32
	TEnd    uint32
	Polygon [][2]uint32 // 3..8 vertices, counter-clockwise
}

// Extra returns the public values that follow the binding limbs. It does the
// range checks the Rust verifier does, but not the convexity check: the guest
// stays the authority on that.
func (s AVZoneStatement) Extra() ([]uint32, error) {
	if n := len(s.Polygon); n < 3 || n > AVZoneMaxVertices {
		return nil, fmt.Errorf("polygon needs 3..%d vertices, got %d", AVZoneMaxVertices, n)
	}
	if s.TStart >= 1<<avZoneTimeBits || s.TEnd >= 1<<avZoneTimeBits || s.TEnd < s.TStart {
		return nil, errors.New("window times must be < 2^30 and t_end >= t_start")
	}
	out := []uint32{s.ZoneID, s.TStart, s.TEnd}
	last := s.Polygon[len(s.Polygon)-1]
	for i := 0; i < AVZoneMaxVertices; i++ {
		v := last
		if i < len(s.Polygon) {
			v = s.Polygon[i]
		}
		if v[0] >= 1<<avZoneCoordBits || v[1] >= 1<<avZoneCoordBits {
			return nil, fmt.Errorf("vertex %d coordinate out of range (must be < 2^%d)", i, avZoneCoordBits)
		}
		out = append(out, v[0], v[1])
	}
	return out, nil
}

// AVZonePublicInputs builds the canonical public inputs a client submits with
// an av-zone proof for the given checkpoint binding.
func AVZonePublicInputs(b types.Binding, s AVZoneStatement) ([]byte, error) {
	extra, err := s.Extra()
	if err != nil {
		return nil, err
	}
	return PQPublicInputs(b, extra)
}
