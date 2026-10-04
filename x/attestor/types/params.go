package types

import (
	"fmt"
	"strings"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// MaxPeriodSeconds caps every duration in the module (10 years). It keeps
// time arithmetic far from overflow.
const MaxPeriodSeconds uint64 = 10 * 365 * 24 * 60 * 60

// DefaultParams returns the default module params. The values are
// placeholders: set bonds, slash fractions and gas from your economics and benchmarks.
func DefaultParams() Params {
	return Params{
		MinAttestorBond:          math.NewInt(1_000_000),
		MinDisputeBond:           math.NewInt(100_000),
		DisputeResolverAuthority: "", // empty => module authority (x/gov)
		MaxClaimPayloadBytes:     4096,
		BondDenom:                sdk.DefaultBondDenom,
		ExitCooldownSeconds:      14 * 24 * 60 * 60,
		SlashFraction:            math.LegacyNewDecWithPrec(10, 2), // 0.10
		ChallengerRewardFraction: math.LegacyNewDecWithPrec(50, 2), // 0.50
		MaxSignerKeys:            7,
		AllowedPubkeyTypeUrls:    []string{"/cosmos.crypto.mldsa65.PubKey"},
		SignatureVerificationGas: 50_000,
		MaxUriBytes:              512,
	}
}

// Validate performs stateless validation of the params. The dispute resolver
// address is checked by the keeper, which owns the address codec.
func (p Params) Validate() error {
	if p.MinAttestorBond.IsNil() || !p.MinAttestorBond.IsPositive() {
		return fmt.Errorf("min_attestor_bond must be positive")
	}
	if p.MinDisputeBond.IsNil() || !p.MinDisputeBond.IsPositive() {
		return fmt.Errorf("min_dispute_bond must be positive")
	}
	if strings.TrimSpace(p.DisputeResolverAuthority) != p.DisputeResolverAuthority {
		return fmt.Errorf("dispute_resolver_authority must not have surrounding whitespace")
	}
	if p.MaxClaimPayloadBytes == 0 {
		return fmt.Errorf("max_claim_payload_bytes must be positive")
	}
	if err := sdk.ValidateDenom(p.BondDenom); err != nil {
		return fmt.Errorf("invalid bond_denom: %w", err)
	}
	if p.ExitCooldownSeconds == 0 || p.ExitCooldownSeconds > MaxPeriodSeconds {
		return fmt.Errorf("exit_cooldown_seconds must be in [1, %d]", MaxPeriodSeconds)
	}
	if err := validateFraction("slash_fraction", p.SlashFraction); err != nil {
		return err
	}
	if err := validateFraction("challenger_reward_fraction", p.ChallengerRewardFraction); err != nil {
		return err
	}
	if p.MaxSignerKeys == 0 {
		return fmt.Errorf("max_signer_keys must be positive")
	}
	if len(p.AllowedPubkeyTypeUrls) == 0 {
		return fmt.Errorf("allowed_pubkey_type_urls must not be empty")
	}
	seen := make(map[string]struct{}, len(p.AllowedPubkeyTypeUrls))
	for _, u := range p.AllowedPubkeyTypeUrls {
		if !strings.HasPrefix(u, "/") {
			return fmt.Errorf("allowed_pubkey_type_urls entry %q must start with '/'", u)
		}
		if _, dup := seen[u]; dup {
			return fmt.Errorf("duplicate allowed_pubkey_type_urls entry %q", u)
		}
		seen[u] = struct{}{}
	}
	if p.SignatureVerificationGas == 0 {
		return fmt.Errorf("signature_verification_gas must be positive")
	}
	if p.MaxUriBytes == 0 {
		return fmt.Errorf("max_uri_bytes must be positive")
	}
	return nil
}

func validateFraction(name string, d math.LegacyDec) error {
	if d.IsNil() || d.IsNegative() || d.GT(math.LegacyOneDec()) {
		return fmt.Errorf("%s must be between 0 and 1", name)
	}
	return nil
}
