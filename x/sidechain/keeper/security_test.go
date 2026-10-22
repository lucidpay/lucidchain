package keeper_test

// Security regression tests from the external review of v0.4-Beta
// (16–17 Oct 2026). Finding N9: before this change the operator's single
// account key could replace the entire threshold-protected signer set in one
// message, with no signature from any outgoing signer and no delay — so a
// 2-of-3 ML-DSA threshold was only as strong as one classical secp256k1 key.

import (
	"testing"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	"github.com/stretchr/testify/require"

	"github.com/lucidpay/lucidchain/x/sidechain/types"
)

func attackerKeyAny(t *testing.T) (*ed25519.PrivKey, *codectypes.Any) {
	t.Helper()
	k := ed25519.GenPrivKey()
	a, err := codectypes.NewAnyWithValue(k.PubKey())
	require.NoError(t, err)
	return k, a
}

func rotationTo(f *fixture, keys []*codectypes.Any, threshold uint32) *types.MsgUpdateSidechainSigners {
	return &types.MsgUpdateSidechainSigners{
		Operator:           f.operatorS,
		Id:                 testID,
		SignerKeys:         keys,
		SignatureThreshold: threshold,
		NotAfterHeight:     uint64(f.ctx.BlockHeight()) + 100,
	}
}

// N9: the operator key alone must NOT be able to replace the signer set.
func TestN9_OperatorKeyAloneCannotReplaceSignerSet(t *testing.T) {
	f := newActiveFixture(t) // signers {0,1,2}, threshold 2, version 1
	_, attacker := attackerKeyAny(t)

	m := rotationTo(f, []*codectypes.Any{attacker}, 1)
	// No current_set_signatures at all.
	_, err := f.msgServer.UpdateSidechainSigners(f.ctx, m)
	require.ErrorIs(t, err, types.ErrRotationNotApproved)

	sc := f.get(t, testID)
	require.Equal(t, uint64(1), sc.SignerSetVersion, "nothing changed")
	require.Len(t, sc.SignerKeys, 3)
	require.Equal(t, uint32(2), sc.SignatureThreshold)
}

// N9: approval below the current threshold is refused; at threshold it passes.
func TestN9_RotationNeedsCurrentSetAtThreshold(t *testing.T) {
	f := newActiveFixture(t)
	_, attacker := attackerKeyAny(t)

	m := rotationTo(f, []*codectypes.Any{attacker}, 1)
	f.approve(t, m, 0) // one of three, threshold is two
	_, err := f.msgServer.UpdateSidechainSigners(f.ctx, m)
	require.ErrorIs(t, err, types.ErrRotationNotApproved)
	require.Equal(t, uint64(1), f.get(t, testID).SignerSetVersion)

	m = rotationTo(f, []*codectypes.Any{attacker}, 1)
	f.approve(t, m, 0, 2) // two of three
	resp, err := f.msgServer.UpdateSidechainSigners(f.ctx, m)
	require.NoError(t, err)
	require.Equal(t, uint64(2), resp.SignerSetVersion)
}

// N9: the attacker's own signature, or a signature from a key that is not in
// the current set, counts for nothing — only current signers can approve.
func TestN9_OnlyCurrentSignersCanApprove(t *testing.T) {
	f := newActiveFixture(t)
	attackerPriv, attacker := attackerKeyAny(t)

	m := rotationTo(f, []*codectypes.Any{attacker}, 1)
	sc := f.get(t, testID)
	bz, err := types.SignerRotationSignBytes(types.RotationSignDocFor(f.ctx.ChainID(), sc, m.SignerKeys, m.SignatureThreshold, m.NotAfterHeight))
	require.NoError(t, err)
	sig, err := attackerPriv.Sign(bz)
	require.NoError(t, err)

	// Claims to be current signer 0 and 1, but signed by the attacker key.
	m.CurrentSetSignatures = []types.SignerSetSignature{
		{SignerIndex: 0, Signature: sig},
		{SignerIndex: 1, Signature: sig},
	}
	_, err = f.msgServer.UpdateSidechainSigners(f.ctx, m)
	require.ErrorIs(t, err, types.ErrRotationNotApproved)

	// Same current signer twice does not reach threshold.
	m = rotationTo(f, []*codectypes.Any{attacker}, 1)
	f.approve(t, m, 1)
	dup := m.CurrentSetSignatures[0]
	m.CurrentSetSignatures = []types.SignerSetSignature{dup, dup}
	_, err = f.msgServer.UpdateSidechainSigners(f.ctx, m)
	require.ErrorIs(t, err, types.ErrRotationNotApproved)

	require.Equal(t, uint64(1), f.get(t, testID).SignerSetVersion)
}

