package types

import (
	"encoding/binary"
	"encoding/hex"
	"testing"
)

// Trip digest from rust_reference/av-zone/src/commit.rs (KAT16_SALTED) as a POSEIDON2_BABYBEAR
// records_root (8 u32 little-endian words), and the state_root of a single-record checkpoint
// (scheme 2, depth 0, count 1), computed independently in Python.
const (
	d0RootHex      = "a5c6b60e91910461e42cee33e658ce1f1624c3243cabc10b3c4fc45dc86b6f62"
	d0StateRootHex = "22ea5aca332548a68be8e898d2e7989662b5d92f1037e2bea86451d9e23a7857"
)

func d0Root(t *testing.T) []byte {
	t.Helper()
	b, err := hex.DecodeString(d0RootHex)
	if err != nil || len(b) != 32 {
		t.Fatalf("bad test root: %v", err)
	}
	return b
}

func TestZkAnchorDepthZeroOnlyForBabyBear(t *testing.T) {
	bb := &ZkAnchor{Scheme: ZkHashScheme_ZK_HASH_SCHEME_POSEIDON2_BABYBEAR, RecordsRoot: d0Root(t), RecordsTreeDepth: 0}
	if err := ValidateZkAnchor(bb); err != nil {
		t.Fatalf("babybear depth 0 must be valid: %v", err)
	}

	// MiMC keeps depth >= 1
	mimcRoot := make([]byte, 32)
	mimcRoot[31] = 7 // canonical BN254 element
	mimc := &ZkAnchor{Scheme: ZkHashScheme_ZK_HASH_SCHEME_MIMC_BN254, RecordsRoot: mimcRoot, RecordsTreeDepth: 0}
	if ValidateZkAnchor(mimc) == nil {
		t.Fatal("mimc depth 0 must stay invalid")
	}
	mimc.RecordsTreeDepth = 1
	if err := ValidateZkAnchor(mimc); err != nil {
		t.Fatalf("mimc depth 1 must be valid: %v", err)
	}

	// an upper bound still applies to both schemes
	bb.RecordsTreeDepth = MaxRecordsTreeDepth + 1
	if ValidateZkAnchor(bb) == nil {
		t.Fatal("depth above the maximum must be invalid")
	}
}

func TestZkAnchorDepthZeroRejectsNonCanonicalLimb(t *testing.T) {
	bad := d0Root(t)
	binary.LittleEndian.PutUint32(bad[0:], BabyBearModulus) // not a field element
	a := &ZkAnchor{Scheme: ZkHashScheme_ZK_HASH_SCHEME_POSEIDON2_BABYBEAR, RecordsRoot: bad, RecordsTreeDepth: 0}
	if ValidateZkAnchor(a) == nil {
		t.Fatal("a limb >= the BabyBear modulus must be rejected at depth 0")
	}
}

// The single-record checkpoint the av-zone flow submits: state_root is derived from the anchor and
// passes the same binding rule the chain applies (ValidateAnchorBinding).
func TestZkAnchorDepthZeroStateRootAndBinding(t *testing.T) {
	a := &ZkAnchor{Scheme: ZkHashScheme_ZK_HASH_SCHEME_POSEIDON2_BABYBEAR, RecordsRoot: d0Root(t), RecordsTreeDepth: 0}
	sr := DeriveStateRoot(a, 1)
	if got := hex.EncodeToString(sr); got != d0StateRootHex {
		t.Fatalf("state_root = %s, want %s", got, d0StateRootHex)
	}
	if err := ValidateAnchorBinding(a, sr, 1); err != nil {
		t.Fatalf("single-record checkpoint must pass the anchor binding rule: %v", err)
	}
}
