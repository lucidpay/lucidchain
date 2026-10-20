package keeper_test

// Place this in x/sidechain/keeper/upgrade_sidechain_test.go. It reuses the
// fixtures in keeper_test.go (newFixture, registerMsg, setStatus, bank mock,
// requireEscrowInvariant, ...) and the constants tier1Bond, tier2Bond,
// tier3Bond and startFunds.

import (
	"strings"
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"

	"github.com/lucidpay/lucidchain/x/sidechain/types"
)

const (
	notarized = types.AssuranceTier_ASSURANCE_TIER_NOTARIZED
	attested  = types.AssuranceTier_ASSURANCE_TIER_ATTESTED
	proven    = types.AssuranceTier_ASSURANCE_TIER_PROVEN
)

func (f *fixture) upgradeMsg(tier types.AssuranceTier, extra int64) *types.MsgUpgradeSidechain {
	return &types.MsgUpgradeSidechain{
		Operator:       f.operatorS,
		Id:             testID,
		NewTier:        tier,
		AdditionalBond: sdk.NewInt64Coin(sdk.DefaultBondDenom, extra),
	}
}

// newActiveFixtureWith registers testID at the given tier and bond and
// activates it.
func newActiveFixtureWith(t *testing.T, tier types.AssuranceTier, bond int64) *fixture {
	t.Helper()
	f := newFixture(t)
	m := f.registerMsg(t)
	m.Tier = tier
	m.Bond = sdk.NewInt64Coin(sdk.DefaultBondDenom, bond)
	_, err := f.msgServer.RegisterSidechain(f.ctx, m)
	require.NoError(t, err)
	f.activate(t, testID)
	return f
}

type upgradeSnapshot struct {
	tier        types.AssuranceTier
	status      types.SidechainStatus
	bond        int64
	operatorBal int64
	moduleBal   int64
}

func (f *fixture) snapshot(t *testing.T) upgradeSnapshot {
	t.Helper()
	sc := f.get(t, testID)
	return upgradeSnapshot{
		tier:        sc.Tier,
		status:      sc.Status,
		bond:        sc.Bond.Amount.Int64(),
		operatorBal: f.bal(f.operator),
		moduleBal:   f.moduleBal(),
	}
}

func TestUpgradeSidechain_Success(t *testing.T) {
	f := newActiveFixture(t)
	before := f.get(t, testID)

	resp, err := f.msgServer.UpgradeSidechain(f.ctx, f.upgradeMsg(attested, tier2Bond-tier1Bond))
	require.NoError(t, err)
	require.Equal(t, notarized, resp.PreviousTier)
	require.Equal(t, attested, resp.NewTier)
	require.Equal(t, tier2Bond, resp.Bond.Amount.Int64())
	require.Equal(t, sdk.DefaultBondDenom, resp.Bond.Denom)

	sc := f.get(t, testID)
	require.Equal(t, attested, sc.Tier)
	require.Equal(t, tier2Bond, sc.Bond.Amount.Int64())
	// Nothing else about the sidechain changes.
	require.Equal(t, types.SidechainStatus_SIDECHAIN_STATUS_ACTIVE, sc.Status)
	require.Equal(t, before.Operator, sc.Operator)
	require.Equal(t, before.SignerSetVersion, sc.SignerSetVersion)
	require.Equal(t, before.SignatureThreshold, sc.SignatureThreshold)
	require.Len(t, sc.SignerKeys, 3)
	require.True(t, sc.RegisteredAt.Equal(before.RegisteredAt))
	require.NotNil(t, sc.ActivatedAt)

	require.Equal(t, startFunds-tier2Bond, f.bal(f.operator))
	require.Equal(t, tier2Bond, f.moduleBal())
	f.requireEscrowInvariant(t)

	// The upgraded sidechain keeps working.
	require.NoError(t, f.keeper.RecordCheckpoint(f.ctx, testID, 1, testHeight, []byte("hash-1")))
}

func TestUpgradeSidechain_EmitsEvent(t *testing.T) {
	f := newActiveFixture(t)
	_, err := f.msgServer.UpgradeSidechain(f.ctx, f.upgradeMsg(attested, tier2Bond-tier1Bond))
	require.NoError(t, err)

	found := false
	for _, ev := range f.ctx.EventManager().Events() {
		if strings.Contains(ev.Type, "sidechain_tier_upgraded") {
			found = true
		}
	}
	require.True(t, found, "sidechain_tier_upgraded event not emitted")
}

