package keeper

import (
	"context"
	"errors"
	"fmt"
	"time"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/lucidpay/lucidchain/x/sidechain/types"
)

const (
	// maxExitsPerBlock bounds the work EndBlock does per block.
	MaxExitsPerBlock = 50
	// exitRetryDelay is how far a failed exit is pushed back in the queue.
	ExitRetryDelay = time.Hour
)

// ProcessExits returns the bond of every EXITED sidechain whose cooldown has
// passed, at most maxExitsPerBlock per block. Each exit runs in its own cached
// context; a failure is logged and the entry is moved back in the queue, so a
// bad entry can neither halt the chain nor starve the healthy ones behind it.
func (k Keeper) ProcessExits(ctx context.Context) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	now := sdkCtx.BlockTime()

	nowSec := now.Unix()
	if nowSec < 0 {
		nowSec = 0
	}

	// Collect first: the queue must not be modified while it is iterated.
	var due []collections.Pair[uint64, string]
	rng := collections.NewPrefixUntilPairRange[uint64, string](uint64(nowSec))
	err := k.ExitQueue.Walk(ctx, rng, func(key collections.Pair[uint64, string]) (bool, error) {
		due = append(due, key)
		return len(due) >= MaxExitsPerBlock, nil // true stops the walk
	})
	if err != nil {
		return err // a store-level failure: nothing sensible to continue with
	}

	for _, key := range due {
		if err := k.processExitSafe(sdkCtx, key); err != nil {
			sdkCtx.Logger().Error("failed to process sidechain exit",
				"sidechain", key.K2(), "err", err.Error())

			// Move the entry back so it cannot hog the front of the queue.
			// Never return an error from here: that would halt the chain.
			if rmErr := k.ExitQueue.Remove(ctx, key); rmErr != nil {
				sdkCtx.Logger().Error("failed to dequeue sidechain exit", "sidechain", key.K2(), "err", rmErr.Error())
				continue
			}
			if setErr := k.ExitQueue.Set(ctx, exitKey(now.Add(ExitRetryDelay), key.K2())); setErr != nil {
				sdkCtx.Logger().Error("failed to requeue sidechain exit", "sidechain", key.K2(), "err", setErr.Error())
			}
		}
	}
	return nil
}

// processExitSafe runs processExit in a cached context and turns a panic into
// an error. State is written only when processExit succeeds.
func (k Keeper) processExitSafe(ctx sdk.Context, key collections.Pair[uint64, string]) (err error) {
	cacheCtx, write := ctx.CacheContext()
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic while processing exit: %v", r)
		}
	}()
	if err := k.processExit(cacheCtx, key); err != nil {
		return err
	}
	write()
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

	// A queue entry is only valid for an EXITED sidechain. Anything else is a
	// stale entry and must never release a bond.
	if sc.Status != types.SidechainStatus_SIDECHAIN_STATUS_EXITED {
		return k.ExitQueue.Remove(ctx, key)
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
