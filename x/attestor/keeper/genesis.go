package keeper

import (
	"context"

	"github.com/lucidpay/lucidchain/x/attestor/types"
)

// InitGenesis stores the genesis state and rebuilds the derived indexes (the
// by-checkpoint attestation index and the exit queue). GenesisState.Validate
// has already checked the cross-references.
//
// The module account must be funded with the sum of all attestor bonds and
// open dispute bonds; that is the bank genesis' job, not this module's.
func (k Keeper) InitGenesis(ctx context.Context, gs types.GenesisState) error {
	if err := k.SetParams(ctx, gs.Params); err != nil {
		return err
	}

	for _, s := range gs.Schemas {
		if err := k.Schemas.Set(ctx, s.Id, s); err != nil {
			return err
		}
	}

	for _, a := range gs.Attestors {
		if err := k.Attestors.Set(ctx, a.Id, a); err != nil {
			return err
		}
		if a.Status == types.AttestorStatus_ATTESTOR_STATUS_EXITED &&
			a.BondReturnAt != nil && a.BondAmount.IsPositive() {
			if err := k.ExitQueue.Set(ctx, exitKey(*a.BondReturnAt, a.Id)); err != nil {
				return err
			}
		}
	}

	for _, at := range gs.Attestations {
		if err := k.setAttestation(ctx, at); err != nil {
			return err
		}
	}

	for _, d := range gs.Disputes {
		if err := k.Disputes.Set(ctx, d.Id, d); err != nil {
			return err
		}
	}
	return nil
}

// ExportGenesis returns the module's exported genesis state, with every list
// ordered by key.
func (k Keeper) ExportGenesis(ctx context.Context) (*types.GenesisState, error) {
	params, err := k.GetParams(ctx)
	if err != nil {
		return nil, err
	}

	gs := types.DefaultGenesis()
	gs.Params = params

	err = k.Attestors.Walk(ctx, nil, func(_ string, a types.Attestor) (bool, error) {
		gs.Attestors = append(gs.Attestors, a)
		return false, nil
	})
	if err != nil {
		return nil, err
	}

	err = k.Schemas.Walk(ctx, nil, func(_ string, s types.AttestationSchema) (bool, error) {
		gs.Schemas = append(gs.Schemas, s)
		return false, nil
	})
	if err != nil {
		return nil, err
	}

	err = k.Attestations.Walk(ctx, nil, func(_ string, at types.Attestation) (bool, error) {
		gs.Attestations = append(gs.Attestations, at)
		return false, nil
	})
	if err != nil {
		return nil, err
	}

	err = k.Disputes.Walk(ctx, nil, func(_ string, d types.Dispute) (bool, error) {
		gs.Disputes = append(gs.Disputes, d)
		return false, nil
	})
	if err != nil {
		return nil, err
	}

	return gs, nil
}
