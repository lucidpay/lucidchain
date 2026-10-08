package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"cosmossdk.io/collections"
	"cosmossdk.io/core/address"
	corestore "cosmossdk.io/core/store"
	errorsmod "cosmossdk.io/errors"
	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"

	"github.com/lucidpay/lucidchain/x/sidechain/types"
)

type Keeper struct {
	storeService corestore.KVStoreService
	cdc          codec.Codec
	addressCodec address.Codec
	// Address capable of executing a MsgUpdateParams message.
	// Typically, this should be the x/gov module account.
	authority []byte

	Schema collections.Schema
	Params collections.Item[types.Params]

	// Sidechains maps sidechain id -> registration record.
	Sidechains collections.Map[string, types.Sidechain]

	bankKeeper types.BankKeeper

	// ExitQueue orders EXITED sidechains awaiting their bond return by
	// (bond_return_at unix seconds, sidechain id).
	ExitQueue collections.KeySet[collections.Pair[uint64, string]]
}

func NewKeeper(
	storeService corestore.KVStoreService,
	cdc codec.Codec,
	addressCodec address.Codec,
	authority []byte,

	bankKeeper types.BankKeeper,
) Keeper {
	if _, err := addressCodec.BytesToString(authority); err != nil {
		panic(fmt.Sprintf("invalid authority address %s: %s", authority, err))
	}

	sb := collections.NewSchemaBuilder(storeService)

	k := Keeper{
		storeService: storeService,
		cdc:          cdc,
		addressCodec: addressCodec,
		authority:    authority,

		bankKeeper: bankKeeper,
		Params:     collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.Params](cdc)),
		Sidechains: collections.NewMap(
			sb,
			types.SidechainKey,
			"sidechains",
			collections.StringKey,
			codec.CollValue[types.Sidechain](cdc),
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

// ======================== Added

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

// GetSidechain returns a sidechain with its signer keys unpacked. It is the
// method x/checkpoint (and the other modules) call, so a missing sidechain
// returns an error that satisfies errors.Is(err, collections.ErrNotFound).
// Handlers inside this module use loadSidechain instead, which returns the
// registered types.ErrSidechainNotFound.
func (k Keeper) GetSidechain(ctx context.Context, id string) (types.Sidechain, error) {
	return k.Sidechains.Get(ctx, id)
}

// HasSidechain reports whether a sidechain id is registered.
func (k Keeper) HasSidechain(ctx context.Context, id string) (bool, error) {
	return k.Sidechains.Has(ctx, id)
}

// SetSidechain writes a sidechain record. Callers are responsible for
// validating the record before storing it.
func (k Keeper) SetSidechain(ctx context.Context, sc types.Sidechain) error {
	if sc.Id == "" {
		return errorsmod.Wrap(sdkerrors.ErrInvalidRequest, "sidechain id cannot be empty")
	}
	return k.Sidechains.Set(ctx, sc.Id, sc)
}

// loadSidechain returns a sidechain or the registered types.ErrSidechainNotFound.
func (k Keeper) loadSidechain(ctx context.Context, id string) (types.Sidechain, error) {
	sc, err := k.Sidechains.Get(ctx, id)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return types.Sidechain{}, errorsmod.Wrapf(types.ErrSidechainNotFound, "%q", id)
		}
		return types.Sidechain{}, err
	}
	return sc, nil
}

