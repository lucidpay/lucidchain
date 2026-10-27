package verifiers

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lucidpay/lucidchain/x/proofs/types"
)

// verifier.go: results must not depend on process-local state. Warm instances
// are reused, so a verdict must be identical on a reused instance (in any order)
// and on a brand-new runtime.
func TestWasmReuseDoesNotAffectVerdicts(t *testing.T) {
	vk := fx(t, "vkid.bin")
	pub, proof := fx(t, "pub_256.bin"), fx(t, "proof_256.bin")
	var id [VKIDLen]byte
	copy(id[:], vk)

	reqs := make([]PQRequest, 0, 24)
	reqs = append(reqs, PQRequest{VKID: id, PublicInputs: pub, Proof: proof}) // one valid
	for k := 0; k < 23; k++ {
		m := bytes.Clone(proof)
		m[int((uint64(k)*2654435761)%uint64(len(m)))] ^= 1 << (k % 8)
		reqs = append(reqs, PQRequest{VKID: id, PublicInputs: pub, Proof: m})
	}

	fresh := make([]Status, len(reqs))
	for i, r := range reqs {
		tr, err := NewWasmTransport(context.Background(), 1, 60*time.Second, 256)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := tr.Verify(r)
		_ = tr.Close()
		if err != nil {
			t.Fatal(err)
		}
		fresh[i] = resp.Status
	}

	reused, _ := NewWasmTransport(context.Background(), 1, 60*time.Second, 256) // 1 instance, reused every call
	defer reused.Close()
	for _, order := range [][]int{seq(len(reqs), false), seq(len(reqs), true)} {
		for _, i := range order {
			resp, err := reused.Verify(reqs[i])
			if err != nil {
				t.Fatal(err)
			}
			if resp.Status != fresh[i] {
				t.Fatalf("request %d: reused instance says %d, fresh says %d", i, resp.Status, fresh[i])
			}
		}
	}
}

func seq(n int, rev bool) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
		if rev {
			out[i] = n - 1 - i
		}
	}
	return out
}

// verifier_fault.go: infrastructure failures must satisfy errors.Is(ErrVerifierFault)
// when seen through the ProofVerifier, and verdicts must not.
func TestWasmFailuresAreVerifierFaults(t *testing.T) {
	pub, proof := fx(t, "pub_16384.bin"), fx(t, "proof_16384.bin")

	_, slow, vk := newWasm(t, 1, 5*time.Millisecond, 256) // timeout
	if err := slow.Verify(vk, pub, proof); !errors.Is(err, types.ErrVerifierFault) {
		t.Fatalf("timeout is not a fault: %v", err)
	}
	_, starved, vk := newWasm(t, 1, 60*time.Second, 17) // memory trap
	if err := starved.Verify(vk, fx(t, "pub_256.bin"), fx(t, "proof_256.bin")); !errors.Is(err, types.ErrVerifierFault) {
		t.Fatalf("memory trap is not a fault: %v", err)
	}
	_, ok, vk := newWasm(t, 1, 60*time.Second, 256) // a plain rejection is NOT a fault
	m := bytes.Clone(proof)
	m[100] ^= 1
	if err := ok.Verify(vk, pub, m); err == nil || errors.Is(err, types.ErrVerifierFault) {
		t.Fatalf("rejection misclassified: %v", err)
	}
}
