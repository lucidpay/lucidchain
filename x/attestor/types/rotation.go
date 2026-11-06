package types

import (
	"fmt"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
)

// AttestorRotationDomain is prepended to the marshaled AttestorRotationSignDoc
// so an approval to rotate an attestor's signer set can never be valid as an
// attestation signature, a sidechain rotation approval or anything else.
const AttestorRotationDomain = "lucidchain/attestor/v1/rotation-signdoc\x00"

// AttestorRotationSignBytes returns the exact bytes every current signer signs
// to approve a rotation:
//
//	AttestorRotationDomain || proto.Marshal(AttestorRotationSignDoc)
//
// The doc binds the chain id, the attestor, the CURRENT signer_set_version (so
// one approval authorises exactly one rotation), the full new signer set with
// its threshold, and an expiry height. Off-chain signers must produce
// byte-identical output (proto3 field order, default values omitted).
func AttestorRotationSignBytes(doc *AttestorRotationSignDoc) ([]byte, error) {
	bz, err := doc.Marshal()
	if err != nil {
		return nil, fmt.Errorf("marshal attestor rotation sign doc: %w", err)
	}
	out := make([]byte, 0, len(AttestorRotationDomain)+len(bz))
	out = append(out, AttestorRotationDomain...)
	out = append(out, bz...)
	return out, nil
}

// AttestorRotationSignDocFor builds the sign doc a rotation of attestor a to
// the given keys/threshold must be approved under. notAfterHeight is the last
// block at which the approval may be used.
func AttestorRotationSignDocFor(chainID string, a Attestor, newKeys []*codectypes.Any, newThreshold uint32, notAfterHeight uint64) *AttestorRotationSignDoc {
	return &AttestorRotationSignDoc{
		ChainId:                 chainID,
		AttestorId:              a.Id,
		CurrentSignerSetVersion: a.SignerSetVersion,
		NewSignerKeys:           newKeys,
		NewSignatureThreshold:   newThreshold,
		NotAfterHeight:          notAfterHeight,
	}
}
