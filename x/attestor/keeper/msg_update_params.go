package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"

	"github.com/lucidpay/lucidchain/x/attestor/types"
)

// UpdateParams implements types.MsgServer.
func (k msgServer) UpdateParams(ctx context.Context, req *types.MsgUpdateParams) (*types.MsgUpdateParamsResponse, error) {
	if err := k.checkAuthority(k.authority, req.Authority); err != nil {
		return nil, err
	}

	// Bonds are stored as bare amounts, so the denom must never change.
	current, err := k.Params.Get(ctx)
	switch {
	case err == nil:
		if current.BondDenom != req.Params.BondDenom {
			return nil, errorsmod.Wrapf(types.ErrInvalidRequest,
				"bond_denom cannot be changed (currently %q)", current.BondDenom)
		}
	case !errors.Is(err, collections.ErrNotFound):
		return nil, err
	}

	if err := k.SetParams(ctx, req.Params); err != nil {
		return nil, err
	}
	return &types.MsgUpdateParamsResponse{}, nil
}
