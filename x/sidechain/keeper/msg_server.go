package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"

	"github.com/lucidpay/lucidchain/x/sidechain/types"
)

type msgServer struct {
	Keeper
}

var _ types.MsgServer = msgServer{}

// NewMsgServerImpl returns an implementation of the MsgServer interface.
func NewMsgServerImpl(keeper Keeper) types.MsgServer {
	return &msgServer{Keeper: keeper}
}

// paramsOrDefault is used by queries before genesis has stored params.
func (k Keeper) paramsOrDefault(ctx context.Context) (types.Params, error) {
	p, err := k.Params.Get(ctx)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return types.DefaultParams(), nil
		}
		return types.Params{}, err
	}
	return p, nil
}
