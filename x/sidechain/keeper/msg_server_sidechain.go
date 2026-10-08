package keeper

import (
	"context"
	"time"

	errorsmod "cosmossdk.io/errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"

	"github.com/lucidpay/lucidchain/x/sidechain/types"
)

// RegisterSidechain registers a new sidechain and escrows its bond.
//
// The sidechain is created PENDING and needs ActivateSidechain from the
// module authority, unless params.auto_activate is set, in which case it is
// created ACTIVE and the bond is the only gate. Nothing is stored and nothing
// is escrowed if any check fails.
func (ms msgServer) RegisterSidechain(ctx context.Context, msg *types.MsgRegisterSidechain) (*types.MsgRegisterSidechainResponse, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)

	operator, err := ms.addressCodec.StringToBytes(msg.Operator)
	if err != nil {
		return nil, errorsmod.Wrap(sdkerrors.ErrInvalidAddress, "invalid operator address")
	}

	params, err := ms.Params.Get(ctx)
	if err != nil {
		return nil, err
	}

	if err := types.ValidateID(msg.Id); err != nil {
		return nil, err
	}
	if err := types.ValidateText("name", msg.Name, types.MaxNameBytes, true); err != nil {
		return nil, err
	}
	if err := types.ValidateText("metadata_uri", msg.MetadataUri, types.MaxURIBytes, false); err != nil {
		return nil, err
	}
	if err := types.ValidateCheckpointInterval(msg.CheckpointIntervalSeconds); err != nil {
		return nil, err
	}
	if !types.ValidTier(msg.Tier) {
		return nil, errorsmod.Wrapf(types.ErrInvalidRequest, "invalid tier %s", msg.Tier)
	}
	minBond, ok := params.MinBondFor(msg.Tier)
	if !ok {
		return nil, errorsmod.Wrapf(types.ErrTierNotAvailable, "%s", msg.Tier)
	}
	if err := types.ValidateSignerSet(msg.SignerKeys, msg.SignatureThreshold, params); err != nil {
		return nil, err
	}

	if err := msg.Bond.Validate(); err != nil {
		return nil, errorsmod.Wrapf(types.ErrInsufficientBond, "invalid bond: %s", err)
	}
	if msg.Bond.Denom != minBond.Denom {
		return nil, errorsmod.Wrapf(types.ErrInsufficientBond,
			"bond must be in %s, got %s", minBond.Denom, msg.Bond.Denom)
	}
	if msg.Bond.Amount.LT(minBond.Amount) {
		return nil, errorsmod.Wrapf(types.ErrInsufficientBond,
			"%s needs a bond of at least %s, got %s", msg.Tier, minBond, msg.Bond)
	}

	exists, err := ms.Sidechains.Has(ctx, msg.Id)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, errorsmod.Wrapf(types.ErrSidechainExists, "%q", msg.Id)
	}

	if err := ms.escrowBond(ctx, operator, msg.Bond); err != nil {
		return nil, err
	}

	now := sdkCtx.BlockTime()
	status := types.SidechainStatus_SIDECHAIN_STATUS_PENDING
	var activatedAt *time.Time
	if params.AutoActivate {
		status = types.SidechainStatus_SIDECHAIN_STATUS_ACTIVE
		activatedAt = &now
	}

	sc := types.Sidechain{
		Id:                        msg.Id,
		Name:                      msg.Name,
		Operator:                  msg.Operator,
		SignerKeys:                msg.SignerKeys,
		SignatureThreshold:        msg.SignatureThreshold,
		SignerSetVersion:          1,
		Tier:                      msg.Tier,
		Status:                    status,
		Bond:                      msg.Bond,
		CheckpointIntervalSeconds: msg.CheckpointIntervalSeconds,
		RegisteredAt:              now,
		MetadataUri:               msg.MetadataUri,
		ActivatedAt:               activatedAt,
	}
	if err := ms.Sidechains.Set(ctx, sc.Id, sc); err != nil {
		return nil, err
	}

	emit(sdkCtx, "sidechain_registered",
		attr("sidechain_id", sc.Id),
		attr("operator", sc.Operator),
		attr("tier", sc.Tier.String()),
		attr("status", sc.Status.String()),
		attr("bond", sc.Bond.String()),
	)
	return &types.MsgRegisterSidechainResponse{Id: sc.Id, Status: sc.Status}, nil
}

