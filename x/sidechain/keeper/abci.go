package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// ProcessExits returns the bond of every EXITED sidechain whose cooldown has
// passed. Wire it into the module's EndBlock.
//
// Each sidechain is processed in its own cached context, and a failure is
// logged and skipped (it is retried next block), so a bad entry can never halt
// the chain.
func (k Keeper) ProcessExits(ctx context.Context) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)

	nowSec := sdkCtx.BlockTime().Unix()
	if nowSec < 0 {
		nowSec = 0
	}

	// Collect first: the queue must not be modified while it is iterated.
	var due []collections.Pair[uint64, string]
	rng := collections.NewPrefixUntilPairRange[uint64, string](uint64(nowSec))
	err := k.ExitQueue.Walk(ctx, rng, func(key collections.Pair[uint64, string]) (bool, error) {
		due = append(due, key)
		return false, nil
	})
	if err != nil {
		return err
	}

	for _, key := range due {
		cacheCtx, write := sdkCtx.CacheContext()
		if err := k.processExit(cacheCtx, key); err != nil {
			sdkCtx.Logger().Error("failed to process sidechain exit",
				"sidechain", key.K2(), "err", err.Error())
			continue
		}
		write()
	}
	return nil
}

func (k Keeper) processExit(ctx sdk.Context, key collections.Pair[uint64, string]) error {
	sc, err := k.Sidechains.Get(ctx, key.K2())
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			// Dangling queue entry: drop it.
			return k.ExitQueue.Remove(ctx, key)
		}
		return err
	}

	if sc.Bond.IsPositive() {
		operator, err := k.addressCodec.StringToBytes(sc.Operator)
		if err != nil {
			return err
		}
		if err := k.releaseBond(ctx, operator, sc.Bond); err != nil {
			return err
		}
		emit(ctx, "sidechain_bond_returned",
			attr("sidechain_id", sc.Id), attr("amount", sc.Bond.String()))
	}

	sc.Bond = zeroBond(sc.Bond)
	if err := k.Sidechains.Set(ctx, sc.Id, sc); err != nil {
		return err
	}
	return k.ExitQueue.Remove(ctx, key)
}
