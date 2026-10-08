package types

import (
	"fmt"
	"strings"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// MaxPeriodSeconds caps every duration in the module (10 years). It keeps
// time arithmetic far from overflow.
const MaxPeriodSeconds uint64 = 10 * 365 * 24 * 60 * 60

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
	if p.MissedCheckpointGraceSeconds > MaxPeriodSeconds {
		return fmt.Errorf("missed_checkpoint_grace_seconds must be at most %d", MaxPeriodSeconds)
	}
	if p.ExitCooldownSeconds == 0 || p.ExitCooldownSeconds > MaxPeriodSeconds {
		return fmt.Errorf("exit_cooldown_seconds must be in [1, %d]", MaxPeriodSeconds)
	}
	if p.MaxSignerKeys == 0 {
		return fmt.Errorf("max_signer_keys must be positive")
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
	return nil
}
