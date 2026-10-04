package keeper

import (
	"bytes"
	"context"
	"time"

	errorsmod "cosmossdk.io/errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"

	"github.com/lucidpay/lucidchain/x/attestor/types"
)

// RaiseDispute challenges an ACTIVE attestation inside its schema's dispute
// window. The challenger's bond is escrowed, the attestation moves to
// DISPUTED, and the attestor is suspended until the dispute is resolved.
//
// An attestation can be disputed at most once: only ACTIVE attestations can be
// challenged, and a resolved dispute leaves the attestation in a final status.
// The attestor's own operator cannot challenge its attestations.
func (ms msgServer) RaiseDispute(ctx context.Context, msg *types.MsgRaiseDispute) (*types.MsgRaiseDisputeResponse, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)

	challenger, err := ms.addressCodec.StringToBytes(msg.Challenger)
	if err != nil {
		return nil, errorsmod.Wrap(sdkerrors.ErrInvalidAddress, "invalid challenger address")
	}

	params, err := ms.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateText("evidence_uri", msg.EvidenceUri, int(params.MaxUriBytes), true); err != nil {
		return nil, err
	}
	if msg.BondAmount.IsNil() || msg.BondAmount.LT(params.MinDisputeBond) {
		return nil, errorsmod.Wrapf(types.ErrInsufficientBond,
			"dispute bond must be at least %s%s", params.MinDisputeBond, params.BondDenom)
	}

	attestation, err := ms.GetAttestation(ctx, msg.AttestationId)
	if err != nil {
		return nil, err
	}
	if attestation.Status != types.AttestationStatus_ATTESTATION_STATUS_ACTIVE {
		return nil, errorsmod.Wrapf(types.ErrInvalidAttestationState,
			"attestation %s is %s, only ACTIVE attestations can be disputed", attestation.Id, attestation.Status)
	}

	schema, err := ms.GetSchema(ctx, attestation.SchemaId)
	if err != nil {
		return nil, err
	}
	now := sdkCtx.BlockTime()
	deadline := attestation.IssuedAt.Add(time.Duration(schema.DisputeWindowSeconds) * time.Second)
	if now.After(deadline) {
		return nil, errorsmod.Wrapf(types.ErrDisputeWindowClosed,
			"window closed at %s", deadline.UTC().Format(time.RFC3339))
	}

	attestor, err := ms.GetAttestor(ctx, attestation.AttestorId)
	if err != nil {
		return nil, err
	}
	operator, err := ms.addressCodec.StringToBytes(attestor.Operator)
	if err != nil {
		return nil, errorsmod.Wrap(sdkerrors.ErrInvalidAddress, "stored operator address is invalid")
	}
	if bytes.Equal(operator, challenger) {
		return nil, errorsmod.Wrap(types.ErrUnauthorized, "an attestor's operator cannot dispute its own attestation")
	}

	disputeID := types.DisputeID(attestation.Id)
	exists, err := ms.Disputes.Has(ctx, disputeID)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, errorsmod.Wrapf(types.ErrDisputeExists, "%s", disputeID)
	}

	if err := ms.escrowBond(ctx, challenger, params, msg.BondAmount); err != nil {
		return nil, err
	}

	dispute := types.Dispute{
		Id:            disputeID,
		AttestationId: attestation.Id,
		Challenger:    msg.Challenger,
		EvidenceUri:   msg.EvidenceUri,
		BondAmount:    msg.BondAmount,
		RaisedAt:      now,
	}
	if err := ms.Disputes.Set(ctx, dispute.Id, dispute); err != nil {
		return nil, err
	}

	attestation.Status = types.AttestationStatus_ATTESTATION_STATUS_DISPUTED
	if err := ms.Attestations.Set(ctx, attestation.Id, attestation); err != nil {
		return nil, err
	}

	attestor.OpenDisputes++
	if attestor.Status == types.AttestorStatus_ATTESTOR_STATUS_ACTIVE {
		attestor.Status = types.AttestorStatus_ATTESTOR_STATUS_SUSPENDED
	}
	if err := ms.Attestors.Set(ctx, attestor.Id, attestor); err != nil {
		return nil, err
	}

	emit(sdkCtx, "dispute_raised",
		attr("dispute_id", dispute.Id),
		attr("attestation_id", attestation.Id),
		attr("attestor_id", attestor.Id),
		attr("challenger", dispute.Challenger),
	)
	return &types.MsgRaiseDisputeResponse{DisputeId: dispute.Id}, nil
}

