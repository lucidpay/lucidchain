package keeper

import (
	"context"

	errorsmod "cosmossdk.io/errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/lucidpay/lucidchain/x/sidechain/types"
)

func (k Keeper) InitGenesis(ctx context.Context, gs types.GenesisState) error {
	if err := gs.Validate(); err != nil {
		return err
	}
	if err := k.SetParams(ctx, gs.Params); err != nil {
		return err
	}

	total := sdk.NewCoins()
	for _, sc := range gs.Sidechains {
		if _, err := k.addressCodec.StringToBytes(sc.Operator); err != nil {
			return errorsmod.Wrapf(types.ErrInvalidGenesis,
				"sidechain %q: invalid operator: %s", sc.Id, err)
		}
		if err := k.SetSidechain(ctx, sc); err != nil {
			return err
		}
		if sc.Status == types.SidechainStatus_SIDECHAIN_STATUS_EXITED &&
			sc.BondReturnAt != nil && sc.Bond.IsPositive() {
			if err := k.ExitQueue.Set(ctx, exitKey(*sc.BondReturnAt, sc.Id)); err != nil {
				return err
			}
		}
		if sc.Bond.IsPositive() {
			total = total.Add(sc.Bond)
		}
	}

	// Solvency: the module account must hold at least the sum of all bonds
	// still recorded. This relies on bank initializing before this module
	// (it does in InitGenesis order in app_config.go).
	bal := k.bankKeeper.GetAllBalances(ctx, authtypes.NewModuleAddress(types.ModuleName))
	if !bal.IsAllGTE(total) {
		return errorsmod.Wrapf(types.ErrInvalidGenesis,
			"module balance %s is less than the sum of bonds %s", bal, total)
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
