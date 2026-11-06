package keeper

import (
	"context"
	"encoding/hex"
	"errors"
	"time"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"

	"github.com/lucidpay/lucidchain/x/attestor/types"
)

// RegisterAttestor registers a new attestor in PENDING status and escrows its bond.
// Governance must call ActivateAttestor before it can attest.
func (ms msgServer) RegisterAttestor(ctx context.Context, msg *types.MsgRegisterAttestor) (*types.MsgRegisterAttestorResponse, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)

	operator, err := ms.addressCodec.StringToBytes(msg.Operator)
	if err != nil {
		return nil, errorsmod.Wrap(sdkerrors.ErrInvalidAddress, "invalid operator address")
	}

	params, err := ms.Params.Get(ctx)
	if err != nil {
		return nil, err
	}

	if err := types.ValidateID("id", msg.Id); err != nil {
		return nil, err
	}
	if err := validateText("name", msg.Name, types.MaxNameBytes, true); err != nil {
		return nil, err
	}
	if err := validateText("credential_uri", msg.CredentialUri, int(params.MaxUriBytes), false); err != nil {
		return nil, err
	}
	if err := types.ValidateSchemaIDList("authorized_schema_ids", msg.AuthorizedSchemaIds); err != nil {
		return nil, err
	}
	if err := types.ValidateSignerSet(msg.SignerKeys, msg.SignatureThreshold, params); err != nil {
		return nil, err
	}
	if msg.BondAmount.IsNil() || msg.BondAmount.LT(params.MinAttestorBond) {
		return nil, errorsmod.Wrapf(types.ErrInsufficientBond,
			"bond must be at least %s%s", params.MinAttestorBond, params.BondDenom)
	}

	exists, err := ms.Attestors.Has(ctx, msg.Id)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, errorsmod.Wrapf(types.ErrAttestorExists, "%q", msg.Id)
	}

	for _, sid := range msg.AuthorizedSchemaIds {
		schema, err := ms.GetSchema(ctx, sid)
		if err != nil {
			return nil, err
		}
		if !schema.Active {
			return nil, errorsmod.Wrapf(types.ErrSchemaInactive, "%q", sid)
		}
	}

	if err := ms.escrowBond(ctx, operator, params, msg.BondAmount); err != nil {
		return nil, err
	}

	attestor := types.Attestor{
		Id:                  msg.Id,
		Name:                msg.Name,
		Operator:            msg.Operator,
		AuthorizedSchemaIds: msg.AuthorizedSchemaIds,
		SignerKeys:          msg.SignerKeys,
		SignatureThreshold:  msg.SignatureThreshold,
		Status:              types.AttestorStatus_ATTESTOR_STATUS_PENDING,
		BondAmount:          msg.BondAmount,
		RegisteredAt:        sdkCtx.BlockTime(),
		CredentialUri:       msg.CredentialUri,
		SignerSetVersion:    1,
	}
	if err := ms.Attestors.Set(ctx, attestor.Id, attestor); err != nil {
		return nil, err
	}

	emit(sdkCtx, "attestor_registered", attr("attestor_id", attestor.Id), attr("operator", attestor.Operator))
	return &types.MsgRegisterAttestorResponse{Id: attestor.Id}, nil
}

// ActivateAttestor moves a PENDING attestor to ACTIVE. Authority only.
func (ms msgServer) ActivateAttestor(ctx context.Context, msg *types.MsgActivateAttestor) (*types.MsgActivateAttestorResponse, error) {
	if err := ms.checkAuthority(ms.authority, msg.Authority); err != nil {
		return nil, err
	}

	attestor, err := ms.GetAttestor(ctx, msg.Id)
	if err != nil {
		return nil, err
	}
	if attestor.Status != types.AttestorStatus_ATTESTOR_STATUS_PENDING {
		return nil, errorsmod.Wrapf(types.ErrInvalidAttestorState,
			"attestor %q is %s, only PENDING attestors can be activated", attestor.Id, attestor.Status)
	}

	attestor.Status = types.AttestorStatus_ATTESTOR_STATUS_ACTIVE
	if err := ms.Attestors.Set(ctx, attestor.Id, attestor); err != nil {
		return nil, err
	}

	emit(sdk.UnwrapSDKContext(ctx), "attestor_activated", attr("attestor_id", attestor.Id))
	return &types.MsgActivateAttestorResponse{}, nil
}

// maxSuspendReasonBytes bounds the free-text reason stored in the event.
const maxSuspendReasonBytes = 256