func TestUpgradeSidechain_BondMath(t *testing.T) {
	cases := []struct {
		name     string
		regTier  types.AssuranceTier
		regBond  int64
		newTier  types.AssuranceTier
		extra    int64
		wantErr  error // nil means success
		wantBond int64 // expected bond on success
	}{
		{"1 to 2, exact top-up", notarized, tier1Bond, attested, tier2Bond - tier1Bond, nil, tier2Bond},
		{"1 to 2, one short", notarized, tier1Bond, attested, tier2Bond - tier1Bond - 1, types.ErrInsufficientBond, 0},
		{"1 to 2, overpay is kept in full", notarized, tier1Bond, attested, tier2Bond - tier1Bond + 7, nil, tier2Bond + 7},
		{"1 to 3 skips a tier", notarized, tier1Bond, proven, tier3Bond - tier1Bond, nil, tier3Bond},
		{"1 to 3 with the tier 2 top-up", notarized, tier1Bond, proven, tier2Bond - tier1Bond, types.ErrInsufficientBond, 0},
		{"2 to 3", attested, tier2Bond, proven, tier3Bond - tier2Bond, nil, tier3Bond},
		{"zero top-up when the bond already covers the tier", notarized, tier2Bond, attested, 0, nil, tier2Bond},
		{"zero top-up when the bond does not cover the tier", notarized, tier1Bond, attested, 0, types.ErrInsufficientBond, 0},
		// The current bond counts, not the old minimum: 1,500,000 needs 3,500,000 more for tier 2.
		{"bond above the old minimum, exact", notarized, 1_500_000, attested, tier2Bond - 1_500_000, nil, tier2Bond},
		{"bond above the old minimum, one short", notarized, 1_500_000, attested, tier2Bond - 1_500_000 - 1, types.ErrInsufficientBond, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newActiveFixtureWith(t, tc.regTier, tc.regBond)
			before := f.snapshot(t)

			_, err := f.msgServer.UpgradeSidechain(f.ctx, f.upgradeMsg(tc.newTier, tc.extra))
			if tc.wantErr == nil {
				require.NoError(t, err)
				sc := f.get(t, testID)
				require.Equal(t, tc.newTier, sc.Tier)
				require.Equal(t, tc.wantBond, sc.Bond.Amount.Int64())
				require.Equal(t, tc.wantBond, f.moduleBal())
				require.Equal(t, before.operatorBal-tc.extra, f.bal(f.operator))
				f.requireEscrowInvariant(t)
				return
			}
			require.ErrorIs(t, err, tc.wantErr)
			require.Equal(t, before, f.snapshot(t), "a failed upgrade must change and escrow nothing")
		})
	}
}

func TestUpgradeSidechain_BondBelowOldMinimum(t *testing.T) {
	// A real Slash also sets the status to SLASHED, which is rejected (see
	// TestUpgradeSidechain_Statuses). To exercise "current bond, not the old
	// minimum" on an upgradable sidechain, put an ACTIVE sidechain at 900,000
	// directly, as if governance had raised the tier minimums.
	f := newActiveFixture(t)
	sc := f.get(t, testID)
	sc.Bond = sdk.NewInt64Coin(sdk.DefaultBondDenom, 900_000)
	require.NoError(t, f.keeper.SetSidechain(f.ctx, sc))
	f.bank.modules[types.ModuleName] = math.NewInt(900_000)

	// A 4,000,000 top-up is not enough: 900,000 + 4,000,000 = 4,900,000, which
	// is 100,000 short of the 5,000,000 minimum. The full top-up needed is
	// 4,100,000, not 4,000,000. The error reports the remaining shortfall.
	_, err := f.msgServer.UpgradeSidechain(f.ctx, f.upgradeMsg(attested, 4_000_000))
	require.ErrorIs(t, err, types.ErrInsufficientBond)
	require.ErrorContains(t, err, "100000 more")

	_, err = f.msgServer.UpgradeSidechain(f.ctx, f.upgradeMsg(attested, 4_100_000))
	require.NoError(t, err)
	require.Equal(t, tier2Bond, f.get(t, testID).Bond.Amount.Int64())
	f.requireEscrowInvariant(t)
}

