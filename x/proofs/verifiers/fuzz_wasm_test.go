package verifiers

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/lucidpay/lucidchain/x/proofs/types"
)

// A mutated proof must never verify, and no input may cause a verifier fault
// (a fault halts the node, so a proof that traps the guest is a chain-halt bug).
// Each iteration costs a full verification (tens of ms), so expect hundreds of
// executions per second at best; seed with structured mutations.
func FuzzWasmVerifyProof(f *testing.F) {
	_, v, vk := newWasm(f, 1, 30*time.Second, 256)
	pub, proof := fx(f, "pub_256.bin"), fx(f, "proof_256.bin")

	f.Add(proof)
	f.Add(proof[:len(proof)/2])
	f.Add(append(bytes.Clone(proof), 0))
	f.Add([]byte{0})
	for _, pos := range []int{0, 1, 8, len(proof) / 2, len(proof) - 1} {
		m := bytes.Clone(proof)
		m[pos] ^= 0x01
		f.Add(m)
	}

	f.Fuzz(func(t *testing.T, p []byte) {
		err := v.Verify(vk, pub, p)
		if errors.Is(err, types.ErrVerifierFault) {
			t.Fatalf("input caused a verifier fault (len %d): %v", len(p), err)
		}
		if err == nil && !bytes.Equal(p, proof) {
			t.Fatalf("a different proof was accepted (len %d)", len(p))
		}
	})
}

// Changing any canonical public value must make the genuine proof fail.
// Values are mutated by whole elements so they stay canonical and reach the
// verifier instead of being rejected by the host-side parser.
func FuzzWasmVerifyPublicValue(f *testing.F) {
	_, v, vk := newWasm(f, 1, 30*time.Second, 256)
	pub, proof := fx(f, "pub_256.bin"), fx(f, "proof_256.bin")

	f.Add(uint8(0), uint32(8))   // a binding limb
	f.Add(uint8(16), uint32(43)) // zone_id
	f.Add(uint8(17), uint32(1)) // t_start
	f.Add(uint8(34), uint32(0)) // last polygon slot

	f.Fuzz(func(t *testing.T, idx uint8, val uint32) {
		i := int(idx) % 35
		val %= babyBearModulus
		bad := bytes.Clone(pub)
		off := 4 + 4*i
		old := uint32(bad[off]) | uint32(bad[off+1])<<8 | uint32(bad[off+2])<<16 | uint32(bad[off+3])<<24
		if val == old {
			return
		}
		bad[off], bad[off+1], bad[off+2], bad[off+3] = byte(val), byte(val>>8), byte(val>>16), byte(val>>24)

		err := v.Verify(vk, bad, proof)
		if errors.Is(err, types.ErrVerifierFault) {
			t.Fatalf("public value %d=%d caused a verifier fault: %v", i, val, err)
		}
		if err == nil {
			t.Fatalf("proof verified with public value %d changed to %d", i, val)
		}
	})
}
