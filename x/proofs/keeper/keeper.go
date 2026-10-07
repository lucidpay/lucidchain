package keeper

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"strconv"

	"cosmossdk.io/collections"
	"cosmossdk.io/core/address"
	corestore "cosmossdk.io/core/store"
	errorsmod "cosmossdk.io/errors"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	checkpointtypes "github.com/lucidpay/lucidchain/x/checkpoint/types"
	"github.com/lucidpay/lucidchain/x/proofs/types"
)

// Keeper owns the x/proofs state: module params, the registered verifiers and
// the proof records. Cryptographic verification is delegated to the
// types.ProofVerifier implementations it was constructed with (see the
// verifiers package), so the keeper itself has no dependency on a proof library.
type Keeper struct {
	storeService corestore.KVStoreService
	cdc          codec.Codec
	addressCodec address.Codec
	// authority is the address allowed to update params and manage verifiers
	// Address capable of executing a MsgUpdateParams message.
	// Typically, this should be the x/gov module account.
	authority []byte

	Schema collections.Schema
	Params collections.Item[types.Params]

	checkpointKeeper types.CheckpointKeeper
	bankKeeper       types.BankKeeper

	// impls are the verifier implementations compiled into the binary, keyed
	// by proof_system_id.
	impls map[string]types.ProofVerifier

	// Verifiers maps proof_system_id -> registration.
	Verifiers collections.Map[string, types.VerifierRegistration]

	// Proofs maps proof id -> verified proof record.
	Proofs collections.Map[string, types.ProofRecord]

	// ProofsByCheckpoint maps ((sidechain_id, lc_sequence), proof_id) -> proof_id.
	// It exists so a checkpoint's proofs can be listed and paginated.
	ProofsByCheckpoint collections.Map[collections.Pair[collections.Pair[string, uint64], string], string]
}

func NewKeeper(
	storeService corestore.KVStoreService,
	cdc codec.Codec,
	addressCodec address.Codec,
	authority []byte,
	checkpointKeeper types.CheckpointKeeper,
	bankKeeper types.BankKeeper,
	impls map[string]types.ProofVerifier,
) Keeper {
	if _, err := addressCodec.BytesToString(authority); err != nil {
		panic(fmt.Sprintf("invalid authority address %s: %s", authority, err))
	}

	sb := collections.NewSchemaBuilder(storeService)

	k := Keeper{
		storeService:     storeService,
		cdc:              cdc,
		addressCodec:     addressCodec,
		authority:        authority,
		checkpointKeeper: checkpointKeeper,
		bankKeeper:       bankKeeper,
		impls:            maps.Clone(impls),
		Params:           collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.Params](cdc)),
		Verifiers: collections.NewMap(
			sb, types.VerifierKey, "verifiers",
			collections.StringKey,
			codec.CollValue[types.VerifierRegistration](cdc),
		),
		Proofs: collections.NewMap(
			sb, types.ProofKey, "proofs",
			collections.StringKey,
			codec.CollValue[types.ProofRecord](cdc),
		),
		ProofsByCheckpoint: collections.NewMap(
			sb, types.ProofByCheckpointKey, "proofs_by_checkpoint",
			collections.PairKeyCodec(
				collections.PairKeyCodec(collections.StringKey, collections.Uint64Key),
				collections.StringKey,
			),
			collections.StringValue,
		),
	}

	schema, err := sb.Build()
	if err != nil {
		panic(err)
	}
	k.Schema = schema

	return k
}

// GetAuthority returns the module's authority.
func (k Keeper) GetAuthority() []byte {
	return k.authority
}

// AddressCodec returns the address codec used by the module.
func (k Keeper) AddressCodec() address.Codec {
	return k.addressCodec
}

// HasImplementation reports whether a verifier implementation for the proof
// system is compiled into this binary.
func (k Keeper) HasImplementation(proofSystemID string) bool {
	_, ok := k.impls[proofSystemID]
	return ok
}

