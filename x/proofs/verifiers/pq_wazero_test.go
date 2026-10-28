package verifiers

import (
	"bytes"
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/lucidpay/lucidchain/x/proofs/types"
)

func fx(t testing.TB, name string) []byte {
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newWasm(t testing.TB, inst int, timeout time.Duration, pages uint32) (*WasmTransport, types.ProofVerifier, []byte) {
	tr, err := NewWasmTransport(context.Background(), inst, timeout, pages)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tr.Close() })
	vk := fx(t, "vkid.bin")
	var id [VKIDLen]byte
	copy(id[:], vk)
	return tr, NewPQ(tr, [][VKIDLen]byte{id}, PQWireMaxProofBytes), vk
}

func TestWasmRealProofs(t *testing.T) {
	_, v, vk := newWasm(t, 2, 60*time.Second, 1024)
	for _, n := range []string{"256", "16384"} {
		pub, proof := fx(t, "pub_"+n+".bin"), fx(t, "proof_"+n+".bin")
		start := time.Now()
		if err := v.Verify(vk, pub, proof); err != nil {
			t.Fatalf("n=%s: %v", n, err)
		}
		t.Logf("n=%s proof=%d bytes verify(first)=%v", n, len(proof), time.Since(start))
		start = time.Now()
		if err := v.Verify(vk, pub, proof); err != nil {
			t.Fatal(err)
		}
		// Every call now uses a fresh instance, so "second" is not "warm" any more.
		t.Logf("n=%s verify(second, fresh instance)=%v", n, time.Since(start))
	}
}

func TestWasmRejectionsAreNotFaults(t *testing.T) {
	_, v, vk := newWasm(t, 2, 60*time.Second, 1024)
	pub, proof := fx(t, "pub_256.bin"), fx(t, "proof_256.bin")

	// flipped bytes: error, but never a fault, and never nil
	for _, pos := range []int{0, 1, 77, len(proof) / 2, len(proof) - 1} {
		m := bytes.Clone(proof)
		m[pos] ^= 0x40
		err := v.Verify(vk, pub, m)
		if err == nil || errors.Is(err, types.ErrVerifierFault) {
			t.Fatalf("flip %d: err=%v", pos, err)
		}
	}
	// wrong zone_id (index 16 of the values; values start at byte 4)
	bad := bytes.Clone(pub)
	bad[4+4*16]++
	if err := v.Verify(vk, bad, proof); err == nil || errors.Is(err, types.ErrVerifierFault) {
		t.Fatalf("wrong public value: %v", err)
	}
	// truncated and trailing proof bytes
	for _, p := range [][]byte{proof[:len(proof)/2], append(bytes.Clone(proof), 0)} {
		if err := v.Verify(vk, pub, p); err == nil || errors.Is(err, types.ErrVerifierFault) {
			t.Fatalf("malformed: %v", err)
		}
	}
}

func TestWasmSelfCheck(t *testing.T) {
	tr, _, _ := newWasm(t, 1, 60*time.Second, 1024)
	if err := PQSelfCheck(tr, DefaultKnownAnswers()); err != nil {
		t.Fatal(err)
	}

	// a vk_id the guest does not know is a fault, so the self-check must fail
	unknown := DefaultKnownAnswers()
	unknown[0].VKID = [VKIDLen]byte{9}
	if err := PQSelfCheck(tr, unknown); err == nil {
		t.Fatal("unknown vk_id passed self-check")
	}

	// a known answer that is NOT a valid proof must fail the self-check
	corrupt := DefaultKnownAnswers()
	corrupt[0].Proof = bytes.Clone(corrupt[0].Proof)
	corrupt[0].Proof[len(corrupt[0].Proof)/2] ^= 0x01
	if err := PQSelfCheck(tr, corrupt); err == nil {
		t.Fatal("corrupted known answer passed self-check")
	}
}

func TestWasmConcurrent(t *testing.T) {
	_, v, vk := newWasm(t, 4, 120*time.Second, 1024)
	pub, proof := fx(t, "pub_256.bin"), fx(t, "proof_256.bin")
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	start := time.Now()
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				errs <- v.Verify(vk, pub, proof)
				return
			}
			m := bytes.Clone(proof)
			m[len(m)/3] ^= 1
			if err := v.Verify(vk, pub, m); err == nil {
				errs <- errors.New("tampered proof accepted")
				return
			}
			errs <- nil
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("16 concurrent verifies on 4 instances: %v", time.Since(start))
}

func TestWasmTimeoutIsAFaultAndRecovers(t *testing.T) {
	tr, _, vk := newWasm(t, 1, 5*time.Millisecond, 1024)
	req := PQRequest{PublicInputs: fx(t, "pub_16384.bin"), Proof: fx(t, "proof_16384.bin")}
	copy(req.VKID[:], vk)
	_, err := tr.Verify(req)
	if err == nil {
		t.Fatal("expected timeout")
	}
	t.Logf("timeout error: %v", err)
	// the transport must still work afterwards (every call uses a fresh instance)
	tr.timeout = 60 * time.Second
	resp, err := tr.Verify(req)
	if err != nil || resp.Status != StatusValid {
		t.Fatalf("after timeout: %v %v", resp, err)
	}
}

func TestWasmMemoryCapIsAFault(t *testing.T) {
	// 17 pages is the module's initial size: no room to verify anything real.
	tr, _, vk := newWasm(t, 1, 60*time.Second, 17)
	req := PQRequest{PublicInputs: fx(t, "pub_256.bin"), Proof: fx(t, "proof_256.bin")}
	copy(req.VKID[:], vk)
	if _, err := tr.Verify(req); err == nil {
		t.Fatal("expected failure with starved memory")
	} else {
		t.Logf("starved-memory error: %v", err)
	}
}

// Queueing must not count against the execution timeout: with one slot and a
// timeout comfortably above one verify, several callers must all succeed.
func TestWasmQueueingDoesNotTimeOut(t *testing.T) {
	_, v, vk := newWasm(t, 1, 5*time.Second, 1024)
	pub, proof := fx(t, "pub_256.bin"), fx(t, "proof_256.bin")
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- v.Verify(vk, pub, proof) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}
