package keeper_test

import (
	"strings"
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/lucidpay/lucidchain/x/sidechain/types"
)

func (f *fixture) slashMsg(fraction string) *types.MsgSlashSidechain {
	return &types.MsgSlashSidechain{
		Authority:     f.govS,
		Id:            testID,
		SlashFraction: math.LegacyMustNewDecFromStr(fraction),
		Reason:        "conflicting checkpoints signed at sequence 42",
	}
}

func TestSlashSidechain(t *testing.T) {
	t.Run("burns the fraction and marks the sidechain SLASHED", func(t *testing.T) {
		f := newActiveFixture(t)
		operatorBefore := f.bal(f.operator)

		res, err := f.msgServer.SlashSidechain(f.ctx, f.slashMsg("0.1"))
		require.NoError(t, err)
		require.Equal(t, sdk.NewInt64Coin(sdk.DefaultBondDenom, tier1Bond/10), res.Slashed)

		sc := f.get(t, testID)
		require.Equal(t, types.SidechainStatus_SIDECHAIN_STATUS_SLASHED, sc.Status)
		require.Equal(t, sdk.NewInt64Coin(sdk.DefaultBondDenom, tier1Bond*9/10), sc.Bond)
		require.NotNil(t, sc.SlashedAt)
		require.True(t, sc.SlashedAt.Equal(testBlockTime))
		require.Equal(t, "conflicting checkpoints signed at sequence 42", sc.SlashReason)

		// The slashed amount is burned, not paid to anyone.
		require.Equal(t, tier1Bond*9/10, f.moduleBal())
		require.Equal(t, operatorBefore, f.bal(f.operator))
		f.requireEscrowInvariant(t)
	})

	t.Run("a slashed sidechain takes no checkpoints and cannot be resumed", func(t *testing.T) {
		f := newActiveFixture(t)
		_, err := f.msgServer.SlashSidechain(f.ctx, f.slashMsg("0.1"))
		require.NoError(t, err)

		err = f.keeper.RecordCheckpoint(f.ctx, testID, 1, testHeight, []byte("h"))
		require.Error(t, err)
		_, err = f.msgServer.ResumeSidechain(f.ctx, &types.MsgResumeSidechain{Authority: f.govS, Id: testID})
		require.ErrorIs(t, err, types.ErrInvalidSidechainState)
	})

	t.Run("a suspended sidechain can be slashed", func(t *testing.T) {
		f := newActiveFixture(t)
		_, err := f.msgServer.SuspendSidechain(f.ctx, &types.MsgSuspendSidechain{Authority: f.govS, Id: testID})
		require.NoError(t, err)

		_, err = f.msgServer.SlashSidechain(f.ctx, f.slashMsg("0.1"))
		require.NoError(t, err)
		require.Equal(t, types.SidechainStatus_SIDECHAIN_STATUS_SLASHED, f.get(t, testID).Status)
	})

	t.Run("a second slash takes a fraction of the remaining bond", func(t *testing.T) {
		f := newActiveFixture(t)
		_, err := f.msgServer.SlashSidechain(f.ctx, f.slashMsg("0.5"))
		require.NoError(t, err)
		res, err := f.msgServer.SlashSidechain(f.ctx, f.slashMsg("0.5"))
		require.NoError(t, err)
		require.Equal(t, tier1Bond/4, res.Slashed.Amount.Int64())
		require.Equal(t, tier1Bond/4, f.get(t, testID).Bond.Amount.Int64())
		f.requireEscrowInvariant(t)
	})

	t.Run("a fraction of 1 burns the whole bond", func(t *testing.T) {
		f := newActiveFixture(t)
		_, err := f.msgServer.SlashSidechain(f.ctx, f.slashMsg("1"))
		require.NoError(t, err)
		require.True(t, f.get(t, testID).Bond.IsZero())
		require.Zero(t, f.moduleBal())

		// Nothing is left: the computed amount is zero.
		_, err = f.msgServer.SlashSidechain(f.ctx, f.slashMsg("0.1"))
		require.ErrorIs(t, err, types.ErrInvalidSlashAmount)
	})

	t.Run("only the authority can slash", func(t *testing.T) {
		f := newActiveFixture(t)
		msg := f.slashMsg("0.1")
		msg.Authority = f.strangerS
		_, err := f.msgServer.SlashSidechain(f.ctx, msg)
		require.ErrorIs(t, err, types.ErrUnauthorized)
		require.Equal(t, types.SidechainStatus_SIDECHAIN_STATUS_ACTIVE, f.get(t, testID).Status)
		require.Equal(t, tier1Bond, f.moduleBal())
	})

	t.Run("invalid requests change nothing", func(t *testing.T) {
		f := newActiveFixture(t)

		cases := map[string]struct {
			mutate func(m *types.MsgSlashSidechain)
			want   error
		}{
			"zero fraction":      {func(m *types.MsgSlashSidechain) { m.SlashFraction = math.LegacyZeroDec() }, types.ErrInvalidSlashFraction},
			"negative fraction":  {func(m *types.MsgSlashSidechain) { m.SlashFraction = math.LegacyMustNewDecFromStr("-0.1") }, types.ErrInvalidSlashFraction},
			"fraction above one": {func(m *types.MsgSlashSidechain) { m.SlashFraction = math.LegacyMustNewDecFromStr("1.5") }, types.ErrInvalidSlashFraction},
			"nil fraction":       {func(m *types.MsgSlashSidechain) { m.SlashFraction = math.LegacyDec{} }, types.ErrInvalidSlashFraction},
			"fraction rounds to zero": {func(m *types.MsgSlashSidechain) {
				m.SlashFraction = math.LegacyMustNewDecFromStr("0.0000001") // 0.1 of 1,000,000 base units, truncated
			}, types.ErrInvalidSlashAmount},
			"empty reason":      {func(m *types.MsgSlashSidechain) { m.Reason = "" }, types.ErrInvalidRequest},
			"reason too long":   {func(m *types.MsgSlashSidechain) { m.Reason = strings.Repeat("x", types.MaxReasonBytes+1) }, types.ErrLimitExceeded},
			"unknown sidechain": {func(m *types.MsgSlashSidechain) { m.Id = "nope" }, types.ErrSidechainNotFound},
		}
		for name, tc := range cases {
			msg := f.slashMsg("0.1")
			tc.mutate(msg)
			_, err := f.msgServer.SlashSidechain(f.ctx, msg)
			require.ErrorIs(t, err, tc.want, name)
		}

		sc := f.get(t, testID)
		require.Equal(t, types.SidechainStatus_SIDECHAIN_STATUS_ACTIVE, sc.Status)
		require.Equal(t, tier1Bond, sc.Bond.Amount.Int64())
		require.Nil(t, sc.SlashedAt)
		require.Equal(t, tier1Bond, f.moduleBal())
	})
}

