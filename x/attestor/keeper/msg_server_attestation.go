package keeper

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/lucidpay/lucidchain/x/attestor/types"
)

// SubmitAttestation publishes a signed claim about an existing checkpoint.
//
// Flow:
//  1. the schema is active and the attestor is ACTIVE, authorized for it, and
//     the submitter is its operator
//  2. the claim payload is within limits and valid JSON
//  3. the checkpoint exists in x/checkpoint (its hash is bound into the signatures)
//  4. the attestation id is unused
//  5. the threshold is met and every signature verifies over the sign bytes
//  6. the attestation is stored ACTIVE and the attestor's reputation goes up
//
// The same (schema, sidechain, lc_sequence, attestor) tuple can only ever be
// attested once, even after the attestation was revoked or overturned.
func (ms msgServer) SubmitAttestation(ctx context.Context, msg *types.MsgSubmitAttestation) (*types.MsgSubmitAttestationResponse, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)

	params, err := ms.Params.Get(ctx)
	if err != nil {
		return nil, err
	}

	schema, err := ms.GetSchema(ctx, msg.SchemaId)
	if err != nil {
		return nil, err
	}
	if !schema.Active {
		return nil, errorsmod.Wrapf(types.ErrSchemaInactive, "%q", schema.Id)
	}

	attestor, err := ms.GetAttestor(ctx, msg.AttestorId)
	if err != nil {
		return nil, err
	}
	if err := ms.checkOperator(attestor, msg.Submitter); err != nil {
		return nil, err
	}
	if attestor.Status != types.AttestorStatus_ATTESTOR_STATUS_ACTIVE {
		return nil, errorsmod.Wrapf(types.ErrAttestorNotActive,
			"attestor %q is %s", attestor.Id, attestor.Status)
	}
	if !slices.Contains(attestor.AuthorizedSchemaIds, schema.Id) {
		return nil, errorsmod.Wrapf(types.ErrSchemaNotAuthorized,
			"attestor %q is not authorized for schema %q", attestor.Id, schema.Id)
	}
	if msg.SignerSetVersion != attestor.SignerSetVersion {
		return nil, errorsmod.Wrapf(types.ErrSignerSetMismatch,
			"message uses version %d, attestor is at %d", msg.SignerSetVersion, attestor.SignerSetVersion)
	}

	if msg.SidechainId == "" || msg.CheckpointSequence == 0 {
		return nil, errorsmod.Wrap(types.ErrInvalidRequest, "sidechain_id and a checkpoint_sequence >= 1 are required")
	}
	if len(msg.ClaimPayload) == 0 {
		return nil, errorsmod.Wrap(types.ErrInvalidRequest, "claim_payload is required")
	}
	if uint64(len(msg.ClaimPayload)) > uint64(params.MaxClaimPayloadBytes) {
		return nil, errorsmod.Wrapf(types.ErrLimitExceeded,
			"claim_payload is %d bytes, max %d", len(msg.ClaimPayload), params.MaxClaimPayloadBytes)
	}
	if !json.Valid(msg.ClaimPayload) {
		return nil, errorsmod.Wrap(types.ErrInvalidRequest, "claim_payload must be valid JSON")
	}

	cp, err := ms.checkpointKeeper.GetCheckpoint(ctx, msg.SidechainId, msg.CheckpointSequence)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, errorsmod.Wrapf(types.ErrCheckpointNotFound,
				"sidechain %q lc_sequence %d", msg.SidechainId, msg.CheckpointSequence)
		}
		return nil, err
	}

	id := types.AttestationID(schema.Id, msg.SidechainId, msg.CheckpointSequence, attestor.Id)
	exists, err := ms.Attestations.Has(ctx, id)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, errorsmod.Wrapf(types.ErrAttestationExists, "%s", id)
	}

	signBytes, err := types.AttestationSignBytes(&types.AttestationSignDoc{
		ChainId:            sdkCtx.ChainID(),
		SchemaId:           schema.Id,
		AttestorId:         attestor.Id,
		SidechainId:        msg.SidechainId,
		CheckpointSequence: msg.CheckpointSequence,
		CheckpointHash:     cp.CheckpointHash,
		ClaimPayload:       msg.ClaimPayload,
		SignerSetVersion:   attestor.SignerSetVersion,
	})
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidRequest, err.Error())
	}

	bitmap, digest, err := ms.verifySignatures(
		sdkCtx, attestor.SignerKeys, attestor.SignatureThreshold, params, signBytes, msg.Signatures)
	if err != nil {
		return nil, err
	}

	now := sdkCtx.BlockTime()
	attestation := types.Attestation{
		Id:                 id,
		SchemaId:           schema.Id,
		AttestorId:         attestor.Id,
		SidechainId:        msg.SidechainId,
		CheckpointSequence: msg.CheckpointSequence,
		ClaimPayload:       msg.ClaimPayload,
		Status:             types.AttestationStatus_ATTESTATION_STATUS_ACTIVE,
		IssuedHeight:       uint64(sdkCtx.BlockHeight()),
		IssuedAt:           now,
		ExpiresAt:          now.Add(time.Duration(schema.ValidityPeriodSeconds) * time.Second),
		CheckpointHash:     cp.CheckpointHash,
		SignerSetVersion:   attestor.SignerSetVersion,
		SignerBitmap:       bitmap,
		SignaturesDigest:   digest,
	}
	if err := ms.setAttestation(ctx, attestation); err != nil {
		return nil, err
	}

	attestor.ReputationScore++
	if err := ms.Attestors.Set(ctx, attestor.Id, attestor); err != nil {
		return nil, err
	}

	emit(sdkCtx, "attestation_submitted",
		attr("attestation_id", id),
		attr("schema_id", schema.Id),
		attr("attestor_id", attestor.Id),
		attr("sidechain_id", msg.SidechainId),
		attr("checkpoint_sequence", uitoa(msg.CheckpointSequence)),
		attr("checkpoint_hash", hex.EncodeToString(cp.CheckpointHash)),
	)
	return &types.MsgSubmitAttestationResponse{AttestationId: id}, nil
}

// RevokeAttestation lets the attestor withdraw one of its own ACTIVE
// attestations. Disputed attestations cannot be revoked, so an attestor cannot
// dodge a pending dispute.
func (ms msgServer) RevokeAttestation(ctx context.Context, msg *types.MsgRevokeAttestation) (*types.MsgRevokeAttestationResponse, error) {
	attestation, err := ms.GetAttestation(ctx, msg.AttestationId)
	if err != nil {
		return nil, err
	}
	attestor, err := ms.GetAttestor(ctx, attestation.AttestorId)
	if err != nil {
		return nil, err
	}
	if err := ms.checkOperator(attestor, msg.Operator); err != nil {
		return nil, err
	}
	if attestation.Status != types.AttestationStatus_ATTESTATION_STATUS_ACTIVE {
		return nil, errorsmod.Wrapf(types.ErrInvalidAttestationState,
			"attestation %s is %s, only ACTIVE attestations can be revoked", attestation.Id, attestation.Status)
	}

	attestation.Status = types.AttestationStatus_ATTESTATION_STATUS_REVOKED
	if err := ms.Attestations.Set(ctx, attestation.Id, attestation); err != nil {
		return nil, err
	}

	emit(sdk.UnwrapSDKContext(ctx), "attestation_revoked", attr("attestation_id", attestation.Id))
	return &types.MsgRevokeAttestationResponse{}, nil
}
