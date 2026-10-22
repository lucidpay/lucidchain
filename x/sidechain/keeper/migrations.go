package keeper

import (
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/lucidpay/lucidchain/x/sidechain/types"
)

// Migrator handles in-place store migrations.
type Migrator struct{ k Keeper }

// NewMigrator returns a Migrator for the keeper.
func NewMigrator(k Keeper) Migrator { return Migrator{k: k} }

// Migrate1to2 fills the parameters introduced at ConsensusVersion 2 (the
// v0.4-Beta review series). Stored Params from version 1 decode with
// rotation_signature_gas = 0, which Params.Validate rejects and which would
// make rotation-approval verification gas-free; set it to the default.
func (m Migrator) Migrate1to2(ctx sdk.Context) error {
	p, err := m.k.Params.Get(ctx)
	if err != nil {
		return err
	}
	if p.RotationSignatureGas == 0 {
		p.RotationSignatureGas = types.DefaultRotationSignatureGas
	}
	if err := p.Validate(); err != nil {
		return err
	}
	return m.k.Params.Set(ctx, p)
}