// ActivateSidechain moves a PENDING sidechain to ACTIVE. Authority only.
func (ms msgServer) ActivateSidechain(ctx context.Context, msg *types.MsgActivateSidechain) (*types.MsgActivateSidechainResponse, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)

	if err := ms.checkAuthority(ms.authority, msg.Authority); err != nil {
		return nil, err
	}
	sc, err := ms.loadSidechain(ctx, msg.Id)
	if err != nil {
		return nil, err
	}
	if sc.Status != types.SidechainStatus_SIDECHAIN_STATUS_PENDING {
		return nil, errorsmod.Wrapf(types.ErrInvalidSidechainState,
			"sidechain %q is %s, only PENDING sidechains can be activated", sc.Id, sc.Status)
	}

	now := sdkCtx.BlockTime()
	sc.Status = types.SidechainStatus_SIDECHAIN_STATUS_ACTIVE
	sc.ActivatedAt = &now
	if err := ms.Sidechains.Set(ctx, sc.Id, sc); err != nil {
		return nil, err
	}

	emit(sdkCtx, "sidechain_activated", attr("sidechain_id", sc.Id))
	return &types.MsgActivateSidechainResponse{}, nil
}

// UpdateSidechainSigners replaces the signer keys and threshold and bumps
// signer_set_version. Checkpoints already stored keep the version they were
// signed under; checkpoints in flight signed under the old version are
// rejected by x/checkpoint, so rotate between checkpoints.
func (ms msgServer) UpdateSidechainSigners(ctx context.Context, msg *types.MsgUpdateSidechainSigners) (*types.MsgUpdateSidechainSignersResponse, error) {
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

	switch sc.Status {
	case types.SidechainStatus_SIDECHAIN_STATUS_PENDING,
		types.SidechainStatus_SIDECHAIN_STATUS_ACTIVE,
		types.SidechainStatus_SIDECHAIN_STATUS_SUSPENDED:
	default:
		return nil, errorsmod.Wrapf(types.ErrInvalidSidechainState,
			"signers cannot be updated while the sidechain is %s", sc.Status)
	}

	if err := types.ValidateSignerSet(msg.SignerKeys, msg.SignatureThreshold, params); err != nil {
		return nil, err
	}

	sc.SignerKeys = msg.SignerKeys
	sc.SignatureThreshold = msg.SignatureThreshold
	sc.SignerSetVersion++
	if err := ms.Sidechains.Set(ctx, sc.Id, sc); err != nil {
		return nil, err
	}

	emit(sdk.UnwrapSDKContext(ctx), "sidechain_signers_updated",
		attr("sidechain_id", sc.Id), attr("signer_set_version", uitoa(sc.SignerSetVersion)))
	return &types.MsgUpdateSidechainSignersResponse{SignerSetVersion: sc.SignerSetVersion}, nil
}

// UpdateSidechain changes the name, metadata URI and checkpoint interval. Operator only.
func (ms msgServer) UpdateSidechain(ctx context.Context, msg *types.MsgUpdateSidechain) (*types.MsgUpdateSidechainResponse, error) {
	sc, err := ms.loadSidechain(ctx, msg.Id)
	if err != nil {
		return nil, err
	}
	if err := ms.checkOperator(sc, msg.Operator); err != nil {
		return nil, err
	}
	if sc.Status == types.SidechainStatus_SIDECHAIN_STATUS_EXITED {
		return nil, errorsmod.Wrapf(types.ErrInvalidSidechainState, "sidechain %q has exited", sc.Id)
	}

	if err := types.ValidateText("name", msg.Name, types.MaxNameBytes, true); err != nil {
		return nil, err
	}
	if err := types.ValidateText("metadata_uri", msg.MetadataUri, types.MaxURIBytes, false); err != nil {
		return nil, err
	}
	if err := types.ValidateCheckpointInterval(msg.CheckpointIntervalSeconds); err != nil {
		return nil, err
	}

	sc.Name = msg.Name
	sc.MetadataUri = msg.MetadataUri
	sc.CheckpointIntervalSeconds = msg.CheckpointIntervalSeconds
	if err := ms.Sidechains.Set(ctx, sc.Id, sc); err != nil {
		return nil, err
	}

	emit(sdk.UnwrapSDKContext(ctx), "sidechain_updated", attr("sidechain_id", sc.Id))
	return &types.MsgUpdateSidechainResponse{}, nil
}

