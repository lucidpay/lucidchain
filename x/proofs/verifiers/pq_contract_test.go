package verifiers

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lucidpay/lucidchain/x/proofs/types"
)

// verifier.go: "the result (including which error class) must be a pure,
// deterministic function of the arguments ... no process-local cache state".
// Warm instance reuse must therefore never change an outcome.
func TestWasmReuseDoesNotChangeOutcomes(t *testing.T) {
	_, reused, vk := newWasm(t, 1, 60*time.Second, 256) // one instance, reused for every call
	pub, proof := fx(t, "pub_256.bin"), fx(t, "proof_256.bin")

	class := func(err error) string {
		switch {
		case err == nil:
			return "valid"
		case errors.Is(err, types.ErrVerifierFault):
			return "fault"
		default:
			return "reject:" + err.Error()
		}
	}
	// A mix of valid, flipped and wrong-input cases, in a fixed order.
	type tc struct{ pub, proof []byte }
	var cases []tc
	cases = append(cases, tc{pub, proof})
	for k := 0; k < 12; k++ {
		m := bytes.Clone(proof)
		m[int((uint64(k)*2654435761)%uint64(len(m)))] ^= 1 << (k % 8)
		cases = append(cases, tc{pub, m}, tc{pub, proof})
	}
	bad := bytes.Clone(pub)
	bad[4+4*AVZoneIdxZoneID]++
	cases = append(cases, tc{bad, proof}, tc{pub, proof[:len(proof)/2]}, tc{pub, proof})

	for i, c := range cases {
		gotReused := class(reused.Verify(vk, c.pub, c.proof))

		// reference: a brand-new runtime and instance for this single call
		tr, err := NewWasmTransport(context.Background(), 1, 60*time.Second, 256)
		if err != nil {
			t.Fatal(err)
		}
		var id [VKIDLen]byte
		copy(id[:], vk)
		fresh := class(NewPQ(tr, WasmImageHash(), [][VKIDLen]byte{id}, PQWireMaxProofBytes).Verify(vk, c.pub, c.proof))
		_ = tr.Close()

		if gotReused != fresh {
			t.Fatalf("case %d: reused instance says %q, fresh instance says %q", i, gotReused, fresh)
		}
	}
	t.Logf("%d cases: identical outcome and error class on reused vs fresh instances", len(cases))
}

// verifier.go: CheckInputs compares against "a big-endian unsigned integer (any
// length, leading zeros ignored, empty is zero)".
func TestCheckInputsSemanticsMatchInterfaceDoc(t *testing.T) {
	_, v, _ := newWasm(t, 1, 10*time.Second, 256)
	st := AVZoneStatement{ZoneID: 42, TStart: 0, TEnd: 100, Polygon: [][2]uint32{{1000, 1000}, {3000, 1000}, {3000, 3000}}}
	pub, err := AVZonePublicInputs(types.Binding{}, st) // binding limbs are all zero
	if err != nil {
		t.Fatal(err)
	}
	ok := func(name string, e types.ExpectedInput) {
		t.Helper()
		if err := v.CheckInputs(pub, []types.ExpectedInput{e}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	bad := func(name string, e types.ExpectedInput) {
		t.Helper()
		if err := v.CheckInputs(pub, []types.ExpectedInput{e}); err == nil {
			t.Fatalf("%s: should have been rejected", name)
		}
	}
	ok("empty value is zero (limb 0)", types.ExpectedInput{Index: 0, Value: nil})
	ok("explicit zero byte", types.ExpectedInput{Index: 0, Value: []byte{0}})
	ok("zone_id 42", types.ExpectedInput{Index: AVZoneIdxZoneID, Value: []byte{42}})
	ok("leading zeros ignored", types.ExpectedInput{Index: AVZoneIdxZoneID, Value: append(make([]byte, 40), 42)})
	ok("t_start is zero", types.ExpectedInput{Index: AVZoneIdxTStart, Value: []byte{}})
	bad("wrong value", types.ExpectedInput{Index: AVZoneIdxZoneID, Value: []byte{43}})
	bad("empty vs nonzero", types.ExpectedInput{Index: AVZoneIdxZoneID, Value: nil})
	bad("index out of range", types.ExpectedInput{Index: AVZoneNumPublic, Value: []byte{0}})
	bad("value wider than a field element", types.ExpectedInput{Index: AVZoneIdxZoneID, Value: []byte{1, 0, 0, 0, 0, 0}})
	if v.BindingWidth() != 16 {
		t.Fatalf("BindingWidth = %d", v.BindingWidth())
	}
	if err := v.CheckBinding(pub, types.Binding{}); err != nil {
		t.Fatalf("zero binding: %v", err)
	}
}

// verifier.go: "Verify ... must not rely on CheckBinding or CheckInputs having run".
func TestVerifyAloneRejectsWithoutPriorChecks(t *testing.T) {
	_, v, vk := newWasm(t, 1, 60*time.Second, 256)
	pub, proof := fx(t, "pub_256.bin"), fx(t, "proof_256.bin")
	forged := bytes.Clone(pub)
	forged[4] ^= 1 // flip a binding limb; no CheckBinding called
	if err := v.Verify(vk, forged, proof); err == nil || errors.Is(err, types.ErrVerifierFault) {
		t.Fatalf("Verify must reject on its own, deterministically; got %v", err)
	}
}
