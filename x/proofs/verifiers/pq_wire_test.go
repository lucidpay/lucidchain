package verifiers

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"
)

func wireCfg() PQWasmConfig {
	return PQWasmConfig{MaxInstances: 4, Timeout: 10 * time.Second, MemoryPages: 256, VKIDs: PQAllowedVKIDs}
}

func TestPinMatchesEmbeddedWasm(t *testing.T) {
	if err := CheckWasmPin(); err != nil {
		t.Fatal(err) // message prints the actual hash to paste into pqWasmPinnedImageHex
	}
}

func TestDefaultWithPQ_GnarkAndPQCoexist(t *testing.T) {
	m, closeFn, err := DefaultWithPQ(context.Background(), wireCfg())
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()
	if _, ok := m["groth16-bn254"]; !ok {
		t.Fatal("gnark entry lost")
	}
	v, ok := m[PQBabyBear]
	if !ok {
		t.Fatal("PQ verifier not registered")
	}
	if err := v.Verify(fx(t, "vkid.bin"), fx(t, "pub_256.bin"), fx(t, "proof_256.bin")); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultWithPQ_RefusesWrongPin(t *testing.T) {
	saved := PQWasmPinnedImage
	defer func() { PQWasmPinnedImage = saved }()
	PQWasmPinnedImage[0] ^= 1
	if _, _, err := DefaultWithPQ(context.Background(), wireCfg()); err == nil {
		t.Fatal("must refuse to start when the pin does not match")
	} else {
		t.Logf("refused: %v", err)
	}
}

func TestDefaultWithPQ_RefusesUnknownVKID(t *testing.T) {
	cfg := wireCfg()
	cfg.VKIDs = [][VKIDLen]byte{{9}}
	if _, _, err := DefaultWithPQ(context.Background(), cfg); err == nil {
		t.Fatal("must refuse when the allowlist has an id the guest does not know")
	}
}

func TestSelfCheckFilesMatchFixtures(t *testing.T) {
	pairs := []struct {
		name    string
		got     []byte
		fixture string
	}{
		{"selfcheck_proof", selfCheckProof, "proof_256_rt.bin"},
		{"selfcheck_pub", selfCheckPub, "pub_256_rt.bin"},
		{"selfcheck_cap_proof", selfCheckCapProof, "proof_256_cap.bin"},
		{"selfcheck_cap_pub", selfCheckCapPub, "pub_256_cap.bin"},
	}
	for _, p := range pairs {
		if !bytes.Equal(p.got, fx(t, p.fixture)) {
			t.Errorf("wasm/embed/%s differs from testdata/%s; recopy it", p.name, p.fixture)
		}
	}
}

func TestDebugMutationTable(t *testing.T) {
	tr, err := NewWasmTransport(context.Background(), 2, 5*time.Second, 256)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	ka := DefaultKnownAnswers()[0]
	row := ""
	for i := 0; i < 35; i++ {
		bad := bytes.Clone(ka.Pub)
		bad[4+4*i] ^= 1
		r, err := tr.Verify(PQRequest{VKID: ka.VKID, PublicInputs: bad, Proof: ka.Proof})
		if err != nil {
			row += "F"
			continue
		}
		row += fmt.Sprint(r.Status)
	}
	t.Logf("WASM   %s", row)
}
