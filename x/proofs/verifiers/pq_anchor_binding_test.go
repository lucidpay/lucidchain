package verifiers

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"testing"

	"github.com/lucidpay/lucidchain/x/proofs/types"
)

// abKATDigest is KAT16_SALTED from rust_reference/av-zone/src/commit.rs.
var abKATDigest = [PQDigestElems]uint32{246859429, 1627689361, 871247076, 533616870, 616768534, 197241660, 1573146428, 1651469256}

// Single-trip checkpoint: zk_anchor{scheme: POSEIDON2_BABYBEAR (2), records_root: LE(abKATDigest), depth: 0},
// record_count 1. Computed independently in Python; pin the same values in an x/checkpoint test.
const (
	abKATAnchorRootHex  = "a5c6b60e91910461e42cee33e658ce1f1624c3243cabc10b3c4fc45dc86b6f62"
	abKATStateRootHexS2 = "22ea5aca332548a68be8e898d2e7989662b5d92f1037e2bea86451d9e23a7857"
)

// abAnchorRoot is the zk_anchor.records_root encoding for ZK_HASH_SCHEME_POSEIDON2_BABYBEAR:
// 8 canonical BabyBear elements as u32 little-endian words.
func abAnchorRoot(d [PQDigestElems]uint32) []byte {
	out := make([]byte, 0, 4*PQDigestElems)
	for _, w := range d {
		out = binary.LittleEndian.AppendUint32(out, w)
	}
	return out
}

// abLimbInputs mirrors what the proofs keeper must produce for eight
// INPUT_SOURCE_CHECKPOINT_RECORDS_ROOT_BABYBEAR_LIMB entries starting at firstIndex:
// limb i is u32 LE word i of records_root, as a canonical big-endian value.
// If types.ExpectedInput.Index is not a uint32, adjust the cast.
func abLimbInputs(root []byte, firstIndex int) []types.ExpectedInput {
	out := make([]types.ExpectedInput, 0, 8)
	for i := 0; i < 8; i++ {
		w := binary.LittleEndian.Uint32(root[4*i:])
		out = append(out, types.ExpectedInput{
			Index: uint32(firstIndex + i),
			Value: binary.BigEndian.AppendUint32(nil, w),
		})
	}
	return out
}

func abStateRoot(scheme byte, root []byte, depth byte, count uint64) string {
	h := sha256.New()
	h.Write([]byte("lucidchain/checkpoint/v1/state-root\x00"))
	h.Write([]byte{scheme})
	h.Write(root)
	h.Write([]byte{depth})
	var c [8]byte
	binary.BigEndian.PutUint64(c[:], count)
	h.Write(c[:])
	return hex.EncodeToString(h.Sum(nil))
}

func abNewTestPQ() types.ProofVerifier { return NewPQ(nil, PQAllowedVKIDs, 1) }

func abKATPublicInputs(t *testing.T, digest [PQDigestElems]uint32) []byte {
	t.Helper()
	st := PQStatement{ZoneID: 42, TStart: 1_000_000, TEnd: 1_000_300, TripDigest: digest}
	pi, err := PQStatementPublicInputs(types.Binding{}, st)
	if err != nil {
		t.Fatal(err)
	}
	return pi
}

func TestAnchorBindingTripDigestLimbs(t *testing.T) {
	root := abAnchorRoot(abKATDigest)
	if hex.EncodeToString(root) != abKATAnchorRootHex {
		t.Fatalf("anchor root = %x, want %s", root, abKATAnchorRootHex)
	}
	v := abNewTestPQ()
	exp := abLimbInputs(root, PQPVDigest)

	if err := v.CheckInputs(abKATPublicInputs(t, abKATDigest), exp); err != nil {
		t.Fatalf("matching digest must pass: %v", err)
	}

	// any single wrong digest element must be rejected
	for i := 0; i < PQDigestElems; i++ {
		bad := abKATDigest
		bad[i]++
		if err := v.CheckInputs(abKATPublicInputs(t, bad), exp); err == nil {
			t.Fatalf("digest element %d changed but CheckInputs accepted it", i)
		}
	}
}

func TestAnchorBindingNonCanonicalLimb(t *testing.T) {
	root := abAnchorRoot(abKATDigest)
	binary.LittleEndian.PutUint32(root[0:], babyBearModulus) // not a field element
	if err := abNewTestPQ().CheckInputs(abKATPublicInputs(t, abKATDigest), abLimbInputs(root, PQPVDigest)); err == nil {
		t.Fatal("a root limb >= the modulus must never match")
	}
}

// Documents the scheme-2 derivation. The first case is the readme §3.3 vector, which guards the
// helper itself; the second is the single-trip checkpoint above (scheme 2, depth 0, count 1).
func TestAnchorBindingStateRootVectors(t *testing.T) {
	one := make([]byte, 32)
	one[31] = 1
	if got := abStateRoot(1, one, 5, 3); got != "62daea805d87aec991f46a0b887bdb9337bd53a92a565604831dda49cc3ec137" {
		t.Fatalf("readme vector: got %s", got)
	}
	if got := abStateRoot(2, abAnchorRoot(abKATDigest), 0, 1); got != abKATStateRootHexS2 {
		t.Fatalf("scheme-2 vector: got %s, want %s", got, abKATStateRootHexS2)
	}
}