// ResolveDispute settles an open dispute. Only the dispute resolver authority
// (params.dispute_resolver_authority, or x/gov when unset) can call it.
//
// Upheld (attestation overturned):
//   - the attestor's bond is slashed by params.slash_fraction
//   - the challenger gets its dispute bond back plus
//     params.challenger_reward_fraction of the slashed amount
//   - the rest of the slashed amount is burned
//   - the attestor's reputation drops by 2 and it becomes SLASHED
//     (an EXITED attestor stays EXITED and just loses the slashed part of its bond)
//
// Not upheld (attestation stands):
//   - the challenger's dispute bond is burned
//   - the attestor is reinstated to ACTIVE once its last open dispute is resolved
func (ms msgServer) ResolveDispute(ctx context.Context, msg *types.MsgResolveDispute) (*types.MsgResolveDisputeResponse, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)

	resolver, err := ms.GetDisputeResolver(ctx)
	if err != nil {
		return nil, err
	}
	if err := ms.checkAuthority(resolver, msg.Authority); err != nil {
		return nil, err
	}

	params, err := ms.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateText("rationale_uri", msg.RationaleUri, int(params.MaxUriBytes), false); err != nil {
		return nil, err
	}

	dispute, err := ms.GetDispute(ctx, msg.DisputeId)
	if err != nil {
		return nil, err
	}
	if dispute.Resolved {
		return nil, errorsmod.Wrapf(types.ErrDisputeResolved, "%s", dispute.Id)
	}
	attestation, err := ms.GetAttestation(ctx, dispute.AttestationId)
	if err != nil {
		return nil, err
	}
	attestor, err := ms.GetAttestor(ctx, attestation.AttestorId)
	if err != nil {
		return nil, err
	}
	challenger, err := ms.addressCodec.StringToBytes(dispute.Challenger)
	if err != nil {
		return nil, errorsmod.Wrap(sdkerrors.ErrInvalidAddress, "stored challenger address is invalid")
	}

	now := sdkCtx.BlockTime()
	dispute.Resolved = true
	dispute.Upheld = msg.Upheld
	dispute.ResolvedAt = &now
	dispute.RationaleUri = msg.RationaleUri

	if msg.Upheld {
		slashed := params.SlashFraction.MulInt(attestor.BondAmount).TruncateInt()
		if slashed.GT(attestor.BondAmount) {
			slashed = attestor.BondAmount
		}
		reward := params.ChallengerRewardFraction.MulInt(slashed).TruncateInt()
		burned := slashed.Sub(reward)

		if err := ms.releaseBond(ctx, challenger, params, dispute.BondAmount.Add(reward)); err != nil {
			return nil, errorsmod.Wrap(err, "failed to pay challenger")
		}
		if err := ms.burnBond(ctx, params, burned); err != nil {
			return nil, errorsmod.Wrap(err, "failed to burn slashed bond")
		}

		attestor.BondAmount = attestor.BondAmount.Sub(slashed)
		attestor.ReputationScore -= 2
		if attestor.Status != types.AttestorStatus_ATTESTOR_STATUS_EXITED {
			attestor.Status = types.AttestorStatus_ATTESTOR_STATUS_SLASHED
		}
		attestation.Status = types.AttestationStatus_ATTESTATION_STATUS_OVERTURNED

		emit(sdkCtx, "attestor_slashed",
			attr("attestor_id", attestor.Id),
			attr("slashed", slashed.String()),
			attr("challenger_reward", reward.String()),
			attr("burned", burned.String()),
		)
	} else {
		if err := ms.burnBond(ctx, params, dispute.BondAmount); err != nil {
			return nil, errorsmod.Wrap(err, "failed to burn forfeited dispute bond")
		}
		attestation.Status = types.AttestationStatus_ATTESTATION_STATUS_UPHELD
	}

	if attestor.OpenDisputes > 0 {
		attestor.OpenDisputes--
	}
	if attestor.OpenDisputes == 0 && attestor.Status == types.AttestorStatus_ATTESTOR_STATUS_SUSPENDED {
		// SUSPENDED is only ever set by an open dispute.
		attestor.Status = types.AttestorStatus_ATTESTOR_STATUS_ACTIVE
	}

	if err := ms.Disputes.Set(ctx, dispute.Id, dispute); err != nil {
		return nil, err
	}
	if err := ms.Attestations.Set(ctx, attestation.Id, attestation); err != nil {
		return nil, err
	}
	if err := ms.Attestors.Set(ctx, attestor.Id, attestor); err != nil {
		return nil, err
	}

	emit(sdkCtx, "dispute_resolved",
		attr("dispute_id", dispute.Id),
		attr("attestation_id", attestation.Id),
		attr("upheld", boolStr(msg.Upheld)),
	)
	return &types.MsgResolveDisputeResponse{}, nil
}
