package keeper

import (
	"bytes"
	"cmp"

	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"cosmossdk.io/collections"
	"cosmossdk.io/core/address"
	corestore "cosmossdk.io/core/store"

	"cosmossdk.io/math"
	sidechaintypes "github.com/lucidpay/lucidchain/x/sidechain/types"

	errorsmod "cosmossdk.io/errors"
	"github.com/cosmos/cosmos-sdk/codec"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/lucidpay/lucidchain/x/checkpoint/types"

	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
)

const (
	eventTypeCheckpointSubmitted = "checkpoint_submitted"
	attributeSidechainID         = "sidechain_id"
	attributeSequence            = "sequence"
	attributeStateRoot           = "state_root"
	attributeCheckpointHash      = "checkpoint_hash"
)

type Keeper struct {
	storeService corestore.KVStoreService
	cdc          codec.Codec
	addressCodec address.Codec
	// Address capable of executing a MsgUpdateParams message.
	// Typically, this should be the x/gov module account.
	authority []byte

	Schema collections.Schema
	// Params holds module-wide parameters.
	Params collections.Item[types.Params]

	sidechainKeeper types.SidechainKeeper
	bankKeeper      types.BankKeeper

	// Checkpoints maps (sidechain_id, sequence) -> checkpoint. Append-only.
	Checkpoints collections.Map[collections.Pair[string, uint64], types.Checkpoint]

	// LatestSequence maps sidechain_id -> sequence of its newest checkpoint.
	LatestSequence collections.Map[string, uint64]
}

func NewKeeper(
	storeService corestore.KVStoreService,
	cdc codec.Codec,
	addressCodec address.Codec,
	authority []byte,
	sidechainKeeper types.SidechainKeeper,
	bankKeeper types.BankKeeper,
) Keeper {
	if _, err := addressCodec.BytesToString(authority); err != nil {
		panic(fmt.Sprintf("invalid authority address %s: %s", authority, err))
	}

	sb := collections.NewSchemaBuilder(storeService)

	k := Keeper{
		storeService:    storeService,
		cdc:             cdc,
		addressCodec:    addressCodec,
		authority:       authority,
		sidechainKeeper: sidechainKeeper,
		bankKeeper:      bankKeeper,
		Params:          collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.Params](cdc)),
		Checkpoints: collections.NewMap(
			sb,
			types.CheckpointKey,
			"checkpoints",
			collections.PairKeyCodec(collections.StringKey, collections.Uint64Key),
			codec.CollValue[types.Checkpoint](cdc),
		),
		LatestSequence: collections.NewMap(
			sb,
			types.LatestSequenceKey,
			"latest_sequence",
			collections.StringKey,
			collections.Uint64Value,
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

// GetParams returns the module params.
func (k Keeper) GetParams(ctx context.Context) (types.Params, error) {
	return k.Params.Get(ctx)
}

// SetParams validates and stores the module params.
func (k Keeper) SetParams(ctx context.Context, params types.Params) error {
	if err := params.Validate(); err != nil {
		return errorsmod.Wrap(sdkerrors.ErrInvalidRequest, err.Error())
	}
	return k.Params.Set(ctx, params)
}

// GetCheckpoint returns the checkpoint at (sidechainID, sequence).
func (k Keeper) GetCheckpoint(ctx context.Context, sidechainID string, sequence uint64) (types.Checkpoint, error) {
	return k.Checkpoints.Get(ctx, collections.Join(sidechainID, sequence))
}

// GetLatestCheckpoint returns the newest checkpoint of a sidechain.
func (k Keeper) GetLatestCheckpoint(ctx context.Context, sidechainID string) (types.Checkpoint, error) {
	seq, err := k.LatestSequence.Get(ctx, sidechainID)
	if err != nil {
		return types.Checkpoint{}, err
	}
	return k.GetCheckpoint(ctx, sidechainID, seq)
}

// storeCheckpoint writes the checkpoint and advances the latest-sequence index.
func (k Keeper) storeCheckpoint(ctx context.Context, cp types.Checkpoint) error {
	if err := k.Checkpoints.Set(ctx, collections.Join(cp.SidechainId, cp.Sequence), cp); err != nil {
		return err
	}
	return k.LatestSequence.Set(ctx, cp.SidechainId, cp.Sequence)
}

// SubmitCheckpoint validates, verifies, charges for, and stores a checkpoint.
//
// Flow:
//  1. stateless limits from params
//  2. sidechain exists, is ACTIVE, signer_set_version matches
//  3. sequence == latest + 1 and previous_checkpoint_hash links to the latest
//  4. build sign bytes, check threshold, verify every signature
//  5. collect the fee, store the checkpoint, update x/sidechain
//
// The handler runs in a cached context, so any error reverts all state writes.
func (k Keeper) SubmitCheckpoint(ctx context.Context, msg *types.MsgSubmitCheckpoint) (*types.MsgSubmitCheckpointResponse, error) {
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

	sc, err := k.sidechainKeeper.GetSidechain(ctx, msg.SidechainId)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, errorsmod.Wrapf(types.ErrSidechainNotFound, "%q", msg.SidechainId)
		}
		return nil, err
	}
	if sc.Status != sidechaintypes.SidechainStatus_SIDECHAIN_STATUS_ACTIVE {
		return nil, errorsmod.Wrapf(types.ErrSidechainNotActive,
			"sidechain %q has status %s", msg.SidechainId, sc.Status)
	}
	if msg.SignerSetVersion != sc.SignerSetVersion {
		return nil, errorsmod.Wrapf(types.ErrSignerSetMismatch,
			"message uses version %d, sidechain is at %d", msg.SignerSetVersion, sc.SignerSetVersion)
	}

	if err := k.checkChaining(ctx, msg); err != nil {
		return nil, err
	}

	signBytes, err := types.CheckpointSignBytes(&types.CheckpointSignDoc{
		ChainId:                sdkCtx.ChainID(),
		SidechainId:            msg.SidechainId,
		Sequence:               msg.Sequence,
		StateRoot:              msg.StateRoot,
		PreviousCheckpointHash: msg.PreviousCheckpointHash,
		RecordCount:            msg.RecordCount,
		SignerSetVersion:       msg.SignerSetVersion,
		DataPointer:            msg.DataPointer,
	})
	if err != nil {
		return nil, errorsmod.Wrap(sdkerrors.ErrInvalidRequest, err.Error())
	}
	checkpointHash := types.CheckpointHash(signBytes)

	bitmap, digest, err := k.verifySignatures(sdkCtx, sc, params, signBytes, msg.Signatures)
	if err != nil {
		return nil, err
	}

	if err := k.chargeFee(ctx, submitter, params, msg.RecordCount); err != nil {
		return nil, err
	}

	height := sdkCtx.BlockHeight()
	cp := types.Checkpoint{
		SidechainId:            msg.SidechainId,
		Sequence:               msg.Sequence,
		StateRoot:              msg.StateRoot,
		PreviousCheckpointHash: msg.PreviousCheckpointHash,
		RecordCount:            msg.RecordCount,
		CheckpointHash:         checkpointHash,
		SignerSetVersion:       msg.SignerSetVersion,
		SignerBitmap:           bitmap,
		SignaturesDigest:       digest,
		FinalizedHeight:        height,
		FinalizedAt:            sdkCtx.BlockTime(),
		DataPointer:            msg.DataPointer,
	}
	if err := k.storeCheckpoint(ctx, cp); err != nil {
		return nil, err
	}

	if err := k.sidechainKeeper.RecordCheckpoint(ctx, msg.SidechainId, msg.Sequence, height, checkpointHash); err != nil {
		return nil, err
	}

	sdkCtx.EventManager().EmitEvent(sdk.NewEvent(
		eventTypeCheckpointSubmitted,
		sdk.NewAttribute(attributeSidechainID, msg.SidechainId),
		sdk.NewAttribute(attributeSequence, strconv.FormatUint(msg.Sequence, 10)),
		sdk.NewAttribute(attributeStateRoot, hex.EncodeToString(msg.StateRoot)),
		sdk.NewAttribute(attributeCheckpointHash, hex.EncodeToString(checkpointHash)),
	))

	return &types.MsgSubmitCheckpointResponse{
		FinalizedHeight: uint64(height),
		CheckpointHash:  checkpointHash,
	}, nil
}

