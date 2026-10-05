package types

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"math/big"
	"regexp"

	errorsmod "cosmossdk.io/errors"
)

const (
	// MaxIDBytes bounds proof system and claim identifiers.
	MaxIDBytes = 64

	proofIDDomain   = "lucidchain/proofs/v1/proof-id\x00"
	bindingDomain   = "lucidchain/proofs/v1/binding\x00"
	bindingHalfSize = 16 // bytes per public-input limb
)

// idPattern restricts user-chosen identifiers to a safe charset (no "\x00" or
// "/", which would be ambiguous in pair keys and REST paths).
var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// ValidateID validates a user-chosen identifier (proof system id, claim id).
func ValidateID(field, id string) error {
	switch {
	case id == "":
		return errorsmod.Wrapf(ErrInvalidRequest, "%s is required", field)
	case len(id) > MaxIDBytes:
		return errorsmod.Wrapf(ErrLimitExceeded, "%s is %d bytes, max %d", field, len(id), MaxIDBytes)
	case !idPattern.MatchString(id):
		return errorsmod.Wrapf(ErrInvalidRequest, "%s %q must match [a-z0-9][a-z0-9._-]*", field, id)
	}
	return nil
}

// ProofID derives the deterministic proof record id:
//
//	hex(sha256(domain || lp(sidechain_id) || be64(sequence) || lp(proof_system_id) || lp(claim_id)))
//
// where lp is a uvarint length prefix, so field boundaries are unambiguous.
func ProofID(sidechainID string, checkpointSequence uint64, proofSystemID, claimID string) string {
	h := sha256.New()
	h.Write([]byte(proofIDDomain))
	writeLengthPrefixed(h, []byte(sidechainID))
	writeUint64(h, checkpointSequence)
	writeLengthPrefixed(h, []byte(proofSystemID))
	writeLengthPrefixed(h, []byte(claimID))
	return hex.EncodeToString(h.Sum(nil))
}

// Binding is the 32-byte value that ties a proof to exactly one checkpoint and
// claim on one chain. Provers put its two 128-bit halves in the first two
// public inputs of their circuit.
type Binding [32]byte

// ComputeBinding derives the binding:
//
//	sha256(domain || lp(chain_id) || lp(sidechain_id) || be64(sequence)
//	       || lp(state_root) || lp(checkpoint_hash) || lp(claim_id))
//
// Including chain_id stops replay on another chain, and checkpoint_hash (not
// just state_root) binds the proof to the exact accepted checkpoint.
func ComputeBinding(chainID, sidechainID string, checkpointSequence uint64, stateRoot, checkpointHash []byte, claimID string) Binding {
	h := sha256.New()
	h.Write([]byte(bindingDomain))
	writeLengthPrefixed(h, []byte(chainID))
	writeLengthPrefixed(h, []byte(sidechainID))
	writeUint64(h, checkpointSequence)
	writeLengthPrefixed(h, stateRoot)
	writeLengthPrefixed(h, checkpointHash)
	writeLengthPrefixed(h, []byte(claimID))

	var b Binding
	copy(b[:], h.Sum(nil))
	return b
}

// Hi returns the first 16 bytes (public input 0, big-endian).
func (b Binding) Hi() []byte {
	out := make([]byte, bindingHalfSize)
	copy(out, b[:bindingHalfSize])
	return out
}

// Lo returns the last 16 bytes (public input 1, big-endian).
func (b Binding) Lo() []byte {
	out := make([]byte, bindingHalfSize)
	copy(out, b[bindingHalfSize:])
	return out
}

// Limbs returns Hi and Lo as integers. Both are below 2^128, so they fit
// every supported scalar field without reduction.
func (b Binding) Limbs() (hi, lo *big.Int) {
	return new(big.Int).SetBytes(b[:bindingHalfSize]), new(big.Int).SetBytes(b[bindingHalfSize:])
}

func writeLengthPrefixed(h hash.Hash, bz []byte) {
	var l [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(l[:], uint64(len(bz)))
	h.Write(l[:n])
	h.Write(bz)
}

func writeUint64(h hash.Hash, v uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	h.Write(b[:])
}