func TestSlashSidechain_Exit(t *testing.T) {
	t.Run("a slashed sidechain can exit and gets the rest of its bond back", func(t *testing.T) {
		f := newActiveFixture(t)
		_, err := f.msgServer.SlashSidechain(f.ctx, f.slashMsg("0.1"))
		require.NoError(t, err)
		operatorBefore := f.bal(f.operator)

		_, err = f.msgServer.InitiateSidechainExit(f.ctx, f.exitMsg())
		require.NoError(t, err)
		require.Equal(t, types.SidechainStatus_SIDECHAIN_STATUS_EXITED, f.get(t, testID).Status)

		f.advance(14 * day)
		require.NoError(t, f.keeper.ProcessExits(f.ctx))
		require.Equal(t, operatorBefore+tier1Bond*9/10, f.bal(f.operator))
		require.Zero(t, f.moduleBal())
		f.requireEscrowInvariant(t)
	})

	t.Run("a slash during the exit cooldown burns and keeps the status EXITED", func(t *testing.T) {
		f := newActiveFixture(t)
		_, err := f.msgServer.InitiateSidechainExit(f.ctx, f.exitMsg())
		require.NoError(t, err)
		operatorBefore := f.bal(f.operator)

		f.advance(day)
		_, err = f.msgServer.SlashSidechain(f.ctx, f.slashMsg("0.5"))
		require.NoError(t, err)
		sc := f.get(t, testID)
		require.Equal(t, types.SidechainStatus_SIDECHAIN_STATUS_EXITED, sc.Status)
		require.Equal(t, tier1Bond/2, sc.Bond.Amount.Int64())

		f.advance(13 * day)
		require.NoError(t, f.keeper.ProcessExits(f.ctx))
		require.Equal(t, operatorBefore+tier1Bond/2, f.bal(f.operator))
		require.Zero(t, f.moduleBal())
		f.requireEscrowInvariant(t)
	})

	t.Run("after the bond is returned there is nothing to slash", func(t *testing.T) {
		f := newActiveFixture(t)
		_, err := f.msgServer.InitiateSidechainExit(f.ctx, f.exitMsg())
		require.NoError(t, err)
		f.advance(14 * day)
		require.NoError(t, f.keeper.ProcessExits(f.ctx))

		_, err = f.msgServer.SlashSidechain(f.ctx, f.slashMsg("0.1"))
		require.ErrorIs(t, err, types.ErrInvalidSlashAmount)
	})
}

func TestSlashBond(t *testing.T) {
	f := newActiveFixture(t)
	const reason = "conflicting checkpoints"

	require.NoError(t, f.keeper.SlashBond(f.ctx, testID, reason, math.NewInt(250_000)))
	require.Equal(t, tier1Bond-250_000, f.get(t, testID).Bond.Amount.Int64())
	f.requireEscrowInvariant(t)

	err := f.keeper.SlashBond(f.ctx, testID, reason, math.NewInt(tier1Bond))
	require.ErrorIs(t, err, types.ErrInvalidSlashAmount, "more than the remaining bond")
	err = f.keeper.SlashBond(f.ctx, testID, reason, math.ZeroInt())
	require.ErrorIs(t, err, types.ErrInvalidSlashAmount)

	require.NoError(t, f.keeper.SlashBond(f.ctx, testID, reason, math.NewInt(tier1Bond-250_000)))
	require.True(t, f.get(t, testID).Bond.IsZero())

	err = f.keeper.SlashBond(f.ctx, testID, reason, math.NewInt(1))
	require.ErrorIs(t, err, types.ErrNothingToSlash)
	f.requireEscrowInvariant(t)
}