// validateSubmission applies the stateless limits from params.
func validateSubmission(msg *types.MsgSubmitCheckpoint, p types.Params) error {
	switch {
	case msg.SidechainId == "":
		return errorsmod.Wrap(types.ErrInvalidCheckpoint, "sidechain_id is required")
	case msg.Sequence == 0:
		return errorsmod.Wrap(types.ErrInvalidSequence, "sequence must be >= 1")
	case len(msg.StateRoot) == 0:
		return errorsmod.Wrap(types.ErrInvalidCheckpoint, "state_root is required")
	case len(msg.StateRoot) > types.MaxStateRootBytes:
		return errorsmod.Wrapf(types.ErrLimitExceeded,
			"state_root is %d bytes, max %d", len(msg.StateRoot), types.MaxStateRootBytes)
	case msg.RecordCount > p.MaxRecordsPerCheckpoint:
		return errorsmod.Wrapf(types.ErrLimitExceeded,
			"record_count %d exceeds max %d", msg.RecordCount, p.MaxRecordsPerCheckpoint)
	case uint64(len(msg.DataPointer)) > uint64(p.MaxDataPointerBytes):
		return errorsmod.Wrapf(types.ErrLimitExceeded,
			"data_pointer is %d bytes, max %d", len(msg.DataPointer), p.MaxDataPointerBytes)
	case len(msg.Signatures) == 0:
		return errorsmod.Wrap(types.ErrThresholdNotMet, "at least one signature is required")
	case uint64(len(msg.Signatures)) > uint64(p.MaxSignaturesPerCheckpoint):
		return errorsmod.Wrapf(types.ErrLimitExceeded,
			"%d signatures, max %d", len(msg.Signatures), p.MaxSignaturesPerCheckpoint)
	}

	var total uint64
	for _, s := range msg.Signatures {
		total += uint64(len(s.Signature))
	}
	if total > uint64(p.MaxSignatureBytesPerCheckpoint) {
		return errorsmod.Wrapf(types.ErrLimitExceeded,
			"%d total signature bytes, max %d", total, p.MaxSignatureBytesPerCheckpoint)
	}
	return nil
}

