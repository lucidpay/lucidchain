package types

import (
	"fmt"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
)

// SignerRotationDomain is prepended to the marshaled SignerRotationSignDoc so
// an approval to rotate a signer set can never be valid as a checkpoint
// signature or in any other context.
const SignerRotationDomain = "lucidchain/sidechain/v1/rotation-signdoc\x00"

// SignerRotationSignBytes returns the exact bytes every current signer signs
// to approve a rotation:
//
//	SignerRotationDomain || proto.Marshal(SignerRotationSignDoc)
//
// The doc binds the chain id, the sidechain, the CURRENT signer_set_version
// (so one approval authorises exactly one rotation), and the full new signer
// set with its threshold. Off-chain signers must produce byte-identical
// output (proto3 field order, default values omitted).
func SignerRotationSignBytes(doc *SignerRotationSignDoc) ([]byte, error) {
	bz, err := doc.Marshal()
	if err != nil {
		return nil, fmt.Errorf("marshal signer rotation sign doc: %w", err)
	}
	out := make([]byte, 0, len(SignerRotationDomain)+len(bz))
	out = append(out, SignerRotationDomain...)
	out = append(out, bz...)
	return out, nil
}

// RotationSignDocFor builds the sign doc a rotation of sidechain sc to the
// given keys/threshold must be approved under. notAfterHeight is the last
// block at which the approval may be used.
func RotationSignDocFor(chainID string, sc Sidechain, newKeys []*codectypes.Any, newThreshold uint32, notAfterHeight uint64) *SignerRotationSignDoc {
	return &SignerRotationSignDoc{
		ChainId:                 chainID,
		SidechainId:             sc.Id,
		CurrentSignerSetVersion: sc.SignerSetVersion,
		NewSignerKeys:           newKeys,
		NewSignatureThreshold:   newThreshold,
		NotAfterHeight:          notAfterHeight,
	}
}
