package types

import "fmt"

// SignDocDomain is prepended to the marshaled AttestationSignDoc so these
// signatures can never be valid in another context (including x/checkpoint).
const SignDocDomain = "lucidchain/attestor/v1/signdoc\x00"

// AttestationSignBytes returns the exact bytes every attestor signer signs:
//
//	SignDocDomain || proto.Marshal(AttestationSignDoc)
//
// Off-chain signers must produce byte-identical output (proto3 field order,
// default values omitted). Publish test vectors for your signer SDKs.
func AttestationSignBytes(doc *AttestationSignDoc) ([]byte, error) {
	bz, err := doc.Marshal()
	if err != nil {
		return nil, fmt.Errorf("marshal attestation sign doc: %w", err)
	}
	out := make([]byte, 0, len(SignDocDomain)+len(bz))
	out = append(out, SignDocDomain...)
	out = append(out, bz...)
	return out, nil
}
