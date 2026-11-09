package verifiers

import (
	"os"
	"testing"

	"github.com/lucidpay/lucidchain/x/proofs/types"
)

func sampleStatement() PQStatement {
	s := PQStatement{ZoneID: 42, TStart: 1_000_000, TEnd: 1_000_770}
	for i := range s.Polygon {
		s.Polygon[i] = [2]uint32{uint32(1000 + i), uint32(2000 + i)}
	}
	for i := range s.TripDigest {
		s.TripDigest[i] = uint32(900 + i)
	}
	return s
}

func TestStatementEncodingMatchesTheLayout(t *testing.T) {
	var b types.Binding
	for i := range b {
		b[i] = byte(i + 1)
	}
	s := sampleStatement()
	bz, err := PQStatementPublicInputs(b, s)
	if err != nil {
		t.Fatal(err)
	}
	vals, err := parsePQPublicInputs(bz)
	if err != nil {
		t.Fatal(err)
	}
	if len(vals) != PQNumPublicValues {
		t.Fatalf("got %d values, want %d", len(vals), PQNumPublicValues)
	}
	// The verifier must agree with the encoder about how many values a statement has.
	id := [VKIDLen]byte{1}
	n, err := NewPQ(nil, [][VKIDLen]byte{id}, 1).PublicInputCount(id[:])
	if err != nil || n != len(vals) {
		t.Fatalf("PublicInputCount = %d, %v; encoder produced %d values", n, err, len(vals))
	}
	limbs, _ := bindingToLimbs(b)
	for i, l := range limbs {
		if vals[i] != l {
			t.Fatalf("binding limb %d", i)
		}
	}
	if vals[PQPVZoneID] != 42 || vals[PQPVTStart] != 1_000_000 || vals[PQPVTEnd] != 1_000_770 {
		t.Fatal("zone or window at the wrong index")
	}
	for i := 0; i < PQPolySlots; i++ {
		if vals[PQPVPoly+2*i] != uint32(1000+i) || vals[PQPVPoly+2*i+1] != uint32(2000+i) {
			t.Fatalf("polygon slot %d at the wrong index", i)
		}
	}
	for i := 0; i < PQDigestElems; i++ {
		if vals[PQPVDigest+i] != uint32(900+i) {
			t.Fatalf("digest element %d at the wrong index", i)
		}
	}
	got, err := ParsePQStatement(bz)
	if err != nil || got != s {
		t.Fatalf("round trip: %v %+v", err, got)
	}
}

func TestStatementRejectsBadShapes(t *testing.T) {
	var b types.Binding
	if _, err := ParsePQStatement(mustEncode(t, b, nil)); err == nil {
		t.Fatal("a binding-only input must not parse as an av-zone statement")
	}
	s := sampleStatement()
	s.TripDigest[3] = babyBearModulus
	if _, err := PQStatementPublicInputs(b, s); err == nil {
		t.Fatal("non-canonical digest element accepted")
	}
}

func mustEncode(t *testing.T, b types.Binding, extra []uint32) []byte {
	t.Helper()
	bz, err := PQPublicInputs(b, extra)
	if err != nil {
		t.Fatal(err)
	}
	return bz
}

// Cross-language check: the Rust-generated self-check file must parse with this
// layout and satisfy the invariants the Rust Statement enforces.
func TestEmbeddedSelfcheckParsesWithThisLayout(t *testing.T) {
	bz, err := os.ReadFile("wasm/embed/selfcheck_pub.bin")
	if err != nil {
		t.Skip("no embedded self-check file here: ", err)
	}
	s, err := ParsePQStatement(bz)
	if err != nil {
		t.Fatal(err)
	}
	if s.TStart > s.TEnd {
		t.Fatal("t_start after t_end")
	}
	for i, p := range s.Polygon {
		if p[0] >= 1<<14 || p[1] >= 1<<14 {
			t.Fatalf("vertex %d outside 14 bits", i)
		}
	}
	if s.Polygon[PQPolySlots-1] != s.Polygon[PQPolySlots-2] {
		t.Fatal("expected the fixture's trailing polygon slots to repeat the last vertex")
	}
	allZero := true
	for _, d := range s.TripDigest {
		allZero = allZero && d == 0
	}
	if allZero {
		t.Fatal("trip digest is all zero")
	}
}
