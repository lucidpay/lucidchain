package verifiers

import (
	"context"
	"os"
	"testing"
	"time"
)

func BenchmarkWasmVerify(b *testing.B) {
	for _, tag := range []string{"256", "256_rt", "1024"} {
		pub, _ := os.ReadFile("testdata/pub_" + tag + ".bin")
		proof, _ := os.ReadFile("testdata/proof_" + tag + ".bin")
		tr, err := NewWasmTransport(context.Background(), 1, 30*time.Second, 256)
		if err != nil {
			b.Fatal(err)
		}
		req := PQRequest{VKID: PQAllowedVKIDs[0], PublicInputs: pub, Proof: proof}
		b.Run(tag, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if r, err := tr.Verify(req); err != nil || r.Status != StatusValid {
					b.Fatal(err, r.Status)
				}
			}
		})
		tr.Close()
	}
}

func BenchmarkWasmOverhead(b *testing.B) {
	tr, err := NewWasmTransport(context.Background(), 1, 30*time.Second, 256)
	if err != nil {
		b.Fatal(err)
	}
	defer tr.Close()
	pub := make([]byte, 4+4*16)
	pub[0] = 16
	req := PQRequest{VKID: PQAllowedVKIDs[0], PublicInputs: pub, Proof: []byte{0}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if r, err := tr.Verify(req); err != nil || r.Status != StatusMalformed {
			b.Fatal(err, r.Status)
		}
	}
}
