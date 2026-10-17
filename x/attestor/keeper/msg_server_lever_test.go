package keeper_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lucidpay/lucidchain/x/attestor/types"
)

func (f *fixture) suspend(reason string) error {
	_, err := f.msgServer.SuspendAttestor(f.ctx, &types.MsgSuspendAttestor{
		Authority: f.govS, Id: testAttestor, Reason: reason,
	})
	return err
}

func (f *fixture) reinstate() error {
	_, err := f.msgServer.ReinstateAttestor(f.ctx, &types.MsgReinstateAttestor{
		Authority: f.govS, Id: testAttestor,
	})
	return err
}

func TestRaiseDispute_DoesNotSuspendAttestor(t *testing.T) {
	f := newActiveFixture(t)
	f.raiseDispute(t, f.attest(t, 1))

	a := f.getAttestor(t)
	require.Equal(t, types.AttestorStatus_ATTESTOR_STATUS_ACTIVE, a.Status)
	require.EqualValues(t, 1, a.OpenDisputes)

	f.attest(t, 2) // still able to attest the next checkpoint
}

func TestSuspendAndReinstateAttestor(t *testing.T) {
	f := newActiveFixture(t)

	_, err := f.msgServer.SuspendAttestor(f.ctx, &types.MsgSuspendAttestor{Authority: f.strangerS, Id: testAttestor})
	require.ErrorIs(t, err, types.ErrUnauthorized)

	require.NoError(t, f.suspend("dispute 19b6c8e1"))
	require.Equal(t, types.AttestorStatus_ATTESTOR_STATUS_SUSPENDED, f.getAttestor(t).Status)

	_, err = f.msgServer.SubmitAttestation(f.ctx, f.attestMsg(t, 1, 0, 1))
	require.ErrorIs(t, err, types.ErrAttestorNotActive)

	require.ErrorIs(t, f.suspend(""), types.ErrInvalidAttestorState) // already suspended

	_, err = f.msgServer.ReinstateAttestor(f.ctx, &types.MsgReinstateAttestor{Authority: f.strangerS, Id: testAttestor})
	require.ErrorIs(t, err, types.ErrUnauthorized)

	require.NoError(t, f.reinstate())
	require.Equal(t, types.AttestorStatus_ATTESTOR_STATUS_ACTIVE, f.getAttestor(t).Status)
	f.attest(t, 1)

	require.ErrorIs(t, f.reinstate(), types.ErrInvalidAttestorState) // already active
}

func TestSuspendAttestor_Validation(t *testing.T) {
	f := newActiveFixture(t)

	_, err := f.msgServer.SuspendAttestor(f.ctx, &types.MsgSuspendAttestor{Authority: f.govS, Id: "nope"})
	require.ErrorIs(t, err, types.ErrAttestorNotFound)

	require.ErrorIs(t, f.suspend(strings.Repeat("x", 257)), types.ErrLimitExceeded)
	require.Equal(t, types.AttestorStatus_ATTESTOR_STATUS_ACTIVE, f.getAttestor(t).Status)

	_, err = f.msgServer.ReinstateAttestor(f.ctx, &types.MsgReinstateAttestor{Authority: f.govS, Id: "nope"})
	require.ErrorIs(t, err, types.ErrAttestorNotFound)
}

func TestSuspendReinstate_WrongStates(t *testing.T) {
	for _, st := range []types.AttestorStatus{
		types.AttestorStatus_ATTESTOR_STATUS_PENDING,
		types.AttestorStatus_ATTESTOR_STATUS_SLASHED,
		types.AttestorStatus_ATTESTOR_STATUS_EXITED,
	} {
		t.Run(st.String(), func(t *testing.T) {
			f := newActiveFixture(t)
			f.setAttestorStatus(t, st)
			require.ErrorIs(t, f.suspend(""), types.ErrInvalidAttestorState)
			require.ErrorIs(t, f.reinstate(), types.ErrInvalidAttestorState)
			require.Equal(t, st, f.getAttestor(t).Status)
		})
	}
}

// A governance suspension must survive dispute resolution: only
// ReinstateAttestor lifts it.
func TestSuspensionSurvivesDisputeResolution(t *testing.T) {
	f := newActiveFixture(t)
	disputeID := f.raiseDispute(t, f.attest(t, 1))
	require.NoError(t, f.suspend("pending ruling"))

	f.mustResolve(t, disputeID, false)

	a := f.getAttestor(t)
	require.Zero(t, a.OpenDisputes)
	require.Equal(t, types.AttestorStatus_ATTESTOR_STATUS_SUSPENDED, a.Status)

	require.NoError(t, f.reinstate())
	require.Equal(t, types.AttestorStatus_ATTESTOR_STATUS_ACTIVE, f.getAttestor(t).Status)
}

func TestSuspendedAttestorCanStillExit(t *testing.T) {
	f := newActiveFixture(t)
	require.NoError(t, f.suspend(""))

	_, err := f.msgServer.InitiateAttestorExit(f.ctx, f.exitMsg())
	require.NoError(t, err)
	require.Equal(t, types.AttestorStatus_ATTESTOR_STATUS_EXITED, f.getAttestor(t).Status)
}
