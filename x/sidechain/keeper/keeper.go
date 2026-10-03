package keeper

import (
	"context"
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/core/address"
	corestore "cosmossdk.io/core/store"
	errorsmod "cosmossdk.io/errors"
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

// GetSidechain returns a sidechain by id.
func (k Keeper) GetSidechain(ctx context.Context, id string) (types.Sidechain, error) {
	sc, err := k.Sidechains.Get(ctx, id)
	if err != nil {
		return types.Sidechain{}, err
	}
	return sc, nil
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

// RecordCheckpoint is the expected-keeper entrypoint for x/checkpoint. It
// updates the sidechain's last-checkpoint fields after a checkpoint has been
// accepted and verified by x/checkpoint.
//
// Rules enforced here (state owned by x/sidechain):
//   - the sidechain must exist and be ACTIVE
//   - the sequence must be strictly greater than the last recorded one
//   - the height must not decrease
func (k Keeper) RecordCheckpoint(
	ctx context.Context,
	id string,
	sequence uint64,
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
	if sequence <= sc.LastCheckpointSequence {
		return errorsmod.Wrapf(sdkerrors.ErrInvalidRequest,
			"checkpoint sequence %d must exceed last sequence %d", sequence, sc.LastCheckpointSequence)
	}
	if height < sc.LastCheckpointHeight {
		return errorsmod.Wrapf(sdkerrors.ErrInvalidRequest,
			"checkpoint height %d is below last height %d", height, sc.LastCheckpointHeight)
	}
	if len(hash) == 0 {
		return errorsmod.Wrap(sdkerrors.ErrInvalidRequest, "checkpoint hash cannot be empty")
	}

	blockTime := sdk.UnwrapSDKContext(ctx).BlockTime()

	sc.LastCheckpointSequence = sequence
	sc.LastCheckpointHeight = height
	sc.LastCheckpointHash = hash
	sc.LastCheckpointAt = &blockTime

	return k.Sidechains.Set(ctx, id, sc)
}
