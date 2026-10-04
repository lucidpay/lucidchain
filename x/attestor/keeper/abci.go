package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/lucidpay/lucidchain/x/attestor/types"
)

// ProcessExits returns the remaining bond of every EXITED attestor whose
// cooldown has passed and that has no open disputes. Wire it into the module's
// EndBlock.
//
// An attestor with open disputes stays in the queue and is re-checked every
// block. Each attestor is processed in its own cached context and a failure is
// logged and skipped, so a bad entry can never halt the chain.
func (k Keeper) ProcessExits(ctx context.Context) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)

	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}

	nowSec := sdkCtx.BlockTime().Unix()
	if nowSec < 0 {
		nowSec = 0
	}

	// Collect first: the queue must not be modified while it is iterated.
	var due []collections.Pair[uint64, string]
	rng := collections.NewPrefixUntilPairRange[uint64, string](uint64(nowSec))
	err = k.ExitQueue.Walk(ctx, rng, func(key collections.Pair[uint64, string]) (bool, error) {
		due = append(due, key)
		return false, nil
	})
	if err != nil {
		return err
	}

	for _, key := range due {
		cacheCtx, write := sdkCtx.CacheContext()
		done, err := k.processExit(cacheCtx, params, key)
		if err != nil {
			sdkCtx.Logger().Error("failed to process attestor exit",
				"attestor", key.K2(), "err", err.Error())
			continue
		}
		if done {
			write()
		}
	}
	return nil
}

// processExit returns done=false when the entry must be left untouched.
func (k Keeper) processExit(ctx sdk.Context, params types.Params, key collections.Pair[uint64, string]) (done bool, err error) {
	id := key.K2()

	attestor, err := k.Attestors.Get(ctx, id)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			// Dangling queue entry: drop it.
			return true, k.ExitQueue.Remove(ctx, key)
		}
		return false, err
	}

	if attestor.OpenDisputes > 0 {
		return false, nil
	}

	if attestor.BondAmount.IsPositive() {
		operator, err := k.addressCodec.StringToBytes(attestor.Operator)
		if err != nil {
			return false, err
		}
		if err := k.releaseBond(ctx, operator, params, attestor.BondAmount); err != nil {
			return false, err
		}
		emit(ctx, "attestor_bond_returned",
			attr("attestor_id", attestor.Id), attr("amount", attestor.BondAmount.String()))
	}

	attestor.BondAmount = math.ZeroInt()
	if err := k.Attestors.Set(ctx, attestor.Id, attestor); err != nil {
		return false, err
	}
	return true, k.ExitQueue.Remove(ctx, key)
}