// SuspendAttestor bars an ACTIVE attestor from submitting attestations. Authority
// only. It is the governance lever for a dispute (or any other concern) that
// should not wait for a ruling; RaiseDispute never suspends on its own.
func (ms msgServer) SuspendAttestor(ctx context.Context, msg *types.MsgSuspendAttestor) (*types.MsgSuspendAttestorResponse, error) {
	if err := ms.checkAuthority(ms.authority, msg.Authority); err != nil {
		return nil, err
	}
	if err := validateText("reason", msg.Reason, maxSuspendReasonBytes, false); err != nil {
		return nil, err
	}

	attestor, err := ms.GetAttestor(ctx, msg.Id)
	if err != nil {
		return nil, err
	}
	if attestor.Status != types.AttestorStatus_ATTESTOR_STATUS_ACTIVE {
		return nil, errorsmod.Wrapf(types.ErrInvalidAttestorState,
			"attestor %q is %s, only ACTIVE attestors can be suspended", attestor.Id, attestor.Status)
	}

	attestor.Status = types.AttestorStatus_ATTESTOR_STATUS_SUSPENDED
	if err := ms.Attestors.Set(ctx, attestor.Id, attestor); err != nil {
		return nil, err
	}

	emit(sdk.UnwrapSDKContext(ctx), "attestor_suspended",
		attr("attestor_id", attestor.Id), attr("reason", msg.Reason))
	return &types.MsgSuspendAttestorResponse{}, nil
}

// ReinstateAttestor moves a SUSPENDED attestor back to ACTIVE. Authority only.
// Open disputes do not block it: lifting a suspension is a governance decision.
func (ms msgServer) ReinstateAttestor(ctx context.Context, msg *types.MsgReinstateAttestor) (*types.MsgReinstateAttestorResponse, error) {
	if err := ms.checkAuthority(ms.authority, msg.Authority); err != nil {
		return nil, err
	}

	attestor, err := ms.GetAttestor(ctx, msg.Id)
	if err != nil {
		return nil, err
	}
	if attestor.Status != types.AttestorStatus_ATTESTOR_STATUS_SUSPENDED {
		return nil, errorsmod.Wrapf(types.ErrInvalidAttestorState,
			"attestor %q is %s, only SUSPENDED attestors can be reinstated", attestor.Id, attestor.Status)
	}

	attestor.Status = types.AttestorStatus_ATTESTOR_STATUS_ACTIVE
	if err := ms.Attestors.Set(ctx, attestor.Id, attestor); err != nil {
		return nil, err
	}

	emit(sdk.UnwrapSDKContext(ctx), "attestor_reinstated", attr("attestor_id", attestor.Id))
	return &types.MsgReinstateAttestorResponse{}, nil
}