func TestUpgradeSidechain_TwoStepsAccumulateBond(t *testing.T) {
	f := newActiveFixture(t)

	_, err := f.msgServer.UpgradeSidechain(f.ctx, f.upgradeMsg(attested, tier2Bond-tier1Bond))
	require.NoError(t, err)
	_, err = f.msgServer.UpgradeSidechain(f.ctx, f.upgradeMsg(proven, tier3Bond-tier2Bond))
	require.NoError(t, err)

	sc := f.get(t, testID)
	require.Equal(t, proven, sc.Tier)
	require.Equal(t, tier3Bond, sc.Bond.Amount.Int64())
	require.Equal(t, startFunds-tier3Bond, f.bal(f.operator))
	f.requireEscrowInvariant(t)
}

func TestUpgradeSidechain_TierMustRiseAndBeValid(t *testing.T) {
	f := newActiveFixtureWith(t, attested, tier2Bond)
	before := f.snapshot(t)

	for name, tier := range map[string]types.AssuranceTier{
		"same tier":   attested,
		"lower tier":  notarized,
		"unspecified": types.AssuranceTier_ASSURANCE_TIER_UNSPECIFIED,
		"unknown":     types.AssuranceTier(99),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := f.msgServer.UpgradeSidechain(f.ctx, f.upgradeMsg(tier, 1_000_000))
			require.ErrorIs(t, err, types.ErrInvalidRequest)
			require.Equal(t, before, f.snapshot(t))
		})
	}
}

func TestUpgradeSidechain_Statuses(t *testing.T) {
	t.Run("PENDING is allowed", func(t *testing.T) {
		f := newFixture(t)
		f.register(t) // stays PENDING
		_, err := f.msgServer.UpgradeSidechain(f.ctx, f.upgradeMsg(attested, tier2Bond-tier1Bond))
		require.NoError(t, err)
		sc := f.get(t, testID)
		require.Equal(t, attested, sc.Tier)
		require.Equal(t, types.SidechainStatus_SIDECHAIN_STATUS_PENDING, sc.Status, "upgrading must not activate")
		f.requireEscrowInvariant(t)
	})

	t.Run("SUSPENDED is allowed", func(t *testing.T) {
		f := newActiveFixture(t)
		f.setStatus(t, testID, types.SidechainStatus_SIDECHAIN_STATUS_SUSPENDED)
		_, err := f.msgServer.UpgradeSidechain(f.ctx, f.upgradeMsg(attested, tier2Bond-tier1Bond))
		require.NoError(t, err)
		sc := f.get(t, testID)
		require.Equal(t, attested, sc.Tier)
		require.Equal(t, types.SidechainStatus_SIDECHAIN_STATUS_SUSPENDED, sc.Status, "upgrading must not resume")
		f.requireEscrowInvariant(t)
	})

	for _, st := range []types.SidechainStatus{
		types.SidechainStatus_SIDECHAIN_STATUS_SLASHED,
		types.SidechainStatus_SIDECHAIN_STATUS_EXITED,
	} {
		t.Run(st.String()+" is rejected", func(t *testing.T) {
			f := newActiveFixture(t)
			f.setStatus(t, testID, st)
			before := f.snapshot(t)

			_, err := f.msgServer.UpgradeSidechain(f.ctx, f.upgradeMsg(attested, tier2Bond-tier1Bond))
			require.ErrorIs(t, err, types.ErrInvalidSidechainState)
			require.Equal(t, before, f.snapshot(t))
		})
	}
}

