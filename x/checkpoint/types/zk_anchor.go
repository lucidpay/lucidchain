package types

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"math/big"

	errorsmod "cosmossdk.io/errors"
)

// MaxRecordsTreeDepth: set to your circuits' real maximum.
const MaxRecordsTreeDepth = 64

// Bn254Fr is the BN254 scalar field modulus. Read-only; x/proofs reuses it.
var Bn254Fr, _ = new(big.Int).SetString(
	"21888242871839275222246405745257275088548364400416034343698204186575808495617", 10)

// BabyBearModulus is 2^31 - 2^27 + 1, the field of the Poseidon2/BabyBear scheme.
const BabyBearModulus uint32 = 2013265921

// BabyBearRootLimbs decodes a Poseidon2/BabyBear records_root: 8 limbs, each a
// u32 little-endian canonical field element (32 bytes in total).
func BabyBearRootLimbs(root []byte) ([8]uint32, error) {
	var out [8]uint32
	if len(root) != 32 {
		return out, errorsmod.Wrapf(ErrInvalidCheckpoint, "records_root is %d bytes, want 32", len(root))
	}
	for i := range out {
		out[i] = binary.LittleEndian.Uint32(root[4*i:])
		if out[i] >= BabyBearModulus {
			return out, errorsmod.Wrapf(ErrInvalidCheckpoint, "records_root limb %d is not a canonical field element", i)
		}
	}
	return out, nil
}

// ValidateZkAnchor checks an optional anchor. nil is valid (no anchor).
// An empty non-nil anchor is rejected (scheme unspecified), so "no anchor"
// has exactly one encoding in the sign bytes.
func ValidateZkAnchor(a *ZkAnchor) error {
	if a == nil {
		return nil
	}
	// DeriveStateRoot encodes the scheme on one byte; widen the preimage before
	// adding a scheme id above 255.
	switch a.Scheme {
	case ZkHashScheme_ZK_HASH_SCHEME_MIMC_BN254:
		if len(a.RecordsRoot) != 32 {
			return errorsmod.Wrapf(ErrInvalidCheckpoint, "records_root is %d bytes, want 32", len(a.RecordsRoot))
		}
		if new(big.Int).SetBytes(a.RecordsRoot).Cmp(Bn254Fr) >= 0 {
			return errorsmod.Wrap(ErrInvalidCheckpoint, "records_root is not a canonical field element")
		}
	case ZkHashScheme_ZK_HASH_SCHEME_POSEIDON2_BABYBEAR:
		if _, err := BabyBearRootLimbs(a.RecordsRoot); err != nil {
			return err
		}
	default:
		return errorsmod.Wrap(ErrInvalidCheckpoint, "unsupported zk hash scheme")
	}
	if a.RecordsTreeDepth == 0 || a.RecordsTreeDepth > MaxRecordsTreeDepth {
		return errorsmod.Wrapf(ErrInvalidCheckpoint, "records_tree_depth %d out of range [1, %d]",
			a.RecordsTreeDepth, MaxRecordsTreeDepth)
	}
	return nil
}

// StateRootDomain is prepended to the canonical state-root preimage.
const StateRootDomain = "lucidchain/checkpoint/v1/state-root\x00"

// DeriveStateRoot returns the one state_root a checkpoint carrying zk anchor
// a and record_count n is allowed to have:
//
//	SHA-256( StateRootDomain || scheme (1 byte) || records_root (32 bytes)
//	         || records_tree_depth (1 byte) || record_count (8 bytes, big-endian) )
//
// Without this rule the Tier 1 state_root (SHA-256, operator-defined) and the
// Tier 3 records_root (MiMC) are two unrelated commitments under one
// signature set, so a proof verified against the anchor says nothing about
// the records a relying party trusts under the state root. Deriving the state
// root from the anchor makes one checkpoint commit to one record set. Off-chain
// tooling must reproduce this byte for byte; see tools/signcheckpoint
// -derive-state-root and tools/zkgeo/submit-checkpoint-from-batch.sh.
//
// Precondition: a passed ValidateZkAnchor (32-byte root, depth in [1, 64],
// known scheme). The 1-byte fields are only injective under that check;
// ValidateAnchorBinding enforces it, callers deriving directly must too.
func DeriveStateRoot(a *ZkAnchor, recordCount uint64) []byte {
	h := sha256.New()
	h.Write([]byte(StateRootDomain))
	h.Write([]byte{byte(a.Scheme)})
	h.Write(a.RecordsRoot)
	h.Write([]byte{byte(a.RecordsTreeDepth)})
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], recordCount)
	h.Write(n[:])
	return h.Sum(nil)
}

// AnchorFits reports whether recordCount records fit in a Merkle tree of the
// anchor's depth (2^depth leaves). Depth is validated to be in [1, 64].
func AnchorFits(a *ZkAnchor, recordCount uint64) bool {
	if a.RecordsTreeDepth >= 64 {
		return true
	}
	return recordCount <= uint64(1)<<a.RecordsTreeDepth
}

// ValidateAnchorBinding enforces, for a checkpoint that carries an anchor,
// that record_count fits the tree and that state_root is the derived root.
func ValidateAnchorBinding(a *ZkAnchor, stateRoot []byte, recordCount uint64) error {
	if a == nil {
		return nil
	}
	if err := ValidateZkAnchor(a); err != nil {
		return err
	}
	if !AnchorFits(a, recordCount) {
		return errorsmod.Wrapf(ErrInvalidCheckpoint,
			"record_count %d does not fit a records tree of depth %d", recordCount, a.RecordsTreeDepth)
	}
	if want := DeriveStateRoot(a, recordCount); !bytes.Equal(stateRoot, want) {
		return errorsmod.Wrapf(ErrInvalidCheckpoint,
			"state_root must be derived from zk_anchor and record_count (want %x)", want)
	}
	return nil
}