// UpdateAttestorSigners replaces the signer keys and threshold and bumps
// signer_set_version. Attestations already stored keep the version they were
// signed under.
//
// Authorisation is two-sided: the operator account must sign the transaction
// AND the CURRENT signer set must approve the rotation at its current
// threshold (current_set_signatures over the AttestorRotationSignDoc). Without
// the second condition the operator's single account key could replace the
// attestor's post-quantum signer keys in one message and sign attestations
// alone, which defeats the threshold. Same rule as MsgUpdateSidechainSigners.
// Rotation stays possible while SUSPENDED: the current-set approval is what
// stops an operator-alone takeover, and a suspended attestor with compromised
// keys must be able to rotate them out before it is reinstated. SLASHED and
// EXITED cannot rotate. Every approval carries not_after_height, so an
// approval gathered for a planned rotation expires instead of lingering.
func (ms msgServer) UpdateAttestorSigners(ctx context.Context, msg *types.MsgUpdateAttestorSigners) (*types.MsgUpdateAttestorSignersResponse, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)

	params, err := ms.Params.Get(ctx)
	if err != nil {
		return nil, err
	}

	attestor, err := ms.GetAttestor(ctx, msg.Id)
	if err != nil {
		return nil, err
	}
	if err := ms.checkOperator(attestor, msg.Operator); err != nil {
		return nil, err
	}

	switch attestor.Status {
	case types.AttestorStatus_ATTESTOR_STATUS_PENDING,
		types.AttestorStatus_ATTESTOR_STATUS_ACTIVE,
		types.AttestorStatus_ATTESTOR_STATUS_SUSPENDED:
	default:
		return nil, errorsmod.Wrapf(types.ErrInvalidAttestorState,
			"signers cannot be updated while the attestor is %s", attestor.Status)
	}

	if err := types.ValidateSignerSet(msg.SignerKeys, msg.SignatureThreshold, params); err != nil {
		return nil, err
	}
	if msg.NotAfterHeight == 0 {
		return nil, errorsmod.Wrap(types.ErrInvalidRequest, "not_after_height is required")
	}
	if height := uint64(sdkCtx.BlockHeight()); height > msg.NotAfterHeight {
		return nil, errorsmod.Wrapf(types.ErrRotationNotApproved,
			"approval expired: not_after_height %d, current height %d", msg.NotAfterHeight, height)
	}

	// The current signer set must approve its own replacement. verifySignatures
	// checks the count against the threshold before any verification, charges
	// signature_verification_gas per signature, and rejects any invalid,
	// duplicate or out-of-range signature.
	signBytes, err := types.AttestorRotationSignBytes(types.AttestorRotationSignDocFor(
		sdkCtx.ChainID(), attestor, msg.SignerKeys, msg.SignatureThreshold, msg.NotAfterHeight))
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidRequest, err.Error())
	}
	approvers, _, err := ms.verifySignatures(sdkCtx, attestor.SignerKeys, attestor.SignatureThreshold,
		params, signBytes, msg.CurrentSetSignatures)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrRotationNotApproved, err.Error())
	}

	attestor.SignerKeys = msg.SignerKeys
	attestor.SignatureThreshold = msg.SignatureThreshold
	attestor.SignerSetVersion++
	attestor.LastRotationApproverBitmap = approvers
	if err := ms.Attestors.Set(ctx, attestor.Id, attestor); err != nil {
		return nil, err
	}

	emit(sdkCtx, "attestor_signers_updated",
		attr("attestor_id", attestor.Id),
		attr("signer_set_version", uitoa(attestor.SignerSetVersion)),
		attr("approved_by_bitmap", hex.EncodeToString(approvers)))
	return &types.MsgUpdateAttestorSignersResponse{SignerSetVersion: attestor.SignerSetVersion}, nil
}

// InitiateAttestorExit deregisters an attestor. It can no longer attest, and
// the remaining bond is returned by ProcessExits after the cooldown, once no
// dispute against it is open.
//
// The cooldown is the larger of params.exit_cooldown_seconds and the longest
// dispute window among the attestor's authorized schemas, so every attestation
// it issued is out of its dispute window before the bond can leave.
func (ms msgServer) InitiateAttestorExit(ctx context.Context, msg *types.MsgInitiateAttestorExit) (*types.MsgInitiateAttestorExitResponse, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)

	params, err := ms.Params.Get(ctx)
	if err != nil {
		return nil, err
	}

	attestor, err := ms.GetAttestor(ctx, msg.Id)
	if err != nil {
		return nil, err
	}
	if err := ms.checkOperator(attestor, msg.Operator); err != nil {
		return nil, err
	}

	switch attestor.Status {
	case types.AttestorStatus_ATTESTOR_STATUS_PENDING,
		types.AttestorStatus_ATTESTOR_STATUS_ACTIVE,
		types.AttestorStatus_ATTESTOR_STATUS_SUSPENDED,
		types.AttestorStatus_ATTESTOR_STATUS_SLASHED:
	default:
		return nil, errorsmod.Wrapf(types.ErrInvalidAttestorState,
			"attestor %q is already %s", attestor.Id, attestor.Status)
	}

	cooldown := params.ExitCooldownSeconds
	for _, sid := range attestor.AuthorizedSchemaIds {
		schema, err := ms.Schemas.Get(ctx, sid)
		if err != nil {
			if errors.Is(err, collections.ErrNotFound) {
				continue
			}
			return nil, err
		}
		cooldown = max(cooldown, schema.DisputeWindowSeconds)
	}

	now := sdkCtx.BlockTime()
	returnAt := now.Add(time.Duration(cooldown) * time.Second)

	attestor.Status = types.AttestorStatus_ATTESTOR_STATUS_EXITED
	attestor.ExitRequestedAt = &now
	attestor.BondReturnAt = &returnAt
	if err := ms.Attestors.Set(ctx, attestor.Id, attestor); err != nil {
		return nil, err
	}
	if err := ms.ExitQueue.Set(ctx, exitKey(returnAt, attestor.Id)); err != nil {
		return nil, err
	}

	emit(sdkCtx, "attestor_exit_initiated",
		attr("attestor_id", attestor.Id), attr("bond_return_at", returnAt.UTC().Format(time.RFC3339)))
	return &types.MsgInitiateAttestorExitResponse{}, nil
}