// checkChaining enforces sequence == latest+1 and the previous-hash link.
func (k Keeper) checkChaining(ctx context.Context, msg *types.MsgSubmitCheckpoint) error {
	lastSeq, err := k.LatestSequence.Get(ctx, msg.SidechainId)
	if err != nil {
		if !errors.Is(err, collections.ErrNotFound) {
			return err
		}
		lastSeq = 0 // first checkpoint for this sidechain
	}

	if msg.Sequence != lastSeq+1 {
		return errorsmod.Wrapf(types.ErrInvalidSequence,
			"expected sequence %d, got %d", lastSeq+1, msg.Sequence)
	}

	if lastSeq == 0 {
		if len(msg.PreviousCheckpointHash) != 0 {
			return errorsmod.Wrap(types.ErrInvalidPreviousHash,
				"previous_checkpoint_hash must be empty for sequence 1")
		}
		return nil
	}

	prev, err := k.GetCheckpoint(ctx, msg.SidechainId, lastSeq)
	if err != nil {
		return err
	}
	if !bytes.Equal(prev.CheckpointHash, msg.PreviousCheckpointHash) {
		return errorsmod.Wrapf(types.ErrInvalidPreviousHash,
			"does not match checkpoint hash of sequence %d", lastSeq)
	}
	return nil
}

// verifySignatures verifies every submitted signature against the sidechain's
// registered signer keys and returns the signer bitmap and signatures digest.
//
// Bitmap layout: bit i lives in byte i/8 at position i%8 (least significant
// bit first). Digest: SHA-256 over the verified signatures ordered by
// signer_index. Any invalid, duplicate, or out-of-range signature rejects the
// whole message, so a submitter cannot pad a message with junk.
func (k Keeper) verifySignatures(
	ctx sdk.Context,
	sc sidechaintypes.Sidechain,
	params types.Params,
	signBytes []byte,
	sigs []types.Signature,
) (bitmap, digest []byte, err error) {
	threshold := max(sc.SignatureThreshold, 1)
	if uint32(len(sigs)) < threshold {
		return nil, nil, errorsmod.Wrapf(types.ErrThresholdNotMet,
			"%d signatures supplied, threshold is %d", len(sigs), threshold)
	}

	n := len(sc.SignerKeys)
	bitmap = make([]byte, (n+7)/8)

	ordered := slices.Clone(sigs)
	slices.SortFunc(ordered, func(a, b types.Signature) int {
		return cmp.Compare(a.SignerIndex, b.SignerIndex)
	})

	h := sha256.New()
	for _, s := range ordered {
		idx := int(s.SignerIndex)
		if idx >= n {
			return nil, nil, errorsmod.Wrapf(types.ErrInvalidSignature,
				"signer_index %d out of range (%d signer keys)", idx, n)
		}
		if bitmap[idx/8]&(byte(1)<<(idx%8)) != 0 {
			return nil, nil, errorsmod.Wrapf(types.ErrInvalidSignature,
				"duplicate signature for signer_index %d", idx)
		}

		pk, ok := sc.SignerKeys[idx].GetCachedValue().(cryptotypes.PubKey)
		if !ok {
			return nil, nil, errorsmod.Wrapf(types.ErrInvalidSignature,
				"signer key %d is not an unpacked public key", idx)
		}

		// Charge gas before the (expensive) verification.
		ctx.GasMeter().ConsumeGas(params.SignatureVerificationGas, "checkpoint signature verification")

		if !pk.VerifySignature(signBytes, s.Signature) {
			return nil, nil, errorsmod.Wrapf(types.ErrInvalidSignature,
				"signature from signer_index %d failed verification", idx)
		}

		bitmap[idx/8] |= byte(1) << (idx % 8)
		h.Write(s.Signature)
	}

	return bitmap, h.Sum(nil), nil
}

// chargeFee collects base_fee + per_record_fee * record_count from the
// submitter and sends it to the fee collector.
func (k Keeper) chargeFee(ctx context.Context, submitter sdk.AccAddress, params types.Params, recordCount uint64) error {
	amount := params.BaseFee.Amount.Add(
		params.PerRecordFee.Amount.Mul(math.NewIntFromUint64(recordCount)),
	)
	if amount.IsZero() {
		return nil
	}

	fee := sdk.NewCoins(sdk.NewCoin(params.BaseFee.Denom, amount))
	if err := k.bankKeeper.SendCoinsFromAccountToModule(ctx, submitter, authtypes.FeeCollectorName, fee); err != nil {
		return errorsmod.Wrap(err, "failed to pay checkpoint fee")
	}
	return nil
}
