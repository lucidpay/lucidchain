package keeper

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"cosmossdk.io/collections"
	"cosmossdk.io/core/address"
	corestore "cosmossdk.io/core/store"
	errorsmod "cosmossdk.io/errors"
	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"

	"github.com/lucidpay/lucidchain/x/attestor/types"
)

type Keeper struct {
	storeService corestore.KVStoreService
	cdc          codec.Codec
	addressCodec address.Codec
	// Address capable of executing a MsgUpdateParams message.
	// Typically, this should be the x/gov module account.
	authority        []byte
	checkpointKeeper types.CheckpointKeeper

	Schema collections.Schema
	Params collections.Item[types.Params]

	bankKeeper types.BankKeeper
	// Attestors maps attestor id -> registration record.
	Attestors collections.Map[string, types.Attestor]

	// Schemas maps schema id -> attestation schema.
	Schemas collections.Map[string, types.AttestationSchema]

	// Attestations maps attestation id -> attestation.
	Attestations collections.Map[string, types.Attestation]

	// AttestationsByCheckpoint maps ((sidechain_id, lc_sequence), attestation_id) -> attestation_id.
	// It exists so a checkpoint's attestations can be listed and paginated.
	AttestationsByCheckpoint collections.Map[collections.Pair[collections.Pair[string, uint64], string], string]

	// Disputes maps dispute id -> dispute.
	Disputes collections.Map[string, types.Dispute]

	// ExitQueue orders EXITED attestors awaiting their bond return by
	// (bond_return_at unix seconds, attestor id).
	ExitQueue collections.KeySet[collections.Pair[uint64, string]]
}

func NewKeeper(
	storeService corestore.KVStoreService,
	cdc codec.Codec,
	addressCodec address.Codec,
	authority []byte,
	checkpointKeeper types.CheckpointKeeper,
	bankKeeper types.BankKeeper,
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
		Params:           collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.Params](cdc)),
		Attestors: collections.NewMap(
			sb, types.AttestorKey, "attestors",
			collections.StringKey,
			codec.CollValue[types.Attestor](cdc),
		),
		Schemas: collections.NewMap(
			sb, types.SchemaKey, "schemas",
			collections.StringKey,
			codec.CollValue[types.AttestationSchema](cdc),
		),
		Attestations: collections.NewMap(
			sb, types.AttestationKey, "attestations",
			collections.StringKey,
			codec.CollValue[types.Attestation](cdc),
		),
		AttestationsByCheckpoint: collections.NewMap(
			sb, types.AttestationByCheckpointKey, "attestations_by_checkpoint",
			collections.PairKeyCodec(
				collections.PairKeyCodec(collections.StringKey, collections.Uint64Key),
				collections.StringKey,
			),
			collections.StringValue,
		),
		Disputes: collections.NewMap(
			sb, types.DisputeKey, "disputes",
			collections.StringKey,
			codec.CollValue[types.Dispute](cdc),
		),
		ExitQueue: collections.NewKeySet(
			sb, types.ExitQueueKey, "exit_queue",
			collections.PairKeyCodec(collections.Uint64Key, collections.StringKey),
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
	if p.DisputeResolverAuthority != "" {
		if _, err := k.addressCodec.StringToBytes(p.DisputeResolverAuthority); err != nil {
			return errorsmod.Wrapf(sdkerrors.ErrInvalidAddress, "invalid dispute_resolver_authority: %s", err)
		}
	}
	return k.Params.Set(ctx, p)
}

// GetDisputeResolver returns the address allowed to resolve disputes: the
// configured dispute_resolver_authority, or the module authority when unset.
func (k Keeper) GetDisputeResolver(ctx context.Context) ([]byte, error) {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	if p.DisputeResolverAuthority == "" {
		return k.authority, nil
	}
	return k.addressCodec.StringToBytes(p.DisputeResolverAuthority)
}

// ---------------------------------------------------------------------------
// typed getters (not-found becomes a registered module error)
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

// GetAttestor returns an attestor or types.ErrAttestorNotFound.
func (k Keeper) GetAttestor(ctx context.Context, id string) (types.Attestor, error) {
	return getOrWrap(ctx, k.Attestors, id, types.ErrAttestorNotFound)
}

// GetSchema returns a schema or types.ErrSchemaNotFound.
func (k Keeper) GetSchema(ctx context.Context, id string) (types.AttestationSchema, error) {
	return getOrWrap(ctx, k.Schemas, id, types.ErrSchemaNotFound)
}

// GetAttestation returns an attestation or types.ErrAttestationNotFound.
func (k Keeper) GetAttestation(ctx context.Context, id string) (types.Attestation, error) {
	return getOrWrap(ctx, k.Attestations, id, types.ErrAttestationNotFound)
}

// GetDispute returns a dispute or types.ErrDisputeNotFound.
func (k Keeper) GetDispute(ctx context.Context, id string) (types.Dispute, error) {
	return getOrWrap(ctx, k.Disputes, id, types.ErrDisputeNotFound)
}

// setAttestation writes an attestation and its by-checkpoint index entry.
func (k Keeper) setAttestation(ctx context.Context, a types.Attestation) error {
	if err := k.Attestations.Set(ctx, a.Id, a); err != nil {
		return err
	}
	idx := collections.Join(collections.Join(a.SidechainId, a.CheckpointSequence), a.Id)
	return k.AttestationsByCheckpoint.Set(ctx, idx, a.Id)
}

