package types

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"regexp"

	errorsmod "cosmossdk.io/errors"
)

const (
	// MaxIDBytes bounds attestor, schema and domain identifiers.
	MaxIDBytes = 64

	attestationIDDomain = "lucidchain/attestor/v1/attestation-id\x00"
	disputeIDDomain     = "lucidchain/attestor/v1/dispute-id\x00"
)

// idPattern restricts user-chosen identifiers to a safe charset. In
// particular it excludes "\x00" and "/", which would be ambiguous inside
// collections pair keys and REST paths.
var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// ValidateID validates a user-chosen identifier (attestor id, schema id, domain).
func ValidateID(field, id string) error {
	switch {
	case id == "":
		return errorsmod.Wrapf(ErrInvalidRequest, "%s is required", field)
	case len(id) > MaxIDBytes:
		return errorsmod.Wrapf(ErrLimitExceeded, "%s is %d bytes, max %d", field, len(id), MaxIDBytes)
	case !idPattern.MatchString(id):
		return errorsmod.Wrapf(ErrInvalidRequest,
			"%s %q must match [a-z0-9][a-z0-9._-]*", field, id)
	}
	return nil
}

// AttestationID derives the deterministic attestation id:
//
//	hex(sha256(domain || lp(schema_id) || lp(sidechain_id) || be64(sequence) || lp(attestor_id)))
//
// where lp is a uvarint length prefix, so field boundaries are unambiguous.
func AttestationID(schemaID, sidechainID string, checkpointSequence uint64, attestorID string) string {
	h := sha256.New()
	h.Write([]byte(attestationIDDomain))
	writeLengthPrefixed(h, schemaID)
	writeLengthPrefixed(h, sidechainID)
	var seq [8]byte
	binary.BigEndian.PutUint64(seq[:], checkpointSequence)
	h.Write(seq[:])
	writeLengthPrefixed(h, attestorID)
	return hex.EncodeToString(h.Sum(nil))
}

// DisputeID derives the deterministic dispute id for an attestation. An
// attestation can be disputed at most once, so this mapping is one-to-one.
func DisputeID(attestationID string) string {
	h := sha256.New()
	h.Write([]byte(disputeIDDomain))
	h.Write([]byte(attestationID))
	return hex.EncodeToString(h.Sum(nil))
}

func writeLengthPrefixed(h hash.Hash, s string) {
	var l [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(l[:], uint64(len(s)))
	h.Write(l[:n])
	h.Write([]byte(s))
}
