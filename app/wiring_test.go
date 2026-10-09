package app

import (
	"testing"

	"cosmossdk.io/log/v2"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/client/flags"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	"github.com/stretchr/testify/require"

	proofsverifiers "github.com/lucidpay/lucidchain/x/proofs/verifiers"
)

// TestProofVerifiersWired builds the real app and checks that every proof
// system in verifiers.Default() reaches the proofs keeper through depinject.
// Keeper unit tests construct the keeper directly and cannot catch this.
func TestProofVerifiersWired(t *testing.T) {
	appOptions := make(simtestutil.AppOptionsMap, 0)
	appOptions[flags.FlagHome] = t.TempDir()

	app := New(log.NewNopLogger(), dbm.NewMemDB(), true, appOptions)

	for id := range proofsverifiers.Default() {
		require.True(t, app.ProofsKeeper.HasImplementation(id), "proof system %q is not wired into the app", id)
	}
}