// ---------------------------------------------------------------------------
// params
// ---------------------------------------------------------------------------

// GetParams returns the module params.
func (k Keeper) GetParams(ctx context.Context) (types.Params, error) {
	return k.Params.Get(ctx)
}

// SetParams validates and stores the module params.
func (k Keeper) SetParams(ctx context.Context, p types.Params) error {
	if err := p.Validate(); err != nil {
		return errorsmod.Wrap(sdkerrors.ErrInvalidRequest, err.Error())
	}
	return k.Params.Set(ctx, p)
}

// ---------------------------------------------------------------------------
// typed getters and state helpers
// ---------------------------------------------------------------------------

func getOrWrap[V any](ctx context.Context, m collections.Map[string, V], id string, notFound *errorsmod.Error) (V, error) {
	v, err := m.Get(ctx, id)
	if err != nil {
		var zero V
		if errors.Is(err, collections.ErrNotFound) {
			return zero, errorsmod.Wrapf(notFound, "%q", id)
		}
		return zero, err
	}
	return v, nil
}

// GetVerifier returns a registration or types.ErrVerifierNotFound.
func (k Keeper) GetVerifier(ctx context.Context, proofSystemID string) (types.VerifierRegistration, error) {
	return getOrWrap(ctx, k.Verifiers, proofSystemID, types.ErrVerifierNotFound)
}

// GetProof returns a proof record or types.ErrProofNotFound.
func (k Keeper) GetProof(ctx context.Context, id string) (types.ProofRecord, error) {
	return getOrWrap(ctx, k.Proofs, id, types.ErrProofNotFound)
}

// setProof writes a proof record and its by-checkpoint index entry.
func (k Keeper) setProof(ctx context.Context, p types.ProofRecord) error {
	if err := k.Proofs.Set(ctx, p.Id, p); err != nil {
		return err
	}
	idx := collections.Join(collections.Join(p.SidechainId, p.CheckpointSequence), p.Id)
	return k.ProofsByCheckpoint.Set(ctx, idx, p.Id)
}

// checkpointBinding loads the checkpoint and derives the binding a proof of
// claimID about it must carry in its first two public inputs. The binding
// commits to the chain id, so it differs between chains.
func (k Keeper) checkpointBinding(
	ctx context.Context,
	sidechainID string,
	lc_sequence uint64,
	claimID string,
) (types.Binding, checkpointtypes.Checkpoint, error) {
	cp, err := k.checkpointKeeper.GetCheckpoint(ctx, sidechainID, lc_sequence)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return types.Binding{}, checkpointtypes.Checkpoint{}, errorsmod.Wrapf(types.ErrCheckpointNotFound,
				"sidechain %q lc_sequence %d", sidechainID, lc_sequence)
		}
		return types.Binding{}, checkpointtypes.Checkpoint{}, err
	}
	return types.ComputeBinding(sdkChainID(ctx), sidechainID, lc_sequence, cp.StateRoot, cp.CheckpointHash, claimID), cp, nil
}

// sdkChainID returns the chain id of the context's header.
func sdkChainID(ctx context.Context) string {
	return sdk.UnwrapSDKContext(ctx).ChainID()
}

// checkAuthority verifies that actual (a bech32 string) is the expected address.
func (k Keeper) checkAuthority(expected []byte, actual string) error {
	got, err := k.addressCodec.StringToBytes(actual)
	if err != nil {
		return errorsmod.Wrap(sdkerrors.ErrInvalidAddress, "invalid authority address")
	}
	if !bytes.Equal(expected, got) {
		want, _ := k.addressCodec.BytesToString(expected)
		return errorsmod.Wrapf(types.ErrUnauthorized, "invalid authority; expected %s, got %s", want, actual)
	}
	return nil
}

