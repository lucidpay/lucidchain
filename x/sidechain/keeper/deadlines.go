package keeper

import (
	"context"
	"errors"
	"fmt"
	"time"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/lucidpay/lucidchain/x/sidechain/types"
)

const (
	// MaxDeadlinesPerBlock bounds the work EndBlock does per block.
	MaxDeadlinesPerBlock = 100
	// DeadlineRetryDelay is how far a failed deadline is pushed back.
	DeadlineRetryDelay = time.Hour
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// deadlineKey builds the deadline-queue key. Deadlines are stored truncated to
// whole seconds (see freshDeadline), so the key's second always equals the
// record's deadline and "now >= key" implies "now >= record deadline".
func deadlineKey(t time.Time, sidechainID string) collections.Pair[uint64, string] {
	return exitKey(t, sidechainID)
}

func unixSec(t time.Time) uint64 {
	s := t.Unix()
	if s < 0 {
		return 0
	}
	return uint64(s)
}

func sameInstant(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

// freshDeadline is base + checkpoint interval + the tier's grace period,
// truncated to whole seconds. Durations are clamped so time arithmetic cannot
// overflow whatever the stored interval is.
func freshDeadline(base time.Time, sc types.Sidechain, p types.Params) time.Time {
	interval := min(sc.CheckpointIntervalSeconds, types.MaxPeriodSeconds)
	grace := min(sc.EffectiveMissedPolicy(p).GraceSeconds, types.MaxPeriodSeconds)
	return base.Add(time.Duration(interval+grace) * time.Second).Truncate(time.Second)
}

// ---------------------------------------------------------------------------
// the single write path for sidechain records
// ---------------------------------------------------------------------------

// SetSidechain writes a sidechain record and keeps DeadlineQueue consistent
// with it. Every write to Sidechains must go through here.
//
// Rules, applied to the record being written:
//   - not ACTIVE: NextDeadlineAt is cleared (suspended, slashed, exited and
//     pending sidechains are never penalized for missing checkpoints).
//   - ACTIVE with NextDeadlineAt == nil: a fresh deadline is assigned from the
//     current block time. Callers ask for a reset by clearing the field. This is
//     what makes register(auto_activate), activate, resume, an accepted
//     checkpoint and a processed miss all start a new window.
//   - ACTIVE with NextDeadlineAt set: kept as is, so operations such as signer
//     rotation, interval changes and tier upgrades can never push a deadline
//     out.
//
// Callers are responsible for validating the record before storing it.
func (k Keeper) SetSidechain(ctx context.Context, sc types.Sidechain) error {
	if sc.Id == "" {
		return errorsmod.Wrap(types.ErrInvalidRequest, "sidechain id cannot be empty")
	}

	var prevAt *time.Time
	prev, err := k.Sidechains.Get(ctx, sc.Id)
	switch {
	case err == nil:
		prevAt = prev.NextDeadlineAt
	case errors.Is(err, collections.ErrNotFound):
	default:
		return err
	}

	if sc.Status != types.SidechainStatus_SIDECHAIN_STATUS_ACTIVE {
		sc.NextDeadlineAt = nil
	} else if sc.NextDeadlineAt == nil {
		params, err := k.paramsOrDefault(ctx)
		if err != nil {
			return err
		}
		d := freshDeadline(sdk.UnwrapSDKContext(ctx).BlockTime(), sc, params)
		sc.NextDeadlineAt = &d
	}

	if !sameInstant(prevAt, sc.NextDeadlineAt) {
		if prevAt != nil {
			if err := k.DeadlineQueue.Remove(ctx, deadlineKey(*prevAt, sc.Id)); err != nil {
				return err
			}
		}
		if sc.NextDeadlineAt != nil {
			if err := k.DeadlineQueue.Set(ctx, deadlineKey(*sc.NextDeadlineAt, sc.Id)); err != nil {
				return err
			}
		}
	}
	return k.Sidechains.Set(ctx, sc.Id, sc)
}

// ---------------------------------------------------------------------------
// RecordCheckpoint hook
// ---------------------------------------------------------------------------

// applyCheckpointLiveness is called by RecordCheckpoint after it has updated
// the last-checkpoint fields. It decays strikes for on-time checkpoints and
// clears the deadline so SetSidechain starts a new window.
func (k Keeper) applyCheckpointLiveness(ctx context.Context, sc *types.Sidechain, at time.Time) error {
	params, err := k.paramsOrDefault(ctx)
	if err != nil {
		return err
	}
	policy := sc.EffectiveMissedPolicy(params)

	onTime := sc.NextDeadlineAt == nil || !at.After(*sc.NextDeadlineAt)
	if onTime && sc.MissedStrikes > 0 && policy.StrikeDecayCheckpoints > 0 {
		sc.OnTimeStreak++
		if sc.OnTimeStreak >= policy.StrikeDecayCheckpoints {
			sc.MissedStrikes--
			sc.OnTimeStreak = 0
		}
	}
	sc.NextDeadlineAt = nil
	return nil
}

// ---------------------------------------------------------------------------
// EndBlock processing
// ---------------------------------------------------------------------------

// ProcessMissedCheckpoints applies each tier's missed-checkpoint policy to
// every ACTIVE sidechain whose deadline has passed, at most
// MaxDeadlinesPerBlock per block. It follows ProcessExits: entries are
// collected before any is modified, each runs in its own cached context, and a
// failure is logged and the entry pushed back so it can neither halt the chain
// nor starve the healthy entries behind it.
func (k Keeper) ProcessMissedCheckpoints(ctx context.Context) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	now := sdkCtx.BlockTime()

	params, err := k.paramsOrDefault(ctx)
	if err != nil {
		return err
	}
	forgiveBefore, err := k.updateBlockGuard(ctx, params, now)
	if err != nil {
		return err
	}

	var due []collections.Pair[uint64, string]
	rng := collections.NewPrefixUntilPairRange[uint64, string](unixSec(now))
	err = k.DeadlineQueue.Walk(ctx, rng, func(key collections.Pair[uint64, string]) (bool, error) {
		due = append(due, key)
		return len(due) >= MaxDeadlinesPerBlock, nil // true stops the walk
	})
	if err != nil {
		return err
	}

	for _, key := range due {
		if err := k.processDeadlineSafe(sdkCtx, key, params, forgiveBefore); err != nil {
			sdkCtx.Logger().Error("failed to process sidechain deadline",
				"sidechain", key.K2(), "err", err.Error())

			// Never return an error from here: that would halt the chain.
			if rmErr := k.DeadlineQueue.Remove(ctx, key); rmErr != nil {
				sdkCtx.Logger().Error("failed to dequeue sidechain deadline", "sidechain", key.K2(), "err", rmErr.Error())
				continue
			}
			if setErr := k.DeadlineQueue.Set(ctx, deadlineKey(now.Add(DeadlineRetryDelay), key.K2())); setErr != nil {
				sdkCtx.Logger().Error("failed to requeue sidechain deadline", "sidechain", key.K2(), "err", setErr.Error())
			}
		}
	}
	return nil
}

// updateBlockGuard protects sidechains from chain-side outages. It records the
// time of every block; when the gap since the previous block exceeds
// Params.MaxBlockGapSeconds (0 disables the guard) it sets a "forgive before"
// mark at the current block time. Deadlines at or before the mark are rebased
// without a strike, however many blocks it takes to work through them. It
// returns the current mark.
func (k Keeper) updateBlockGuard(ctx context.Context, params types.Params, now time.Time) (uint64, error) {
	nowSec := unixSec(now)

	forgive, err := k.ForgiveBefore.Get(ctx)
	if err != nil {
		if !errors.Is(err, collections.ErrNotFound) {
			return 0, err
		}
		forgive = 0
	}

	last, err := k.LastBlockTime.Get(ctx)
	hasLast := err == nil
	if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return 0, err
	}

	if hasLast && params.MaxBlockGapSeconds > 0 && nowSec > last && nowSec-last > params.MaxBlockGapSeconds {
		forgive = nowSec
		if err := k.ForgiveBefore.Set(ctx, forgive); err != nil {
			return 0, err
		}
	}
	if err := k.LastBlockTime.Set(ctx, nowSec); err != nil {
		return 0, err
	}
	return forgive, nil
}

