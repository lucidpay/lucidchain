package verifiers

import (
	"context"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/lucidpay/lucidchain/x/proofs/types"
)

// pqWasmPinnedImageHex is sha256(pq_verifier.wasm) of the canonical, reproducible
// build (see rust_reference/BUILD.md). Change it only when you deliberately ship
// a new guest, in the same commit that replaces pq_verifier.wasm.
const pqWasmPinnedImageHex = "e044af832818f80a1162bb842f3cbd59986aa36fdff802ad7a6c49dbcbb9bed0"

// PQWasmPinnedImage is the decoded pin. A var (not const) only so tests can
// simulate a mismatch; production code must never assign it.
var PQWasmPinnedImage = mustHex32(pqWasmPinnedImageHex)

// PQAllowedVKIDs is the vk_id allowlist for plonky3-babybear-v1: the golden
// vk_id of av-zone.core.v0 (av-zone/tests/av_zone.rs: GOLDEN_VK_ID).
var PQAllowedVKIDs = [][VKIDLen]byte{
	mustHex32("b6bf25e504350b1a471abf6939a5e89ba2cf531e46ed058c7e60e4a99386e8c6"),
}

func mustHex32(s string) [32]byte {
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 32 {
		panic("verifiers: bad 32-byte hex constant: " + s)
	}
	var out [32]byte
	copy(out[:], b)
	return out
}

// CheckWasmPin fails unless the embedded guest is exactly the pinned build.
func CheckWasmPin() error {
	if got := WasmImageHash(); got != PQWasmPinnedImage {
		return fmt.Errorf("embedded pq_verifier.wasm has sha256 %x, pinned %x: "+
			"the embedded file is not the build this binary was pinned to", got, PQWasmPinnedImage)
	}
	return nil
}

// PQWasmConfig configures the in-process PQ verifier.
type PQWasmConfig struct {
	MaxInstances int
	Timeout      time.Duration
	MemoryPages  uint32 // 64 KiB each; measured need is ~3.2 MiB, so 256 (16 MiB) is generous
	VKIDs        [][VKIDLen]byte
}

// DefaultWithPQ returns the gnark verifiers plus the WASM-backed PQ verifier and
// a close func to run at shutdown. It refuses to return anything if the embedded
// guest is not the pinned build or the self-check fails.
func DefaultWithPQ(ctx context.Context, cfg PQWasmConfig) (map[string]types.ProofVerifier, func() error, error) {
	if err := CheckWasmPin(); err != nil {
		return nil, nil, err
	}
	tr, err := NewWasmTransport(ctx, cfg.MaxInstances, cfg.Timeout, cfg.MemoryPages)
	if err != nil {
		return nil, nil, err
	}
	if err := PQSelfCheck(tr, PQWasmPinnedImage, cfg.VKIDs); err != nil {
		_ = tr.Close()
		return nil, nil, fmt.Errorf("pq self-check: %w", err)
	}
	m := Default() // existing gnark verifiers, untouched
	m[PQBabyBear] = NewPQ(tr, PQWasmPinnedImage, cfg.VKIDs, PQWireMaxProofBytes)
	return m, tr.Close, nil
}