// RecordCheckpoint is the expected-keeper entrypoint for x/checkpoint. It
// updates the sidechain's last-checkpoint fields after a checkpoint has been
// accepted and verified by x/checkpoint.
//
// Rules enforced here (state owned by x/sidechain):
//   - the sidechain must exist and be ACTIVE
//   - the lc_sequence must be strictly greater than the last recorded one
//   - the height must not decrease
func (k Keeper) RecordCheckpoint(
	ctx context.Context,
	id string,
	lc_sequence uint64,
	height int64,
	hash []byte,
) error {
	sc, err := k.Sidechains.Get(ctx, id)
	if err != nil {
		if errorsmod.IsOf(err, collections.ErrNotFound) {
			return errorsmod.Wrapf(sdkerrors.ErrNotFound, "sidechain %q not found", id)
		}
		return err
	}

	if sc.Status != types.SidechainStatus_SIDECHAIN_STATUS_ACTIVE {
		return errorsmod.Wrapf(sdkerrors.ErrInvalidRequest,
			"sidechain %q is not active (status %s)", id, sc.Status)
	}
	if lc_sequence <= sc.LastCheckpointSequence {
		return errorsmod.Wrapf(sdkerrors.ErrInvalidRequest,
			"checkpoint lc_sequence %d must exceed last lc_sequence %d", lc_sequence, sc.LastCheckpointSequence)
	}
	if height < sc.LastCheckpointHeight {
		return errorsmod.Wrapf(sdkerrors.ErrInvalidRequest,
			"checkpoint height %d is below last height %d", height, sc.LastCheckpointHeight)
	}
	if len(hash) == 0 {
		return errorsmod.Wrap(sdkerrors.ErrInvalidRequest, "checkpoint hash cannot be empty")
	}

	blockTime := sdk.UnwrapSDKContext(ctx).BlockTime()

	sc.LastCheckpointSequence = lc_sequence
	sc.LastCheckpointHeight = height
	sc.LastCheckpointHash = hash
	sc.LastCheckpointAt = &blockTime

	return k.Sidechains.Set(ctx, id, sc)
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

// checkOperator verifies that signer is the sidechain's registered operator.
func (k Keeper) checkOperator(sc types.Sidechain, signer string) error {
	want, err := k.addressCodec.StringToBytes(sc.Operator)
	if err != nil {
		return errorsmod.Wrap(sdkerrors.ErrInvalidAddress, "stored operator address is invalid")
	}
	got, err := k.addressCodec.StringToBytes(signer)
	if err != nil {
		return errorsmod.Wrap(sdkerrors.ErrInvalidAddress, "invalid operator address")
	}
	if !bytes.Equal(want, got) {
		return errorsmod.Wrapf(types.ErrUnauthorized, "%s is not the operator of sidechain %q", signer, sc.Id)
	}
	return nil
}

// ---------------------------------------------------------------------------
// bond escrow
// ---------------------------------------------------------------------------

// escrowBond moves the bond from an account into the module account.
func (k Keeper) escrowBond(ctx context.Context, from sdk.AccAddress, bond sdk.Coin) error {
	if err := k.bankKeeper.SendCoinsFromAccountToModule(ctx, from, types.ModuleName, sdk.NewCoins(bond)); err != nil {
		return errorsmod.Wrap(err, "failed to escrow bond")
	}
	return nil
}

// releaseBond pays the bond from the module account to an account. A zero
// bond is a no-op.
func (k Keeper) releaseBond(ctx context.Context, to sdk.AccAddress, bond sdk.Coin) error {
	if !bond.IsPositive() {
		return nil
	}
	return k.bankKeeper.SendCoinsFromModuleToAccount(ctx, types.ModuleName, to, sdk.NewCoins(bond))
}

// zeroBond returns a zero coin of the same denom.
func zeroBond(bond sdk.Coin) sdk.Coin {
	return sdk.NewCoin(bond.Denom, math.ZeroInt())
}

// ---------------------------------------------------------------------------
// misc helpers
// ---------------------------------------------------------------------------

// exitKey builds the exit-queue key for a bond return time.
func exitKey(t time.Time, sidechainID string) collections.Pair[uint64, string] {
	sec := t.Unix()
	if sec < 0 {
		sec = 0
	}
	return collections.Join(uint64(sec), sidechainID)
}

// emit emits an event on the context's event manager.
func emit(ctx sdk.Context, eventType string, attrs ...sdk.Attribute) {
	ctx.EventManager().EmitEvent(sdk.NewEvent(eventType, attrs...))
}

func attr(key, value string) sdk.Attribute {
	return sdk.NewAttribute(key, value)
}

func uitoa(v uint64) string {
	return strconv.FormatUint(v, 10)
}
