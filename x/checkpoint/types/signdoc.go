package types

import (
	"crypto/sha256"
	"fmt"
)

// SignDocDomain is prepended to the marshaled CheckpointSignDoc so signatures
// over checkpoints can never be valid in another context.
const SignDocDomain = "lucidchain/checkpoint/v1/signdoc\x00"

// CheckpointSignBytes returns the exact bytes every signer signs:
//
//	SignDocDomain || proto.Marshal(CheckpointSignDoc)
//
// Off-chain signers must produce byte-identical output (proto3 field order,
// default values omitted). Publish test vectors for your signer SDKs.
func CheckpointSignBytes(doc *CheckpointSignDoc) ([]byte, error) {
	bz, err := doc.Marshal()
	if err != nil {
		return nil, fmt.Errorf("marshal checkpoint sign doc: %w", err)
	}
	out := make([]byte, 0, len(SignDocDomain)+len(bz))
	out = append(out, SignDocDomain...)
	out = append(out, bz...)
	return out, nil
}

// CheckpointHash is the canonical checkpoint hash: SHA-256 over the sign bytes.
// It is stored as Checkpoint.checkpoint_hash and chained through
// Checkpoint.previous_checkpoint_hash.
func CheckpointHash(signBytes []byte) []byte {
	h := sha256.Sum256(signBytes)
	return h[:]
}