func TestUpgradeSidechain_Authorization(t *testing.T) {
	f := newActiveFixture(t)
	before := f.snapshot(t)

	for name, operator := range map[string]string{
		"stranger":   f.strangerS,
		"governance": f.govS,
	} {
		t.Run(name, func(t *testing.T) {
			m := f.upgradeMsg(attested, tier2Bond-tier1Bond)
			m.Operator = operator
			_, err := f.msgServer.UpgradeSidechain(f.ctx, m)
			require.ErrorIs(t, err, types.ErrUnauthorized)
		})
	}

	t.Run("malformed operator address", func(t *testing.T) {
		m := f.upgradeMsg(attested, tier2Bond-tier1Bond)
		m.Operator = "bogus"
		_, err := f.msgServer.UpgradeSidechain(f.ctx, m)
		require.ErrorIs(t, err, sdkerrors.ErrInvalidAddress)
	})

	t.Run("unknown sidechain", func(t *testing.T) {
		m := f.upgradeMsg(attested, tier2Bond-tier1Bond)
		m.Id = "nope"
		_, err := f.msgServer.UpgradeSidechain(f.ctx, m)
		require.ErrorIs(t, err, types.ErrSidechainNotFound)
	})

	require.Equal(t, before, f.snapshot(t))
}

func TestUpgradeSidechain_BadAdditionalBond(t *testing.T) {
	f := newActiveFixture(t)
	before := f.snapshot(t)

	cases := map[string]sdk.Coin{
		"wrong denom":   sdk.NewInt64Coin("other", tier2Bond-tier1Bond),
		"empty coin":    {},
		"negative":      {Denom: sdk.DefaultBondDenom, Amount: math.NewInt(-1)},
		"missing denom": {Denom: "", Amount: math.NewInt(tier2Bond - tier1Bond)},
	}
	for name, coin := range cases {
		t.Run(name, func(t *testing.T) {
			m := f.upgradeMsg(attested, 0)
			m.AdditionalBond = coin
			_, err := f.msgServer.UpgradeSidechain(f.ctx, m)
			require.ErrorIs(t, err, types.ErrInsufficientBond)
			require.Equal(t, before, f.snapshot(t))
		})
	}
}

func TestUpgradeSidechain_TierWithoutMinBond(t *testing.T) {
	f := newActiveFixture(t)
	f.setParamsUnchecked(t, func(p *types.Params) {
		p.MinBondByTier = p.MinBondByTier[:1] // only tier 1
	})
	before := f.snapshot(t)

	_, err := f.msgServer.UpgradeSidechain(f.ctx, f.upgradeMsg(attested, tier2Bond-tier1Bond))
	require.ErrorIs(t, err, types.ErrTierNotAvailable)
	require.Equal(t, before, f.snapshot(t))
}

func TestUpgradeSidechain_UsesCurrentParams(t *testing.T) {
	// Governance raises the tier 2 minimum before the upgrade.
	f := newActiveFixture(t)
	f.setParams(t, func(p *types.Params) {
		for i := range p.MinBondByTier {
			if p.MinBondByTier[i].Tier == attested {
				p.MinBondByTier[i].MinBond = sdk.NewInt64Coin(sdk.DefaultBondDenom, 6_000_000)
			}
		}
	})

	_, err := f.msgServer.UpgradeSidechain(f.ctx, f.upgradeMsg(attested, tier2Bond-tier1Bond))
	require.ErrorIs(t, err, types.ErrInsufficientBond)

	_, err = f.msgServer.UpgradeSidechain(f.ctx, f.upgradeMsg(attested, 6_000_000-tier1Bond))
	require.NoError(t, err)
	require.Equal(t, int64(6_000_000), f.get(t, testID).Bond.Amount.Int64())
}

func TestUpgradeSidechain_EscrowFailureChangesNothing(t *testing.T) {
	t.Run("bank refuses the transfer", func(t *testing.T) {
		f := newActiveFixture(t)
		before := f.snapshot(t)
		f.bank.failToModule = true

		_, err := f.msgServer.UpgradeSidechain(f.ctx, f.upgradeMsg(attested, tier2Bond-tier1Bond))
		require.Error(t, err)
		require.Equal(t, before, f.snapshot(t))
	})

	t.Run("operator cannot afford the top-up", func(t *testing.T) {
		f := newActiveFixture(t)
		before := f.snapshot(t)

		// The operator holds startFunds-tier1Bond; ask for more than that.
		_, err := f.msgServer.UpgradeSidechain(f.ctx, f.upgradeMsg(proven, startFunds))
		require.Error(t, err)
		require.Equal(t, before, f.snapshot(t))
	})
}
