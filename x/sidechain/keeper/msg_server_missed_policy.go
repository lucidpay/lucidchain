package keeper

import (
	"context"

	errorsmod "cosmossdk.io/errors"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/lucidpay/lucidchain/x/sidechain/types"
)

// SetSidechainMissedPolicy sets or clears a sidechain's missed-checkpoint
// policy override. Authority only: the operator is the party being penalized,
// so it must not be able to exempt itself.
//
// msg.Policy == nil clears the override and the sidechain goes back to its
// tier's policy. An override of {action: NONE, suspend_after_strikes: 0}
// exempts the sidechain: misses are still recorded as strikes, nothing else
// happens.
//
// The sidechain's current deadline and strike counters are not touched. The
// override's grace period applies from the next window; its action, penalty
// and strike limit apply to the next miss.
func (ms msgServer) SetSidechainMissedPolicy(ctx context.Context, msg *types.MsgSetSidechainMissedPolicy) (*types.MsgSetSidechainMissedPolicyResponse, error) {
	if err := ms.checkAuthority(ms.authority, msg.Authority); err != nil {
		return nil, err
	}
	if err := types.ValidateMissedPolicyOverride(msg.Policy); err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidRequest, err.Error())
	}

	sc, err := ms.loadSidechain(ctx, msg.Id)
	if err != nil {
		return nil, err
	}
	if sc.Status == types.SidechainStatus_SIDECHAIN_STATUS_EXITED {
		return nil, errorsmod.Wrapf(types.ErrInvalidSidechainState, "sidechain %q has exited", sc.Id)
	}

	sc.MissedPolicyOverride = msg.Policy
	if err := ms.SetSidechain(ctx, sc); err != nil {
		return nil, err
	}

	source := "tier"
	if msg.Policy != nil {
		source = "sidechain_override"
	}
	emit(sdk.UnwrapSDKContext(ctx), "sidechain_missed_policy_set",
		attr("sidechain_id", sc.Id),
		attr("policy_source", source),
	)
	return &types.MsgSetSidechainMissedPolicyResponse{}, nil
}
