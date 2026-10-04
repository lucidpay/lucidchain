package types

import (
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

const (
	// MaxStateRootBytes bounds Checkpoint.state_root. A Merkle root is normally
	// 32 bytes; the headroom allows other commitment schemes.
	MaxStateRootBytes = 64

	DefaultMaxRecordsPerCheckpoint        uint64 = 1_000_000
	DefaultMaxDataPointerBytes            uint32 = 256
	DefaultMaxSignaturesPerCheckpoint     uint32 = 7
	DefaultMaxSignatureBytesPerCheckpoint uint32 = 24_000 // 7 x 3309 B ML-DSA-65 signatures

	// DefaultSignatureVerificationGas is a placeholder. Benchmark ML-DSA-65
	// verification on your validator hardware and set this from the result.
	DefaultSignatureVerificationGas uint64 = 50_000
)

// NewParams creates a new Params instance.
func NewParams() Params {
	return Params{}
}

// DefaultParams returns the default module params. Fees default to zero.
func DefaultParams() Params {
	return Params{
		BaseFee:                        sdk.NewCoin(sdk.DefaultBondDenom, math.ZeroInt()),
		PerRecordFee:                   sdk.NewCoin(sdk.DefaultBondDenom, math.ZeroInt()),
		MaxRecordsPerCheckpoint:        DefaultMaxRecordsPerCheckpoint,
		MaxDataPointerBytes:            DefaultMaxDataPointerBytes,
		MaxSignaturesPerCheckpoint:     DefaultMaxSignaturesPerCheckpoint,
		MaxSignatureBytesPerCheckpoint: DefaultMaxSignatureBytesPerCheckpoint,
		SignatureVerificationGas:       DefaultSignatureVerificationGas,
	}
}

// Validate performs basic validation of the params.
func (p Params) Validate() error {
	if err := p.BaseFee.Validate(); err != nil {
		return fmt.Errorf("invalid base_fee: %w", err)
	}
	if err := p.PerRecordFee.Validate(); err != nil {
		return fmt.Errorf("invalid per_record_fee: %w", err)
	}
	if p.BaseFee.Denom != p.PerRecordFee.Denom {
		return fmt.Errorf("base_fee and per_record_fee must use the same denom (%s vs %s)",
			p.BaseFee.Denom, p.PerRecordFee.Denom)
	}
	if p.MaxRecordsPerCheckpoint == 0 {
		return fmt.Errorf("max_records_per_checkpoint must be positive")
	}
	if p.MaxDataPointerBytes == 0 {
		return fmt.Errorf("max_data_pointer_bytes must be positive")
	}
	if p.MaxSignaturesPerCheckpoint == 0 {
		return fmt.Errorf("max_signatures_per_checkpoint must be positive")
	}
	if p.MaxSignatureBytesPerCheckpoint == 0 {
		return fmt.Errorf("max_signature_bytes_per_checkpoint must be positive")
	}
	if p.SignatureVerificationGas == 0 {
		return fmt.Errorf("signature_verification_gas must be positive")
	}
	return nil
}