// safely runs a verifier call and turns a panic into an error, so a
// malformed proof can never crash block execution. It must only wrap verifier
// code: it would also swallow an out-of-gas panic.
func safely(op string, fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%s panicked: %v", op, r)
		}
	}()
	return fn()
}

// chargeFee collects params.base_verification_fee from the submitter and
// sends it to the fee collector.
func (k Keeper) chargeFee(ctx context.Context, submitter sdk.AccAddress, p types.Params) error {
	if p.BaseVerificationFee.IsZero() {
		return nil
	}
	fee := sdk.NewCoins(sdk.NewCoin(p.FeeDenom, p.BaseVerificationFee))
	if err := k.bankKeeper.SendCoinsFromAccountToModule(ctx, submitter, authtypes.FeeCollectorName, fee); err != nil {
		return errorsmod.Wrap(err, "failed to pay verification fee")
	}
	return nil
}

// ---------------------------------------------------------------------------
// SubmitProof
// ---------------------------------------------------------------------------

// SubmitProof verifies a validity proof about an existing checkpoint and, if
// it is valid, stores a ProofRecord.
//
// Flow (cheap checks first, so bad input costs little):
//  1. size and format limits from params
//  2. the verifier is registered, ACTIVE, and has a compiled-in implementation
//  3. the (checkpoint, system, claim) tuple has no record yet
//  4. the checkpoint exists, and the public inputs carry its binding
//  5. the verification fee is collected and the registration's verification
//     gas is charged
//  6. the proof is verified
//  7. the record and its index entry are stored
//
// The handler runs in a cached context, so any error reverts all state writes.
// The gas of step 5 is spent whether or not the proof turns out to be valid.
func (k Keeper) SubmitProof(ctx context.Context, msg *types.MsgSubmitProof) (*types.MsgSubmitProofResponse, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)

	submitter, err := k.addressCodec.StringToBytes(msg.Submitter)
	if err != nil {
		return nil, errorsmod.Wrap(sdkerrors.ErrInvalidAddress, "invalid submitter address")
	}

	params, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateSubmission(msg, params); err != nil {
		return nil, err
	}

	reg, err := k.GetVerifier(ctx, msg.ProofSystemId)
	if err != nil {
		return nil, err
	}
	if reg.Status != types.VerifierStatus_VERIFIER_STATUS_ACTIVE {
		return nil, errorsmod.Wrapf(types.ErrVerifierDeprecated, "%q", reg.ProofSystemId)
	}
	impl, ok := k.impls[reg.ProofSystemId]
	if !ok {
		return nil, errorsmod.Wrapf(types.ErrVerifierUnavailable, "%q", reg.ProofSystemId)
	}

	id := types.ProofID(msg.SidechainId, msg.CheckpointSequence, msg.ProofSystemId, msg.ClaimId)
	exists, err := k.Proofs.Has(ctx, id)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, errorsmod.Wrapf(types.ErrProofExists, "%s", id)
	}

	binding, cp, err := k.checkpointBinding(ctx, msg.SidechainId, msg.CheckpointSequence, msg.ClaimId)
	if err != nil {
		return nil, err
	}
	if err := safely("binding check", func() error {
		return impl.CheckBinding(msg.PublicInputs, binding)
	}); err != nil {
		return nil, errorsmod.Wrap(types.ErrBindingMismatch, err.Error())
	}

	if err := k.chargeFee(ctx, submitter, params); err != nil {
		return nil, err
	}
	sdkCtx.GasMeter().ConsumeGas(reg.VerificationGas, "proof verification")

	if err := safely("verification", func() error {
		return impl.Verify(reg.VerificationKey, msg.PublicInputs, msg.Proof)
	}); err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidProof, err.Error())
	}

	record := types.ProofRecord{
		Id:                 id,
		SidechainId:        msg.SidechainId,
		CheckpointSequence: msg.CheckpointSequence,
		ProofSystemId:      msg.ProofSystemId,
		VerifierVersion:    reg.Version,
		ClaimId:            msg.ClaimId,
		PublicInputs:       msg.PublicInputs,
		Proof:              msg.Proof,
		Verified:           true,
		VerifiedHeight:     uint64(sdkCtx.BlockHeight()),
		VerifiedAt:         sdkCtx.BlockTime(),
		CheckpointHash:     cp.CheckpointHash,
		Binding:            binding[:],
		Submitter:          msg.Submitter,
	}
	if err := k.setProof(ctx, record); err != nil {
		return nil, err
	}

	sdkCtx.EventManager().EmitEvent(sdk.NewEvent(
		"proof_verified",
		sdk.NewAttribute("proof_id", id),
		sdk.NewAttribute("sidechain_id", msg.SidechainId),
		sdk.NewAttribute("checkpoint_sequence", strconv.FormatUint(msg.CheckpointSequence, 10)),
		sdk.NewAttribute("proof_system_id", msg.ProofSystemId),
		sdk.NewAttribute("claim_id", msg.ClaimId),
		sdk.NewAttribute("verifier_version", strconv.FormatUint(uint64(reg.Version), 10)),
		sdk.NewAttribute("binding", hex.EncodeToString(binding[:])),
	))

	return &types.MsgSubmitProofResponse{ProofRecordId: id}, nil
}

