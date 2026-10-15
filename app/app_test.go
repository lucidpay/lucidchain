package app

import (
	"testing"

	"github.com/stretchr/testify/require"

	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	checkpointmoduletypes "github.com/lucidpay/lucidchain/x/checkpoint/types"
	proofsmoduletypes "github.com/lucidpay/lucidchain/x/proofs/types"
	sidechainmoduletypes "github.com/lucidpay/lucidchain/x/sidechain/types"
)

func TestSidechainModuleAccountIsBurnerOnly(t *testing.T) {
	require.Equal(t,
		[]string{authtypes.Burner},
		GetMaccPerms()[sidechainmoduletypes.ModuleName],
	)
}

func TestChainModuleAccountPermissions(t *testing.T) {
	perms := GetMaccPerms()
	require.Equal(t, []string{authtypes.Burner}, perms[sidechainmoduletypes.ModuleName])
	require.Empty(t, perms[checkpointmoduletypes.ModuleName])
	require.Empty(t, perms[proofsmoduletypes.ModuleName])
	// attestor intentionally not asserted until the module is finished
}
