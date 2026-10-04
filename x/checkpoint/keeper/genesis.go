package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"

	"github.com/lucidpay/lucidchain/x/checkpoint/types"
)

// InitGenesis initializes the module's state from a provided genesis state.
func (k Keeper) InitGenesis(ctx context.Context, genState types.GenesisState) error {

	//		return k.Params.Set(ctx, genState.Params)

	if err := k.SetParams(ctx, genState.Params); err != nil {
		return err
	}

	for _, cp := range genState.Checkpoints {
		if err := k.Checkpoints.Set(ctx, collections.Join(cp.SidechainId, cp.Sequence), cp); err != nil {
			return err
		}

		// Keep the highest sequence per sidechain. Reading before writing keeps
		// this independent of the order of genesis entries and of map iteration.
		latest, err := k.LatestSequence.Get(ctx, cp.SidechainId)
		if err != nil && !errors.Is(err, collections.ErrNotFound) {
			return err
		}
		if errors.Is(err, collections.ErrNotFound) || cp.Sequence > latest {
			if err := k.LatestSequence.Set(ctx, cp.SidechainId, cp.Sequence); err != nil {
				return err
			}
		}
	}

	return nil

}

// ExportGenesis returns the module's exported genesis.
func (k Keeper) ExportGenesis(ctx context.Context) (*types.GenesisState, error) {
	var err error

	genesis := types.DefaultGenesis()
	genesis.Params, err = k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}

	return genesis, nil
}
