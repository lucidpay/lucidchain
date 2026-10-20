package keeper_test

// Place this in x/attestor/keeper/submit_attestation_tier_test.go. It reuses
// the fixtures in keeper_test.go (newActiveFixture, attestMsg, getAttestation,
// the mocks) and requireNoAttestation / putSidechain from
// msg_server_conflict_test.go.

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lucidpay/lucidchain/x/attestor/types"
	sidechaintypes "github.com/lucidpay/lucidchain/x/sidechain/types"
)

const (
	tierUnspecified = sidechaintypes.AssuranceTier_ASSURANCE_TIER_UNSPECIFIED
	tierNotarized   = sidechaintypes.AssuranceTier_ASSURANCE_TIER_NOTARIZED
	tierAttested    = sidechaintypes.AssuranceTier_ASSURANCE_TIER_ATTESTED
	tierProven      = sidechaintypes.AssuranceTier_ASSURANCE_TIER_PROVEN
)

// setSidechainTier changes only the tier of the default sidechain in the mock.
func (f *fixture) setSidechainTier(tier sidechaintypes.AssuranceTier) {
	sc := f.sidechains.m[testSidechain]
	sc.Tier = tier
	f.sidechains.m[testSidechain] = sc
}

func TestSubmitAttestation_RequiresAttestedTierOrHigher(t *testing.T) {
	cases := []struct {
		name string
		tier sidechaintypes.AssuranceTier
		want error // nil means accepted
	}{
		{"UNSPECIFIED is rejected", tierUnspecified, types.ErrSidechainTierTooLow},
		{"NOTARIZED is rejected", tierNotarized, types.ErrSidechainTierTooLow},
		{"ATTESTED is accepted", tierAttested, nil},
		{"PROVEN is accepted", tierProven, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newActiveFixture(t)
			f.setSidechainTier(tc.tier)

			resp, err := f.msgServer.SubmitAttestation(f.ctx, f.attestMsg(t, 1, 0, 1))
			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)
				f.requireNoAttestation(t)
				return
			}
			require.NoError(t, err)
			at := f.getAttestation(t, resp.AttestationId)
			require.Equal(t, testSidechain, at.SidechainId)
			require.Equal(t, types.AttestationStatus_ATTESTATION_STATUS_ACTIVE, at.Status)
			require.Equal(t, int64(1), f.getAttestor(t).ReputationScore)
		})
	}
}

// The tier rule runs before signature verification, so a rejected submission
// costs no verification gas.
func TestSubmitAttestation_TierRejectedBeforeSignatureVerification(t *testing.T) {
	f := newActiveFixture(t)
	f.setSidechainTier(tierNotarized)

	p, err := f.keeper.GetParams(f.ctx)
	require.NoError(t, err)
	before := f.ctx.GasMeter().GasConsumed()

	_, err = f.msgServer.SubmitAttestation(f.ctx, f.attestMsg(t, 1, 0, 1))
	require.ErrorIs(t, err, types.ErrSidechainTierTooLow)
	require.Less(t, f.ctx.GasMeter().GasConsumed()-before, p.SignatureVerificationGas)
}

// A rejected submission leaves the (schema, sidechain, lc_sequence, attestor)
// tuple unused. After the sidechain upgrades, the very same signed message is
// accepted: the signed document does not depend on the tier.
func TestSubmitAttestation_AcceptedAfterSidechainUpgrade(t *testing.T) {
	f := newActiveFixture(t)
	f.setSidechainTier(tierNotarized)

	msg := f.attestMsg(t, 1, 0, 1)
	_, err := f.msgServer.SubmitAttestation(f.ctx, msg)
	require.ErrorIs(t, err, types.ErrSidechainTierTooLow)
	f.requireNoAttestation(t)

	f.setSidechainTier(tierAttested)

	resp, err := f.msgServer.SubmitAttestation(f.ctx, msg)
	require.NoError(t, err)
	require.Equal(t, types.AttestationID(testSchema, testSidechain, 1, testAttestor), resp.AttestationId)
}

// An attestation made while the sidechain was ATTESTED is not affected by a
// later tier change to PROVEN: tiers only go up, so existing attestations stay
// valid and can still be revoked.
func TestSubmitAttestation_ExistingAttestationSurvivesUpgradeToProven(t *testing.T) {
	f := newActiveFixture(t)
	id := f.attest(t, 1)

	f.setSidechainTier(tierProven)

	_, err := f.msgServer.RevokeAttestation(f.ctx, &types.MsgRevokeAttestation{
		Operator:      f.operatorS,
		AttestationId: id,
	})
	require.NoError(t, err)
	require.Equal(t, types.AttestationStatus_ATTESTATION_STATUS_REVOKED, f.getAttestation(t, id).Status)
}