// validateSubmission applies the stateless format and size limits.
func validateSubmission(msg *types.MsgSubmitProof, p types.Params) error {
	if msg.SidechainId == "" {
		return errorsmod.Wrap(types.ErrInvalidRequest, "sidechain_id is required")
	}
	if msg.CheckpointSequence == 0 {
		return errorsmod.Wrap(types.ErrInvalidRequest, "checkpoint_sequence must be >= 1")
	}
	if err := types.ValidateID("proof_system_id", msg.ProofSystemId); err != nil {
		return err
	}
	if err := types.ValidateID("claim_id", msg.ClaimId); err != nil {
		return err
	}
	if len(msg.PublicInputs) == 0 {
		return errorsmod.Wrap(types.ErrInvalidRequest, "public_inputs is required")
	}
	if uint64(len(msg.PublicInputs)) > uint64(p.MaxPublicInputBytes) {
		return errorsmod.Wrapf(types.ErrLimitExceeded,
			"public_inputs is %d bytes, max %d", len(msg.PublicInputs), p.MaxPublicInputBytes)
	}
	if len(msg.Proof) == 0 {
		return errorsmod.Wrap(types.ErrInvalidRequest, "proof is required")
	}
	if uint64(len(msg.Proof)) > uint64(p.MaxProofBytes) {
		return errorsmod.Wrapf(types.ErrLimitExceeded,
			"proof is %d bytes, max %d", len(msg.Proof), p.MaxProofBytes)
	}
	return nil
}

// ---------------------------------------------------------------------------
// verifier management (authority only)
// ---------------------------------------------------------------------------