// processDeadlineSafe runs processDeadline in a cached context and turns a
// panic into an error. State is written only when processDeadline succeeds.
func (k Keeper) processDeadlineSafe(
	ctx sdk.Context,
	key collections.Pair[uint64, string],
	params types.Params,
	forgiveBefore uint64,
) (err error) {
	cacheCtx, write := ctx.CacheContext()
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic while processing deadline: %v", r)
		}
	}()
	if err := k.processDeadline(cacheCtx, key, params, forgiveBefore); err != nil {
		return err
	}
	write()
	return nil
}

// processDeadline handles one due queue entry. The sidechain record is the
// source of truth: the queue is only an index, so an entry that no longer
// matches its record is dropped rather than acted on.
func (k Keeper) processDeadline(
	ctx sdk.Context,
	key collections.Pair[uint64, string],
	params types.Params,
	forgiveBefore uint64,
) error {
	if err := k.DeadlineQueue.Remove(ctx, key); err != nil {
		return err
	}

	sc, err := k.Sidechains.Get(ctx, key.K2())
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil // dangling entry
		}
		return err
	}
	if sc.Status != types.SidechainStatus_SIDECHAIN_STATUS_ACTIVE {
		return nil
	}
	if sc.NextDeadlineAt == nil {
		return k.SetSidechain(ctx, sc) // ACTIVE without a deadline: assign one
	}

	now := ctx.BlockTime()
	if now.Before(*sc.NextDeadlineAt) {
		// Stale entry (the sidechain was rebased since). Make sure the record's
		// own entry exists, then drop this one.
		return k.DeadlineQueue.Set(ctx, deadlineKey(*sc.NextDeadlineAt, sc.Id))
	}

	// Chain-side outage: rebase without a strike.
	if unixSec(*sc.NextDeadlineAt) <= forgiveBefore {
		sc.NextDeadlineAt = nil
		if err := k.SetSidechain(ctx, sc); err != nil {
			return err
		}
		emit(ctx, "sidechain_deadline_forgiven", attr("sidechain_id", sc.Id))
		return nil
	}

	policy := sc.EffectiveMissedPolicy(params)

	sc.MissedStrikes++
	sc.OnTimeStreak = 0
	sc.NextDeadlineAt = nil // SetSidechain assigns a fresh window if still ACTIVE

	burned := zeroBond(sc.Bond)
	if policy.Action == types.MissedCheckpointAction_MISSED_CHECKPOINT_ACTION_PENALIZE &&
		!policy.PenaltyFraction.IsNil() && sc.Bond.IsPositive() {
		amt := sc.Bond.Amount.ToLegacyDec().Mul(policy.PenaltyFraction).TruncateInt()
		// A fraction of a tiny bond can truncate to zero: the strike still
		// counts, there is just nothing to burn.
		if amt.IsPositive() {
			burned = sdk.NewCoin(sc.Bond.Denom, amt)
			if err := k.burnBond(ctx, burned); err != nil {
				return errorsmod.Wrap(err, "failed to burn missed-checkpoint penalty")
			}
			sc.Bond = sc.Bond.Sub(burned)
		}
	}

	suspend := policy.Action == types.MissedCheckpointAction_MISSED_CHECKPOINT_ACTION_SUSPEND ||
		(policy.SuspendAfterStrikes > 0 && sc.MissedStrikes >= policy.SuspendAfterStrikes)
	if suspend {
		sc.Status = types.SidechainStatus_SIDECHAIN_STATUS_SUSPENDED
	}

	if err := k.SetSidechain(ctx, sc); err != nil {
		return err
	}

	emit(ctx, "sidechain_checkpoint_missed",
		attr("sidechain_id", sc.Id),
		attr("tier", sc.Tier.String()),
		attr("policy_source", policySource(sc)),
		attr("strikes", uitoa(uint64(sc.MissedStrikes))),
		attr("action", policy.Action.String()),
		attr("burned", burned.String()),
		attr("bond", sc.Bond.String()),
		attr("status", sc.Status.String()),
	)
	return nil
}

