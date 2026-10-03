package keeper

import (
	"context"

	"github.com/lucidpay/lucidchain/x/sidechain/types"
)

// InitGenesis stores params and sidechains from the genesis state.
func (k Keeper) InitGenesis(ctx context.Context, gs types.GenesisState) error {
	if err := k.SetParams(ctx, gs.Params); err != nil {
		return err
	}
	for _, sc := range gs.Sidechains {
		if err := k.SetSidechain(ctx, sc); err != nil {
			return err
		}
	}
	return nil
}

// ExportGenesis returns the module's exported genesis state.
func (k Keeper) ExportGenesis(ctx context.Context) (*types.GenesisState, error) {
	params, err := k.GetParams(ctx)
	if err != nil {
		return nil, err
	}

	var scs []types.Sidechain
	err = k.Sidechains.Walk(ctx, nil, func(_ string, sc types.Sidechain) (bool, error) {
		scs = append(scs, sc)
		return false, nil
	})
	if err != nil {
		return nil, err
	}

	return &types.GenesisState{Params: params, Sidechains: scs}, nil
}
