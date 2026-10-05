package types

import (
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// MaxDescriptionBytes bounds VerifierRegistration.description.
const MaxDescriptionBytes = 1024

// NewParams creates a new Params instance.
func NewParams() Params {
	return Params{}
}

// DefaultParams returns the default module params. The fee defaults to zero
// and the limits are placeholders: size them from your circuits.
func DefaultParams() Params {
	return Params{
		BaseVerificationFee:     math.ZeroInt(),
		MaxProofBytes:           8192,
		MaxPublicInputBytes:     4096,
		FeeDenom:                sdk.DefaultBondDenom,
		MaxVerificationKeyBytes: 65536,
	}
}

// Validate performs stateless validation of the params.
func (p Params) Validate() error {
	if p.BaseVerificationFee.IsNil() || p.BaseVerificationFee.IsNegative() {
		return fmt.Errorf("base_verification_fee must be non-negative")
	}
	if err := sdk.ValidateDenom(p.FeeDenom); err != nil {
		return fmt.Errorf("invalid fee_denom: %w", err)
	}
	if p.MaxProofBytes == 0 {
		return fmt.Errorf("max_proof_bytes must be positive")
	}
	if p.MaxPublicInputBytes == 0 {
		return fmt.Errorf("max_public_input_bytes must be positive")
	}
	if p.MaxVerificationKeyBytes == 0 {
		return fmt.Errorf("max_verification_key_bytes must be positive")
	}
	return nil
}