// InitiateSidechainExit deregisters a sidechain. It stops accepting
// checkpoints immediately, and its bond is returned by ProcessExits once
// params.exit_cooldown_seconds have passed. Records already accepted by
// x/checkpoint, x/attestor and x/proofs are unaffected.
//
// SLASHED sidechains may exit too, to recover whatever bond is left.
func (ms msgServer) InitiateSidechainExit(ctx context.Context, msg *types.MsgInitiateSidechainExit) (*types.MsgInitiateSidechainExitResponse, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)

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

	switch sc.Status {
	case types.SidechainStatus_SIDECHAIN_STATUS_PENDING,
		types.SidechainStatus_SIDECHAIN_STATUS_ACTIVE,
		types.SidechainStatus_SIDECHAIN_STATUS_SUSPENDED,
		types.SidechainStatus_SIDECHAIN_STATUS_SLASHED:
	default:
		return nil, errorsmod.Wrapf(types.ErrInvalidSidechainState,
			"sidechain %q is already %s", sc.Id, sc.Status)
	}

	now := sdkCtx.BlockTime()
	returnAt := now.Add(time.Duration(params.ExitCooldownSeconds) * time.Second)

	sc.Status = types.SidechainStatus_SIDECHAIN_STATUS_EXITED
	sc.ExitRequestedAt = &now
	sc.BondReturnAt = &returnAt
	if err := ms.Sidechains.Set(ctx, sc.Id, sc); err != nil {
		return nil, err
	}
	if err := ms.ExitQueue.Set(ctx, exitKey(returnAt, sc.Id)); err != nil {
		return nil, err
	}

	emit(sdkCtx, "sidechain_exit_initiated",
		attr("sidechain_id", sc.Id), attr("bond_return_at", returnAt.UTC().Format(time.RFC3339)))
	return &types.MsgInitiateSidechainExitResponse{}, nil
}

// SuspendSidechain halts an ACTIVE sidechain: it stops accepting checkpoints
// until ResumeSidechain. Authority only.
func (ms msgServer) SuspendSidechain(ctx context.Context, msg *types.MsgSuspendSidechain) (*types.MsgSuspendSidechainResponse, error) {
	if err := ms.checkAuthority(ms.authority, msg.Authority); err != nil {
		return nil, err
	}
	sc, err := ms.loadSidechain(ctx, msg.Id)
	if err != nil {
		return nil, err
	}
	if sc.Status != types.SidechainStatus_SIDECHAIN_STATUS_ACTIVE {
		return nil, errorsmod.Wrapf(types.ErrInvalidSidechainState,
			"sidechain %q is %s, only ACTIVE sidechains can be suspended", sc.Id, sc.Status)
	}

	sc.Status = types.SidechainStatus_SIDECHAIN_STATUS_SUSPENDED
	if err := ms.Sidechains.Set(ctx, sc.Id, sc); err != nil {
		return nil, err
	}

	emit(sdk.UnwrapSDKContext(ctx), "sidechain_suspended", attr("sidechain_id", sc.Id))
	return &types.MsgSuspendSidechainResponse{}, nil
}

// ResumeSidechain returns a SUSPENDED sidechain to ACTIVE. Authority only.
func (ms msgServer) ResumeSidechain(ctx context.Context, msg *types.MsgResumeSidechain) (*types.MsgResumeSidechainResponse, error) {
	if err := ms.checkAuthority(ms.authority, msg.Authority); err != nil {
		return nil, err
	}
	sc, err := ms.loadSidechain(ctx, msg.Id)
	if err != nil {
		return nil, err
	}
	if sc.Status != types.SidechainStatus_SIDECHAIN_STATUS_SUSPENDED {
		return nil, errorsmod.Wrapf(types.ErrInvalidSidechainState,
			"sidechain %q is %s, only SUSPENDED sidechains can be resumed", sc.Id, sc.Status)
	}

	sc.Status = types.SidechainStatus_SIDECHAIN_STATUS_ACTIVE
	if err := ms.Sidechains.Set(ctx, sc.Id, sc); err != nil {
		return nil, err
	}

	emit(sdk.UnwrapSDKContext(ctx), "sidechain_resumed", attr("sidechain_id", sc.Id))
	return &types.MsgResumeSidechainResponse{}, nil
}
