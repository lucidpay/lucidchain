package types

import (
	"math/big"

	errorsmod "cosmossdk.io/errors"
)

// MaxRecordsTreeDepth: set to your circuits' real maximum.
const MaxRecordsTreeDepth = 64

// Bn254Fr is the BN254 scalar field modulus. Read-only; x/proofs reuses it.
var Bn254Fr, _ = new(big.Int).SetString(
	"21888242871839275222246405745257275088548364400416034343698204186575808495617", 10)

// ValidateZkAnchor checks an optional anchor. nil is valid (no anchor).
// An empty non-nil anchor is rejected (scheme unspecified), so "no anchor"
// has exactly one encoding in the sign bytes.
func ValidateZkAnchor(a *ZkAnchor) error {
	if a == nil {
		return nil
	}
	if a.Scheme != ZkHashScheme_ZK_HASH_SCHEME_MIMC_BN254 {
		return errorsmod.Wrap(ErrInvalidCheckpoint, "unsupported zk hash scheme")
	}
	if len(a.RecordsRoot) != 32 {
		return errorsmod.Wrapf(ErrInvalidCheckpoint, "records_root is %d bytes, want 32", len(a.RecordsRoot))
	}
	if new(big.Int).SetBytes(a.RecordsRoot).Cmp(Bn254Fr) >= 0 {
		return errorsmod.Wrap(ErrInvalidCheckpoint, "records_root is not a canonical field element")
	}
	if a.RecordsTreeDepth == 0 || a.RecordsTreeDepth > MaxRecordsTreeDepth {
		return errorsmod.Wrapf(ErrInvalidCheckpoint, "records_tree_depth %d out of range [1, %d]",
			a.RecordsTreeDepth, MaxRecordsTreeDepth)
	}
	return nil
}