// RegisterVerifier registers a new verifier, or rotates the key of an ACTIVE
// one. Rotating to a different key bumps the version; resubmitting the same
// key only updates the description and gas. DEPRECATED verifiers cannot be
// re-registered: use a new proof_system_id.
//
// Existing proof records keep the version they were verified under.
func (k Keeper) RegisterVerifier(ctx context.Context, msg *types.MsgRegisterVerifier) (*types.MsgRegisterVerifierResponse, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)

	if err := k.checkAuthority(k.authority, msg.Authority); err != nil {
		return nil, err
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}

	if err := types.ValidateID("proof_system_id", msg.ProofSystemId); err != nil {
		return nil, err
	}
	if msg.Description == "" || len(msg.Description) > types.MaxDescriptionBytes {
		return nil, errorsmod.Wrapf(types.ErrInvalidRequest,
			"description is required and at most %d bytes", types.MaxDescriptionBytes)
	}
	if msg.VerificationGas == 0 {
		return nil, errorsmod.Wrap(types.ErrInvalidRequest, "verification_gas must be positive")
	}
	if len(msg.VerificationKey) == 0 {
		return nil, errorsmod.Wrap(types.ErrInvalidVerifierKey, "verification_key is required")
	}
	if uint64(len(msg.VerificationKey)) > uint64(params.MaxVerificationKeyBytes) {
		return nil, errorsmod.Wrapf(types.ErrLimitExceeded,
			"verification_key is %d bytes, max %d", len(msg.VerificationKey), params.MaxVerificationKeyBytes)
	}

	impl, ok := k.impls[msg.ProofSystemId]
	if !ok {
		return nil, errorsmod.Wrapf(types.ErrVerifierUnavailable, "%q", msg.ProofSystemId)
	}
	if err := safely("key validation", func() error { return impl.ValidateKey(msg.VerificationKey) }); err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidVerifierKey, err.Error())
	}

	now := sdkCtx.BlockTime()
	reg, err := k.Verifiers.Get(ctx, msg.ProofSystemId)
	switch {
	case errors.Is(err, collections.ErrNotFound):
		reg = types.VerifierRegistration{
			ProofSystemId:   msg.ProofSystemId,
			Description:     msg.Description,
			VerificationKey: msg.VerificationKey,
			Status:          types.VerifierStatus_VERIFIER_STATUS_ACTIVE,
			Version:         1,
			RegisteredAt:    now,
			VerificationGas: msg.VerificationGas,
		}
	case err != nil:
		return nil, err
	case reg.Status != types.VerifierStatus_VERIFIER_STATUS_ACTIVE:
		return nil, errorsmod.Wrapf(types.ErrVerifierDeprecated,
			"%q is deprecated; register a new proof_system_id instead", reg.ProofSystemId)
	default:
		if !bytes.Equal(reg.VerificationKey, msg.VerificationKey) {
			reg.VerificationKey = msg.VerificationKey
			reg.Version++
		}
		reg.Description = msg.Description
		reg.VerificationGas = msg.VerificationGas
		reg.UpdatedAt = &now
	}

	if err := k.Verifiers.Set(ctx, reg.ProofSystemId, reg); err != nil {
		return nil, err
	}

	sdkCtx.EventManager().EmitEvent(sdk.NewEvent(
		"verifier_registered",
		sdk.NewAttribute("proof_system_id", reg.ProofSystemId),
		sdk.NewAttribute("version", strconv.FormatUint(uint64(reg.Version), 10)),
	))
	return &types.MsgRegisterVerifierResponse{Version: reg.Version}, nil
}

// DeprecateVerifier stops a verifier from accepting new proofs. Existing
// records stay queryable.
func (k Keeper) DeprecateVerifier(ctx context.Context, msg *types.MsgDeprecateVerifier) (*types.MsgDeprecateVerifierResponse, error) {
	if err := k.checkAuthority(k.authority, msg.Authority); err != nil {
		return nil, err
	}

	reg, err := k.GetVerifier(ctx, msg.ProofSystemId)
	if err != nil {
		return nil, err
	}
	if reg.Status != types.VerifierStatus_VERIFIER_STATUS_ACTIVE {
		return nil, errorsmod.Wrapf(types.ErrVerifierDeprecated, "%q is already deprecated", reg.ProofSystemId)
	}

	now := sdk.UnwrapSDKContext(ctx).BlockTime()
	reg.Status = types.VerifierStatus_VERIFIER_STATUS_DEPRECATED
	reg.UpdatedAt = &now
	if err := k.Verifiers.Set(ctx, reg.ProofSystemId, reg); err != nil {
		return nil, err
	}

	sdk.UnwrapSDKContext(ctx).EventManager().EmitEvent(sdk.NewEvent(
		"verifier_deprecated",
		sdk.NewAttribute("proof_system_id", reg.ProofSystemId),
	))
	return &types.MsgDeprecateVerifierResponse{}, nil
}
