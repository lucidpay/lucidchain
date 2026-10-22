package verifiers

import (
	"bytes"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestWasmFlipCorpus(t *testing.T) {
	tr, _, vk := newWasm(t, 4, 60*time.Second, 1024)
	pub, proof := fx(t, "pub_256.bin"), fx(t, "proof_256.bin")
	var out bytes.Buffer
	for k := 0; k < 400; k++ {
		pos := int((uint64(k) * 2654435761) % uint64(len(proof)))
		m := bytes.Clone(proof)
		m[pos] ^= 1 << (k % 8)
		req := PQRequest{PublicInputs: pub, Proof: m}
		copy(req.VKID[:], vk)
		resp, err := tr.Verify(req)
		if err != nil {
			t.Fatalf("flip %d: %v", k, err)
		}
		fmt.Fprintf(&out, "%d %d %d\n", k, pos, resp.Status)
	}
	_ = os.WriteFile("/tmp/wasm_flips.txt", out.Bytes(), 0o644)
}
