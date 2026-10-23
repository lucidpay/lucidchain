package types

import (
	"fmt"
	"strings"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

const (
	// MaxSignerKeysCap bounds Params.MaxSignerKeys. With 1952-byte keys this
	// directly bounds per-sidechain state size.
	MaxSignerKeysCap uint32 = 64

	// MinExitCooldownSeconds must stay above your governance voting period plus
	// a safety margin, so a slash proposal can still land during the cooldown.
	// SET THIS FROM YOUR GOV PARAMS before launch.
	MinExitCooldownSeconds uint64 = 3 * 24 * 60 * 60

	// MaxPeriodSeconds caps every duration in the module (10 years). It keeps
	// time arithmetic far from overflow.
	MaxPeriodSeconds uint64 = 10 * 365 * 24 * 60 * 60

	// DefaultRotationSignatureGas mirrors the checkpoint module's per-signature
	// gas. Benchmark ML-DSA-65 verification on the target hardware before launch.
	DefaultRotationSignatureGas uint64 = 50_000
)

// DefaultParams returns the default module params. The bonds are placeholders:
// set them from your economics. Registration needs governance activation.
func DefaultParams() Params {
	return Params{
		MinBondByTier: []TierBond{
			{Tier: AssuranceTier_ASSURANCE_TIER_NOTARIZED, MinBond: sdk.NewInt64Coin(sdk.DefaultBondDenom, 1_000_000)},
			{Tier: AssuranceTier_ASSURANCE_TIER_ATTESTED, MinBond: sdk.NewInt64Coin(sdk.DefaultBondDenom, 5_000_000)},
			{Tier: AssuranceTier_ASSURANCE_TIER_PROVEN, MinBond: sdk.NewInt64Coin(sdk.DefaultBondDenom, 10_000_000)},
		},
		MissedCheckpointGraceSeconds: 60 * 60,
		ExitCooldownSeconds:          14 * 24 * 60 * 60,
		MaxSignerKeys:                7,
		AllowedPubkeyTypeUrls:        []string{"/cosmos.crypto.mldsa65.PubKey"},
		AutoActivate:                 false,
		RotationSignatureGas:         DefaultRotationSignatureGas,
	}
}

// MinBondFor returns the minimum bond configured for a tier.
func (p Params) MinBondFor(tier AssuranceTier) (sdk.Coin, bool) {
	for _, tb := range p.MinBondByTier {
		if tb.Tier == tier {
			return tb.MinBond, true
		}
	}
	return sdk.Coin{}, false
}

// ValidTier reports whether tier is a known, specified assurance tier.
func ValidTier(tier AssuranceTier) bool {
	if tier == AssuranceTier_ASSURANCE_TIER_UNSPECIFIED {
		return false
	}
	_, ok := AssuranceTier_name[int32(tier)]
	return ok
}

// Validate performs stateless validation of the params.
func (p Params) Validate() error {
	if len(p.MinBondByTier) == 0 {
		return fmt.Errorf("min_bond_by_tier must not be empty")
	}
	seen := make(map[AssuranceTier]struct{}, len(p.MinBondByTier))
	for _, tb := range p.MinBondByTier {
		if !ValidTier(tb.Tier) {
			return fmt.Errorf("min_bond_by_tier has invalid tier %s", tb.Tier)
		}
		if _, dup := seen[tb.Tier]; dup {
			return fmt.Errorf("min_bond_by_tier has duplicate tier %s", tb.Tier)
		}
		seen[tb.Tier] = struct{}{}
		if err := tb.MinBond.Validate(); err != nil {
			return fmt.Errorf("min_bond for %s: %w", tb.Tier, err)
		}
		if !tb.MinBond.IsPositive() {
			return fmt.Errorf("min_bond for %s must be positive", tb.Tier)
		}
	}

	// every tier present exactly once, one denom across all tiers
	for _, tier := range []AssuranceTier{
		AssuranceTier_ASSURANCE_TIER_NOTARIZED,
		AssuranceTier_ASSURANCE_TIER_ATTESTED,
		AssuranceTier_ASSURANCE_TIER_PROVEN,
	} {
		if _, ok := seen[tier]; !ok {
			return fmt.Errorf("min_bond_by_tier is missing tier %s", tier)
		}
	}
	denom := p.MinBondByTier[0].MinBond.Denom
	for _, tb := range p.MinBondByTier {
		if tb.MinBond.Denom != denom {
			return fmt.Errorf("all min bonds must use one denom, got %s and %s", denom, tb.MinBond.Denom)
		}
	}

	if p.MissedCheckpointGraceSeconds > MaxPeriodSeconds {
		return fmt.Errorf("missed_checkpoint_grace_seconds must be at most %d", MaxPeriodSeconds)
	}

	if p.ExitCooldownSeconds < MinExitCooldownSeconds || p.ExitCooldownSeconds > MaxPeriodSeconds {
		return fmt.Errorf("exit_cooldown_seconds must be in [%d, %d]", MinExitCooldownSeconds, MaxPeriodSeconds)
	}
	if p.MaxSignerKeys == 0 || p.MaxSignerKeys > MaxSignerKeysCap {
		return fmt.Errorf("max_signer_keys must be in [1, %d]", MaxSignerKeysCap)
	}

	if len(p.AllowedPubkeyTypeUrls) == 0 {
		return fmt.Errorf("allowed_pubkey_type_urls must not be empty")
	}
	urls := make(map[string]struct{}, len(p.AllowedPubkeyTypeUrls))
	for _, u := range p.AllowedPubkeyTypeUrls {
		if !strings.HasPrefix(u, "/") {
			return fmt.Errorf("allowed_pubkey_type_urls entry %q must start with '/'", u)
		}
		if _, dup := urls[u]; dup {
			return fmt.Errorf("duplicate allowed_pubkey_type_urls entry %q", u)
		}
		urls[u] = struct{}{}
	}
	if p.RotationSignatureGas == 0 {
		return fmt.Errorf("rotation_signature_gas must be positive")
	}
	return nil
}
