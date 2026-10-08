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
		if sc.Status == types.SidechainStatus_SIDECHAIN_STATUS_EXITED &&
			sc.BondReturnAt != nil && sc.Bond.IsPositive() {
			if err := k.ExitQueue.Set(ctx, exitKey(*sc.BondReturnAt, sc.Id)); err != nil {
				return err
			}
		}
	}
	return nil
}

// ExportGenesis returns the module's exported genesis state, ordered by sidechain id.
func (k Keeper) ExportGenesis(ctx context.Context) (*types.GenesisState, error) {
	params, err := k.GetParams(ctx)
	if err != nil {
		return nil, err
	}

	gs := types.DefaultGenesis()
	gs.Params = params

	err = k.Sidechains.Walk(ctx, nil, func(_ string, sc types.Sidechain) (bool, error) {
		gs.Sidechains = append(gs.Sidechains, sc)
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	return gs, nil
}
