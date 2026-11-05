package types

import (
	"fmt"

	"cosmossdk.io/math"
)

// MaxMissedPenaltyFraction is the largest share of the bond that one missed
// checkpoint may burn (25%). It is a guard rail against a fat-fingered
// governance proposal, not a recommended value.
func MaxMissedPenaltyFraction() math.LegacyDec {
	return math.LegacyNewDecWithPrec(25, 2)
}

// MissedCheckpointPolicyFor returns the policy configured for a tier. A tier
// with no entry has no policy: misses are recorded as strikes and nothing else
// happens, using Params.MissedCheckpointGraceSeconds as the grace period.
func (p Params) MissedCheckpointPolicyFor(tier AssuranceTier) (MissedCheckpointPolicy, bool) {
	for _, mp := range p.MissedCheckpointPolicyByTier {
		if mp.Tier == tier {
			return mp, true
		}
	}
	return MissedCheckpointPolicy{}, false
}

// EffectiveMissedPolicy returns the policy that applies to a sidechain: its
// governance-set override if there is one, else its tier's policy, else a
// no-action policy that only records strikes and uses the global grace period.
func (sc Sidechain) EffectiveMissedPolicy(p Params) MissedCheckpointPolicy {
	if sc.MissedPolicyOverride != nil {
		return *sc.MissedPolicyOverride
	}
	if mp, ok := p.MissedCheckpointPolicyFor(sc.Tier); ok {
		return mp
	}
	return MissedCheckpointPolicy{
		Action:          MissedCheckpointAction_MISSED_CHECKPOINT_ACTION_NONE,
		GraceSeconds:    p.MissedCheckpointGraceSeconds,
		PenaltyFraction: math.LegacyZeroDec(),
	}
}

// DefaultMissedCheckpointPolicies is an example of tier-wide policies and is
// NOT used by DefaultParams: by default no sidechain is penalized, and
// penalties are enabled per sidechain through MsgSetSidechainMissedPolicy.
// The values are placeholders: tune them from your economics. Tier 1 records strikes only; Tiers 2 and 3 burn a share of the
// bond per miss and suspend after repeated misses.
func DefaultMissedCheckpointPolicies() []MissedCheckpointPolicy {
	return []MissedCheckpointPolicy{
		{
			Tier:            AssuranceTier_ASSURANCE_TIER_NOTARIZED,
			Action:          MissedCheckpointAction_MISSED_CHECKPOINT_ACTION_NONE,
			GraceSeconds:    60 * 60,
			PenaltyFraction: math.LegacyZeroDec(),
		},
		{
			Tier:                   AssuranceTier_ASSURANCE_TIER_ATTESTED,
			Action:                 MissedCheckpointAction_MISSED_CHECKPOINT_ACTION_PENALIZE,
			GraceSeconds:           60 * 60,
			PenaltyFraction:        math.LegacyNewDecWithPrec(5, 3), // 0.5%
			SuspendAfterStrikes:    3,
			StrikeDecayCheckpoints: 10,
		},
		{
			Tier:                   AssuranceTier_ASSURANCE_TIER_PROVEN,
			Action:                 MissedCheckpointAction_MISSED_CHECKPOINT_ACTION_PENALIZE,
			GraceSeconds:           60 * 60,
			PenaltyFraction:        math.LegacyNewDecWithPrec(2, 2), // 2%
			SuspendAfterStrikes:    5,
			StrikeDecayCheckpoints: 10,
		},
	}
}

// validateMissedCheckpointPolicies is called from Params.Validate. An empty
// list is valid (no per-tier policies), so genesis files and governance
// proposals written before this feature keep validating.
func (p Params) validateMissedCheckpointPolicies() error {
	if p.MaxBlockGapSeconds > MaxPeriodSeconds {
		return fmt.Errorf("max_block_gap_seconds must be at most %d", MaxPeriodSeconds)
	}

	seen := make(map[AssuranceTier]struct{}, len(p.MissedCheckpointPolicyByTier))
	for _, mp := range p.MissedCheckpointPolicyByTier {
		if !ValidTier(mp.Tier) {
			return fmt.Errorf("missed_checkpoint_policy_by_tier has invalid tier %s", mp.Tier)
		}
		if _, dup := seen[mp.Tier]; dup {
			return fmt.Errorf("missed_checkpoint_policy_by_tier has duplicate tier %s", mp.Tier)
		}
		seen[mp.Tier] = struct{}{}

		if err := validateMissedPolicyBody(mp.Tier.String(), mp); err != nil {
			return err
		}
	}
	return nil
}

// validateMissedPolicyBody checks everything in a policy except its tier.
func validateMissedPolicyBody(label string, mp MissedCheckpointPolicy) error {
	if mp.GraceSeconds > MaxPeriodSeconds {
		return fmt.Errorf("grace_seconds for %s must be at most %d", label, MaxPeriodSeconds)
	}

	switch mp.Action {
	case MissedCheckpointAction_MISSED_CHECKPOINT_ACTION_NONE,
		MissedCheckpointAction_MISSED_CHECKPOINT_ACTION_SUSPEND:
		if !mp.PenaltyFraction.IsNil() && !mp.PenaltyFraction.IsZero() {
			return fmt.Errorf("penalty_fraction for %s must be zero unless the action is PENALIZE", label)
		}
	case MissedCheckpointAction_MISSED_CHECKPOINT_ACTION_PENALIZE:
		if mp.PenaltyFraction.IsNil() || !mp.PenaltyFraction.IsPositive() {
			return fmt.Errorf("penalty_fraction for %s must be positive", label)
		}
		if mp.PenaltyFraction.GT(MaxMissedPenaltyFraction()) {
			return fmt.Errorf("penalty_fraction for %s must be at most %s", label, MaxMissedPenaltyFraction())
		}
		// Without an escalation point, penalties would repeat forever.
		if mp.SuspendAfterStrikes == 0 {
			return fmt.Errorf("suspend_after_strikes for %s must be set when the action is PENALIZE", label)
		}
	default:
		return fmt.Errorf("invalid missed checkpoint action %s for %s", mp.Action, label)
	}
	return nil
}

// ValidateMissedPolicyOverride validates a per-sidechain override. nil is valid
// (no override). The tier field is meaningless here and must be unset. An
// override that exempts a sidechain is {action: NONE, suspend_after_strikes: 0}.
// Call it from the genesis validation of each sidechain as well.
func ValidateMissedPolicyOverride(mp *MissedCheckpointPolicy) error {
	if mp == nil {
		return nil
	}
	if mp.Tier != AssuranceTier_ASSURANCE_TIER_UNSPECIFIED {
		return fmt.Errorf("a sidechain policy override must not set a tier")
	}
	return validateMissedPolicyBody("the sidechain override", *mp)
}
