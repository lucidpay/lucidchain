package keeper

// Place this in x/sidechain/keeper/msg_server_upgrade_sidechain.go.
// It uses only helpers that already exist in msg_server_sidechain.go:
// addressCodec, Params, loadSidechain, checkOperator, escrowBond, emit, attr,
// types.ValidTier, params.MinBondFor and the existing error values.

import (
	"context"

	errorsmod "cosmossdk.io/errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"

	"github.com/lucidpay/lucidchain/x/sidechain/types"
)

// UpgradeSidechain raises a sidechain's assurance tier. Operator only.
//
// The bond after the top-up (current bond plus additional_bond) must be at
// least the new tier's minimum, in the same denom. Tiers only go up (skipping
// a tier is fine); downgrades are not supported because releasing bond would
// bypass the exit cooldown. All checks run before anything is escrowed, so
// nothing is escrowed or stored if a check fails.
func (ms msgServer) UpgradeSidechain(ctx context.Context, msg *types.MsgUpgradeSidechain) (*types.MsgUpgradeSidechainResponse, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)

	operator, err := ms.addressCodec.StringToBytes(msg.Operator)
	if err != nil {
		return nil, errorsmod.Wrap(sdkerrors.ErrInvalidAddress, "invalid operator address")
	}

	params, err := ms.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	sc, err := ms.loadSidechain(ctx, msg.Id)
	if err != nil {
		return nil, err
	}
	if err := ms.checkOperator(sc, msg.Operator); err != nil {
		return nil, err
	}

	// PENDING, ACTIVE and SUSPENDED may upgrade: a tier change does not alter
	// status and extra bond only adds collateral. A SLASHED sidechain must not
	// buy its way back toward good standing, and an EXITED one is leaving.
	switch sc.Status {
	case types.SidechainStatus_SIDECHAIN_STATUS_PENDING,
		types.SidechainStatus_SIDECHAIN_STATUS_ACTIVE,
		types.SidechainStatus_SIDECHAIN_STATUS_SUSPENDED:
	default:
		return nil, errorsmod.Wrapf(types.ErrInvalidSidechainState,
			"tier cannot be upgraded while the sidechain is %s", sc.Status)
	}

	if !types.ValidTier(msg.NewTier) {
		return nil, errorsmod.Wrapf(types.ErrInvalidRequest, "invalid tier %s", msg.NewTier)
	}
	if msg.NewTier <= sc.Tier {
		return nil, errorsmod.Wrapf(types.ErrInvalidRequest,
			"new tier %s must be higher than the current tier %s", msg.NewTier, sc.Tier)
	}
	minBond, ok := params.MinBondFor(msg.NewTier)
	if !ok {
		return nil, errorsmod.Wrapf(types.ErrTierNotAvailable, "%s", msg.NewTier)
	}

	// Zero is allowed (the current bond may already cover the new tier), but
	// the denom must be valid and must match. Checking denoms first also keeps
	// the amount arithmetic below meaningful.
	if err := msg.AdditionalBond.Validate(); err != nil {
		return nil, errorsmod.Wrapf(types.ErrInsufficientBond, "invalid additional_bond: %s", err)
	}
	if msg.AdditionalBond.Denom != minBond.Denom || sc.Bond.Denom != minBond.Denom {
		return nil, errorsmod.Wrapf(types.ErrInsufficientBond,
			"bond must be in %s (current bond %s, additional_bond %s)",
			minBond.Denom, sc.Bond, msg.AdditionalBond)
	}

	// Compare the resulting bond with the new minimum, not the old minimum: a
	// sidechain slashed down to 900000 that wants a 5000000 tier needs 4100000.
	newBond := sdk.NewCoin(sc.Bond.Denom, sc.Bond.Amount.Add(msg.AdditionalBond.Amount))
	if newBond.Amount.LT(minBond.Amount) {
		return nil, errorsmod.Wrapf(types.ErrInsufficientBond,
			"%s needs a bond of at least %s; have %s after the top-up, so %s more is needed",
			msg.NewTier, minBond, newBond, minBond.Amount.Sub(newBond.Amount))
	}

	// Checks done. Escrow, then write the record.
	if msg.AdditionalBond.IsPositive() {
		if err := ms.escrowBond(ctx, operator, msg.AdditionalBond); err != nil {
			return nil, err
		}
	}

	previous := sc.Tier
	sc.Tier = msg.NewTier
	sc.Bond = newBond
	if err := ms.Sidechains.Set(ctx, sc.Id, sc); err != nil {
		return nil, err
	}

	emit(sdkCtx, "sidechain_tier_upgraded",
		attr("sidechain_id", sc.Id),
		attr("previous_tier", previous.String()),
		attr("new_tier", sc.Tier.String()),
		attr("additional_bond", msg.AdditionalBond.String()),
		attr("bond", sc.Bond.String()),
	)
	return &types.MsgUpgradeSidechainResponse{
		PreviousTier: previous,
		NewTier:      sc.Tier,
		Bond:         sc.Bond,
	}, nil
}
