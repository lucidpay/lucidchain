package keeper

import (
	"context"

	errorsmod "cosmossdk.io/errors"

	"github.com/lucidpay/lucidchain/x/proofs/types"
)

// InitGenesis stores the genesis state and rebuilds the by-checkpoint proof
// index. GenesisState.Validate has already checked the cross-references.
//
// Every verifier in genesis must have an implementation compiled into this
// binary: a chain that starts with a registered verifier it cannot run would
// reject every proof for it.
func (k Keeper) InitGenesis(ctx context.Context, gs types.GenesisState) error {
	if err := k.SetParams(ctx, gs.Params); err != nil {
		return err
	}

	for _, v := range gs.Verifiers {
		if !k.HasImplementation(v.ProofSystemId) {
			return errorsmod.Wrapf(types.ErrVerifierUnavailable,
				"genesis registers %q but this binary has no implementation for it", v.ProofSystemId)
		}
		if err := k.Verifiers.Set(ctx, v.ProofSystemId, v); err != nil {
			return err
		}
	}

	for _, p := range gs.Proofs {
		if err := k.setProof(ctx, p); err != nil {
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

	err = k.Verifiers.Walk(ctx, nil, func(_ string, v types.VerifierRegistration) (bool, error) {
		gs.Verifiers = append(gs.Verifiers, v)
		return false, nil
	})
	if err != nil {
		return nil, err
	}

	err = k.Proofs.Walk(ctx, nil, func(_ string, p types.ProofRecord) (bool, error) {
		gs.Proofs = append(gs.Proofs, p)
		return false, nil
	})
	if err != nil {
		return nil, err
	}

	return gs, nil
}
