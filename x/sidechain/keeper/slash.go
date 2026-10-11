package keeper

import (
	"context"

	errorsmod "cosmossdk.io/errors"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/lucidpay/lucidchain/x/sidechain/types"
)

// SlashBond implements the SidechainKeeper method from spec §2.4: it burns an
// exact amount of the sidechain's bond. No authorization is done here; callers
// must have established the fault themselves.
func (k Keeper) SlashBond(ctx context.Context, id, reason string, amount math.Int) error {
	_, err := k.slash(ctx, id, amount, reason)
	return err
}

// Slash burns `fraction` (0 < f <= 1) of the current bond. Used by
// MsgSlashSidechain. Returns the coin that was burned.
func (k Keeper) Slash(ctx context.Context, id string, fraction math.LegacyDec, reason string) (sdk.Coin, error) {
	if fraction.IsNil() || !fraction.IsPositive() || fraction.GT(math.LegacyOneDec()) {
		return sdk.Coin{}, errorsmod.Wrapf(types.ErrInvalidSlashFraction,
			"must be in (0, 1], got %s", fraction)
	}
	sc, err := k.loadSidechain(ctx, id)
	if err != nil {
		return sdk.Coin{}, err
	}
	amt := sc.Bond.Amount.ToLegacyDec().Mul(fraction).TruncateInt()
	return k.slash(ctx, id, amt, reason)
}

func (k Keeper) slash(ctx context.Context, id string, amount math.Int, reason string) (sdk.Coin, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)

	if amount.IsNil() || !amount.IsPositive() {
		return sdk.Coin{}, errorsmod.Wrap(types.ErrInvalidSlashAmount, "amount must be positive")
	}
	if err := types.ValidateText("reason", reason, types.MaxReasonBytes, true); err != nil {
		return sdk.Coin{}, err
	}

	sc, err := k.loadSidechain(ctx, id)
	if err != nil {
		return sdk.Coin{}, err
	}
	if !sc.Bond.IsPositive() {
		return sdk.Coin{}, errorsmod.Wrapf(types.ErrNothingToSlash, "%q", id)
	}
	if amount.GT(sc.Bond.Amount) {
		return sdk.Coin{}, errorsmod.Wrapf(types.ErrInvalidSlashAmount,
			"%s exceeds remaining bond %s", amount, sc.Bond)
	}

	slashed := sdk.NewCoin(sc.Bond.Denom, amount)
	if err := k.burnBond(ctx, slashed); err != nil {
		return sdk.Coin{}, errorsmod.Wrap(err, "failed to burn slashed bond")
	}

	now := sdkCtx.BlockTime()
	sc.Bond = sc.Bond.Sub(slashed)
	if sc.Status != types.SidechainStatus_SIDECHAIN_STATUS_EXITED {
		sc.Status = types.SidechainStatus_SIDECHAIN_STATUS_SLASHED
	}
	sc.SlashedAt = &now
	sc.SlashReason = reason
	if err := k.Sidechains.Set(ctx, sc.Id, sc); err != nil {
		return sdk.Coin{}, err
	}

	emit(sdkCtx, "sidechain_slashed",
		attr("sidechain_id", sc.Id),
		attr("slashed", slashed.String()),
		attr("remaining_bond", sc.Bond.String()),
		attr("status", sc.Status.String()),
		attr("reason", reason),
	)
	return slashed, nil
}