// ---------------------------------------------------------------------------
// migration v1 -> v2
// ---------------------------------------------------------------------------

// MigrateV1ToV2 gives every ACTIVE sidechain a deadline measured from the
// upgrade block, so nobody is penalized for the time before this feature
// existed. Params need no migration: with no per-tier policies, misses only
// record strikes until governance sets policies through MsgUpdateParams.
func (k Keeper) MigrateV1ToV2(ctx context.Context) error {
	// Collect first: the map must not be modified while it is iterated.
	var ids []string
	err := k.Sidechains.Walk(ctx, nil, func(id string, sc types.Sidechain) (bool, error) {
		if sc.Status == types.SidechainStatus_SIDECHAIN_STATUS_ACTIVE && sc.NextDeadlineAt == nil {
			ids = append(ids, id)
		}
		return false, nil
	})
	if err != nil {
		return err
	}
	for _, id := range ids {
		sc, err := k.Sidechains.Get(ctx, id)
		if err != nil {
			return err
		}
		if err := k.SetSidechain(ctx, sc); err != nil { // assigns the fresh deadline
			return err
		}
	}
	return nil
}

func policySource(sc types.Sidechain) string {
	if sc.MissedPolicyOverride != nil {
		return "sidechain_override"
	}
	return "tier"
}
