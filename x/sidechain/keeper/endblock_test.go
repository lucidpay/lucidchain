package keeper_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	sidechainmodule "github.com/lucidpay/lucidchain/x/sidechain/module"
	"github.com/lucidpay/lucidchain/x/sidechain/types"
)

// TestEndBlockReturnsExitBonds checks that the module's EndBlock runs
// ProcessExits, so an exited sidechain's bond is returned after the cooldown
// without anything else calling the keeper.
func TestEndBlockReturnsExitBonds(t *testing.T) {
	f := newActiveFixture(t)
	am := sidechainmodule.NewAppModule(nil, f.keeper, nil, nil)

	_, err := f.msgServer.InitiateSidechainExit(f.ctx, f.exitMsg())
	require.NoError(t, err)
	operatorBefore := f.bal(f.operator)

	f.advance(14*day - time.Second)
	require.NoError(t, am.EndBlock(f.ctx))
	require.Equal(t, operatorBefore, f.bal(f.operator), "bond must stay escrowed until the cooldown ends")
	require.Equal(t, 1, f.exitQueueLen(t))

	f.advance(time.Second)
	require.NoError(t, am.EndBlock(f.ctx))
	require.Equal(t, operatorBefore+tier1Bond, f.bal(f.operator))
	require.Zero(t, f.moduleBal())
	require.Zero(t, f.get(t, testID).Bond.Amount.Int64())
	require.Equal(t, types.SidechainStatus_SIDECHAIN_STATUS_EXITED, f.get(t, testID).Status)
	require.Zero(t, f.exitQueueLen(t))
	f.requireEscrowInvariant(t)
}
