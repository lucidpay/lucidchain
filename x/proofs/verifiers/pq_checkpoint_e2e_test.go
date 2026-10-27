package verifiers

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lucidpay/lucidchain/x/proofs/types"
)

func TestPQNumPublicMatchesCircuit(t *testing.T) {
	_, v, vk := newWasm(t, 1, 10*time.Second, 256)
	n, err := v.PublicInputCount(vk)
	if err != nil || n != AVZoneNumPublic {
		t.Fatalf("PublicInputCount = %d, %v; the av-zone statement has %d public values (set pqNumPublic in qp.go)", n, err, AVZoneNumPublic)
	}
}

// Checkpoint fields -> binding -> Rust prover -> wasm verifier.
func TestCheckpointToProofEndToEnd(t *testing.T) {
	prove := os.Getenv("AV_ZONE_PROVE")
	if prove == "" {
		t.Skip("AV_ZONE_PROVE (path to the av-zone-prove binary) not set")
	}

	// What the chain holds after a checkpoint is finalized (checkpoint.proto).
	const chainID, sidechainID, claimID = "lucidchain-1", "sc-1", "geofence.av-zone.v0"
	const seq = uint64(12)
	stateRoot := sha256.Sum256([]byte("records-batch-12"))
	checkpointHash := sha256.Sum256([]byte("signdoc-of-checkpoint-12"))
	binding := types.ComputeBinding(chainID, sidechainID, seq, stateRoot[:], checkpointHash[:], claimID)

	st := AVZoneStatement{
		ZoneID: 42, TStart: 1_000_000,
		Polygon: [][2]uint32{{1000, 1000}, {3000, 1000}, {3000, 3000}, {1000, 3000}},
	}
	const n = 256
	var spec strings.Builder
	var lastT uint32
	for i := uint32(0); i < n; i++ {
		lastT = st.TStart + 2 + 3*i
		fmt.Fprintf(&spec, "sample %d %d %d\n", 1500+(i*7)%1000, 1500+(i*13)%1000, lastT)
	}
	st.TEnd = lastT + 5
	fmt.Fprintf(&spec, "binding_hex %s\nzone_id %d\nt_start %d\nt_end %d\n", hex.EncodeToString(binding[:]), st.ZoneID, st.TStart, st.TEnd)
	for _, p := range st.Polygon {
		fmt.Fprintf(&spec, "polygon %d %d\n", p[0], p[1])
	}
	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.txt")
	if err := os.WriteFile(specPath, []byte(spec.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(prove, specPath, dir).CombinedOutput()
	if err != nil {
		t.Fatalf("prover: %v\n%s", err, out)
	}
	t.Logf("prover: %s", strings.TrimSpace(string(out)))
	pub, _ := os.ReadFile(filepath.Join(dir, "pub.bin"))
	proof, _ := os.ReadFile(filepath.Join(dir, "proof.bin"))

	// 1. The bytes Rust produced equal the bytes Go builds from the same inputs.
	want, err := AVZonePublicInputs(binding, st)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pub, want) {
		t.Fatalf("public inputs differ between Rust prover and Go\nrust: %x\ngo:   %x", pub, want)
	}

	// 2. Chain-side checks and verification through wasm.
	_, v, vk := newWasm(t, 2, 60*time.Second, 256)
	if err := v.CheckBinding(pub, binding); err != nil {
		t.Fatalf("CheckBinding: %v", err)
	}
	zone := big.NewInt(42).Bytes()
	if err := v.CheckInputs(pub, []types.ExpectedInput{{Index: AVZoneIdxZoneID, Value: zone}}); err != nil {
		t.Fatalf("CheckInputs zone_id: %v", err)
	}
	if err := v.CheckInputs(pub, []types.ExpectedInput{{Index: AVZoneIdxZoneID, Value: big.NewInt(43).Bytes()}}); err == nil {
		t.Fatal("wrong zone_id must be rejected by CheckInputs")
	}
	if err := v.Verify(vk, pub, proof); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	// 3. The proof is useless for any other checkpoint, claim or chain.
	other := map[string]types.Binding{
		"different checkpoint_hash": types.ComputeBinding(chainID, sidechainID, seq, stateRoot[:], append([]byte{0xFF}, checkpointHash[1:]...), claimID),
		"different chain":           types.ComputeBinding("lucidchain-2", sidechainID, seq, stateRoot[:], checkpointHash[:], claimID),
		"different sequence":        types.ComputeBinding(chainID, sidechainID, seq+1, stateRoot[:], checkpointHash[:], claimID),
		"different claim":           types.ComputeBinding(chainID, sidechainID, seq, stateRoot[:], checkpointHash[:], "geofence.other.v0"),
	}
	for name, b := range other {
		if err := v.CheckBinding(pub, b); err == nil {
			t.Fatalf("%s: CheckBinding accepted a foreign binding", name)
		}
		// an attacker who rewrites the public inputs to claim that binding still fails the proof
		forged, _ := AVZonePublicInputs(b, st)
		if err := v.Verify(vk, forged, proof); err == nil {
			t.Fatalf("%s: proof verified under a forged binding", name)
		}
	}
}
