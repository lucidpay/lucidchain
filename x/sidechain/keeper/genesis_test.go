package keeper_test

import (
	"testing"

	"github.com/lucidpay/lucidchain/x/sidechain/types"

	"github.com/stretchr/testify/require"
)

func TestGenesis(t *testing.T) {
	genesisState := types.GenesisState{
		Params: types.DefaultParams(),
	}

	f := initFixture(t)
	err := f.keeper.InitGenesis(f.ctx, genesisState)
	require.NoError(t, err)
	got, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.NotNil(t, got)

	require.EqualExportedValues(t, genesisState.Params, got.Params)
}

/*
func TestGenesisValidate_SidechainRules(t *testing.T) {
	cases := map[string]func(sc *types.Sidechain){
		"unknown status":            func(sc *types.Sidechain) { sc.Status = 99 },
		"active without activated":  func(sc *types.Sidechain) { sc.Status = types.SidechainStatus_SIDECHAIN_STATUS_ACTIVE; sc.ActivatedAt = nil },
		"pending with activated_at": func(sc *types.Sidechain) { now := time.Now(); sc.ActivatedAt = &now },
		"exited without timestamps": func(sc *types.Sidechain) { sc.Status = types.SidechainStatus_SIDECHAIN_STATUS_EXITED },
		"exit timestamps on active": func(sc *types.Sidechain) { now := time.Now(); sc.ExitRequestedAt, sc.BondReturnAt = &now, &now },
		"slashed_at without reason": func(sc *types.Sidechain) { now := time.Now(); sc.SlashedAt = &now },
		"checkpoint seq no time":    func(sc *types.Sidechain) { sc.LastCheckpointSequence = 1; sc.LastCheckpointHash = []byte{1} },
		"negative height":           func(sc *types.Sidechain) { sc.LastCheckpointHeight = -1 },
		"duplicate signer key":      func(sc *types.Sidechain) { sc.SignerKeys = append(sc.SignerKeys, sc.SignerKeys[0]) },
		"empty signer key":          func(sc *types.Sidechain) { sc.SignerKeys[0] = &codectypes.Any{} },
		"zero registered_at":        func(sc *types.Sidechain) { sc.RegisteredAt = time.Time{} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			sc := validPendingSidechain() // your existing builder, or a small one for this file
			mutate(&sc)
			gs := types.DefaultGenesis()
			gs.Sidechains = []types.Sidechain{sc}
			require.ErrorIs(t, gs.Validate(), types.ErrInvalidGenesis)
		})
	}
}
*/