// exitKey builds the exit-queue key for a bond return time.
func exitKey(t time.Time, attestorID string) collections.Pair[uint64, string] {
	sec := t.Unix()
	if sec < 0 {
		sec = 0
	}
	return collections.Join(uint64(sec), attestorID)
}

// ---------------------------------------------------------------------------
// authorization helpers
// ---------------------------------------------------------------------------

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

// checkOperator verifies that signer is the attestor's registered operator.
func (k Keeper) checkOperator(a types.Attestor, signer string) error {
	want, err := k.addressCodec.StringToBytes(a.Operator)
	if err != nil {
		return errorsmod.Wrap(sdkerrors.ErrInvalidAddress, "stored operator address is invalid")
	}
	got, err := k.addressCodec.StringToBytes(signer)
	if err != nil {
		return errorsmod.Wrap(sdkerrors.ErrInvalidAddress, "invalid signer address")
	}
	if !bytes.Equal(want, got) {
		return errorsmod.Wrapf(types.ErrUnauthorized, "%s is not the operator of attestor %q", signer, a.Id)
	}
	return nil
}

// ---------------------------------------------------------------------------
// bond escrow
// ---------------------------------------------------------------------------

func bondCoins(p types.Params, amt math.Int) sdk.Coins {
	return sdk.NewCoins(sdk.NewCoin(p.BondDenom, amt))
}

// escrowBond moves amt from an account into the module account.
func (k Keeper) escrowBond(ctx context.Context, from sdk.AccAddress, p types.Params, amt math.Int) error {
	if err := k.bankKeeper.SendCoinsFromAccountToModule(ctx, from, types.ModuleName, bondCoins(p, amt)); err != nil {
		return errorsmod.Wrap(err, "failed to escrow bond")
	}
	return nil
}

// releaseBond pays amt from the module account to an account. Zero is a no-op.
func (k Keeper) releaseBond(ctx context.Context, to sdk.AccAddress, p types.Params, amt math.Int) error {
	if !amt.IsPositive() {
		return nil
	}
	return k.bankKeeper.SendCoinsFromModuleToAccount(ctx, types.ModuleName, to, bondCoins(p, amt))
}

// burnBond burns amt from the module account. Zero is a no-op.
func (k Keeper) burnBond(ctx context.Context, p types.Params, amt math.Int) error {
	if !amt.IsPositive() {
		return nil
	}
	return k.bankKeeper.BurnCoins(ctx, types.ModuleName, bondCoins(p, amt))
}

// ---------------------------------------------------------------------------
// signature verification
// ---------------------------------------------------------------------------

// verifySignatures verifies every submitted signature against an attestor's
// signer keys and returns the signer bitmap and signatures digest.
//
// Bitmap layout: bit i lives in byte i/8 at position i%8 (least significant
// bit first). Digest: SHA-256 over the verified signatures ordered by
// signer_index. Any invalid, duplicate, or out-of-range signature rejects the
// whole message, so a submitter cannot pad a message with junk. The threshold
// and count checks run before any (expensive) verification.
func (k Keeper) verifySignatures(
	ctx sdk.Context,
	keys []*codectypes.Any,
	threshold uint32,
	params types.Params,
	signBytes []byte,
	sigs []types.Signature,
) (bitmap, digest []byte, err error) {
	need := max(threshold, 1)
	if uint32(len(sigs)) < need {
		return nil, nil, errorsmod.Wrapf(types.ErrThresholdNotMet,
			"%d signatures supplied, threshold is %d", len(sigs), need)
	}

	n := len(keys)
	if len(sigs) > n {
		return nil, nil, errorsmod.Wrapf(types.ErrInvalidSignature,
			"%d signatures supplied but only %d signer keys are registered", len(sigs), n)
	}
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

		pk, ok := keys[idx].GetCachedValue().(cryptotypes.PubKey)
		if !ok {
			return nil, nil, errorsmod.Wrapf(types.ErrInvalidSignature,
				"signer key %d is not an unpacked public key", idx)
		}

		// Charge gas before the (expensive) verification.
		ctx.GasMeter().ConsumeGas(params.SignatureVerificationGas, "attestation signature verification")

		if !pk.VerifySignature(signBytes, s.Signature) {
			return nil, nil, errorsmod.Wrapf(types.ErrInvalidSignature,
				"signature from signer_index %d failed verification", idx)
		}

		bitmap[idx/8] |= byte(1) << (idx % 8)
		h.Write(s.Signature)
	}

	return bitmap, h.Sum(nil), nil
}

// ---------------------------------------------------------------------------
// misc helpers
// ---------------------------------------------------------------------------

// validateText checks a free-text field against a byte limit.
func validateText(field, v string, maxBytes int, required bool) error {
	if required && v == "" {
		return errorsmod.Wrapf(types.ErrInvalidRequest, "%s is required", field)
	}
	if len(v) > maxBytes {
		return errorsmod.Wrapf(types.ErrLimitExceeded, "%s is %d bytes, max %d", field, len(v), maxBytes)
	}
	return nil
}

// emit emits a typed-less event on the context's event manager.
func emit(ctx sdk.Context, eventType string, attrs ...sdk.Attribute) {
	ctx.EventManager().EmitEvent(sdk.NewEvent(eventType, attrs...))
}

func attr(key, value string) sdk.Attribute {
	return sdk.NewAttribute(key, value)
}

func uitoa(v uint64) string {
	return strconv.FormatUint(v, 10)
}

func boolStr(b bool) string {
	return strconv.FormatBool(b)
}
