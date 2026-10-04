package keeper

import (
	"context"
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

// UpdateAttestorSigners replaces the signer keys and threshold and bumps
// signer_set_version. Existing attestations keep the version they were signed under.
func (ms msgServer) UpdateAttestorSigners(ctx context.Context, msg *types.MsgUpdateAttestorSigners) (*types.MsgUpdateAttestorSignersResponse, error) {
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

	attestor.SignerKeys = msg.SignerKeys
	attestor.SignatureThreshold = msg.SignatureThreshold
	attestor.SignerSetVersion++
	if err := ms.Attestors.Set(ctx, attestor.Id, attestor); err != nil {
		return nil, err
	}

	emit(sdk.UnwrapSDKContext(ctx), "attestor_signers_updated",
		attr("attestor_id", attestor.Id), attr("signer_set_version", uitoa(attestor.SignerSetVersion)))
	return &types.MsgUpdateAttestorSignersResponse{}, nil
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