// N9: an approval is bound to one rotation. Signatures gathered for rotation A
// cannot be reused to push through a different rotation B, and cannot be
// replayed after the version has moved on.
func TestN9_ApprovalIsBoundToOneRotation(t *testing.T) {
	f := newActiveFixture(t)
	_, keyA := attackerKeyAny(t)
	_, keyB := attackerKeyAny(t)

	// Approval for "rotate to {keyA}".
	approved := rotationTo(f, []*codectypes.Any{keyA}, 1)
	f.approve(t, approved, 0, 1)

	// Reuse those signatures for "rotate to {keyB}".
	m := rotationTo(f, []*codectypes.Any{keyB}, 1)
	m.CurrentSetSignatures = approved.CurrentSetSignatures
	_, err := f.msgServer.UpdateSidechainSigners(f.ctx, m)
	require.ErrorIs(t, err, types.ErrRotationNotApproved, "approval for A used for B")

	// The genuine rotation goes through…
	_, err = f.msgServer.UpdateSidechainSigners(f.ctx, approved)
	require.NoError(t, err)
	require.Equal(t, uint64(2), f.get(t, testID).SignerSetVersion)

	// …and replaying it against the new version is refused (version is in the doc,
	// and the old signers are no longer the current set).
	_, err = f.msgServer.UpdateSidechainSigners(f.ctx, approved)
	require.ErrorIs(t, err, types.ErrRotationNotApproved, "replay after rotation")
}

// N9b: while SUSPENDED the operator alone still cannot rotate, but the
// current set can — so compromised keys can be rotated out without waiting
// for a governance resume (independent review, finding 5).
func TestN9b_SuspendedRotationStillNeedsApproval(t *testing.T) {
	f := newActiveFixture(t)
	_, attacker := attackerKeyAny(t)
	f.setStatus(t, testID, types.SidechainStatus_SIDECHAIN_STATUS_SUSPENDED)

	m := rotationTo(f, []*codectypes.Any{attacker}, 1)
	_, err := f.msgServer.UpdateSidechainSigners(f.ctx, m)
	require.ErrorIs(t, err, types.ErrRotationNotApproved)
	require.Equal(t, uint64(1), f.get(t, testID).SignerSetVersion)

	m = rotationTo(f, []*codectypes.Any{attacker}, 1)
	f.approve(t, m, 0, 1)
	_, err = f.msgServer.UpdateSidechainSigners(f.ctx, m)
	require.NoError(t, err)
	require.Equal(t, uint64(2), f.get(t, testID).SignerSetVersion)
}

// Approvals expire: not_after_height is in the signed doc and enforced.
func TestN9_ApprovalExpires(t *testing.T) {
	f := newActiveFixture(t)
	_, attacker := attackerKeyAny(t)

	m := rotationTo(f, []*codectypes.Any{attacker}, 1)
	m.NotAfterHeight = uint64(f.ctx.BlockHeight()) // valid only at this height
	f.approve(t, m, 0, 1)

	later := f.ctx.WithBlockHeight(f.ctx.BlockHeight() + 1)
	_, err := f.msgServer.UpdateSidechainSigners(later, m)
	require.ErrorIs(t, err, types.ErrRotationNotApproved, "stale approval")

	// Changing the expiry after signing invalidates the signatures.
	m.NotAfterHeight += 1000
	_, err = f.msgServer.UpdateSidechainSigners(later, m)
	require.ErrorIs(t, err, types.ErrRotationNotApproved, "expiry is part of the signed doc")

	m2 := rotationTo(f, []*codectypes.Any{attacker}, 1)
	f.approve(t, m2, 0, 1)
	m2.NotAfterHeight = 0 // the helper fills a default, so clear it after signing
	_, err = f.msgServer.UpdateSidechainSigners(f.ctx, m2)
	require.ErrorIs(t, err, types.ErrInvalidRequest, "expiry is required")
}

// Version binding in isolation: rotate to the SAME set (keys and threshold
// unchanged) so nothing but signer_set_version differs, then replay.
func TestN9_SameSetReplayRefusedByVersionAlone(t *testing.T) {
	f := newActiveFixture(t)
	m := rotationTo(f, f.keyAnys(t, 0, 1, 2), 2)
	f.approve(t, m, 0, 1)
	_, err := f.msgServer.UpdateSidechainSigners(f.ctx, m)
	require.NoError(t, err)
	sc := f.get(t, testID)
	require.Equal(t, uint64(2), sc.SignerSetVersion)
	require.Equal(t, []byte{0b00000011}, sc.LastRotationApproverBitmap, "approvers 0 and 1 recorded in state")

	_, err = f.msgServer.UpdateSidechainSigners(f.ctx, m)
	require.ErrorIs(t, err, types.ErrRotationNotApproved)
	require.Equal(t, uint64(2), f.get(t, testID).SignerSetVersion)
}

// Governance activates the signer set it reviewed (independent review, finding 4).
func TestActivate_ExpectedSignerSetVersion(t *testing.T) {
	f := newFixture(t)
	f.register(t) // PENDING, version 1

	// Operator rotates (with approval) between proposal and execution.
	m := rotationTo(f, f.keyAnys(t, 0), 1)
	f.approve(t, m, 0, 1)
	_, err := f.msgServer.UpdateSidechainSigners(f.ctx, m)
	require.NoError(t, err)

	_, err = f.msgServer.ActivateSidechain(f.ctx, &types.MsgActivateSidechain{
		Authority: f.govS, Id: testID, ExpectedSignerSetVersion: 1,
	})
	require.ErrorIs(t, err, types.ErrInvalidSidechainState)
	require.Equal(t, types.SidechainStatus_SIDECHAIN_STATUS_PENDING, f.get(t, testID).Status)

	_, err = f.msgServer.ActivateSidechain(f.ctx, &types.MsgActivateSidechain{
		Authority: f.govS, Id: testID, ExpectedSignerSetVersion: 2,
	})
	require.NoError(t, err)
}
