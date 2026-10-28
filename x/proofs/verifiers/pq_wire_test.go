package verifiers

import (
	"bytes"
	"context"
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
	if !bytes.Equal(selfCheckProof, fx(t, "proof_256.bin")) ||
		!bytes.Equal(selfCheckPub, fx(t, "pub_256.bin")) {
		t.Fatal("wasm/embed/selfcheck_* differ from testdata/*_256.bin; recopy them")
	}
}
