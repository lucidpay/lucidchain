package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"

	"github.com/lucidpay/lucidchain/x/attestor/types"
)

// Regression tests for attestor signer rotation (finding 4.3 of the v0.6-Beta
// summary): MsgUpdateAttestorSigners must be approved by the CURRENT signer
// set at its threshold, not by the operator account alone. Same rule as
// MsgUpdateSidechainSigners.

// rotationTo builds an unsigned rotation of the default attestor to the given
// fixture key positions and threshold.
func rotationTo(f *fixture, t *testing.T, order []int, threshold uint32) *types.MsgUpdateAttestorSigners {
	t.Helper()
	return &types.MsgUpdateAttestorSigners{
		Operator:           f.operatorS,
		Id:                 testAttestor,
		SignerKeys:         f.keyAnys(t, order),
		SignatureThreshold: threshold,
		NotAfterHeight:     uint64(f.ctx.BlockHeight()) + 100,
	}
}

// attackerSet is a signer set made only of a key the attacker controls.
func attackerSet(t *testing.T) (*ed25519.PrivKey, []*codectypes.Any) {
	t.Helper()
	priv := ed25519.GenPrivKey()
	a, err := codectypes.NewAnyWithValue(priv.PubKey())
	require.NoError(t, err)
	return priv, []*codectypes.Any{a}
}

func requireUnchanged(t *testing.T, f *fixture, before types.Attestor) {
	t.Helper()
	after := f.getAttestor(t)
	require.Equal(t, before.SignerSetVersion, after.SignerSetVersion)
	require.Equal(t, before.SignatureThreshold, after.SignatureThreshold)
	require.Equal(t, len(before.SignerKeys), len(after.SignerKeys))
	for i := range before.SignerKeys {
		require.Equal(t, before.SignerKeys[i].Value, after.SignerKeys[i].Value)
	}
	require.Empty(t, after.LastRotationApproverBitmap)
}

func TestAttestorRotation_OperatorAloneCannotReplaceSigners(t *testing.T) {
	f := newActiveFixture(t)
	before := f.getAttestor(t)

	_, keys := attackerSet(t)
	m := &types.MsgUpdateAttestorSigners{
		Operator:           f.operatorS,
		Id:                 testAttestor,
		SignerKeys:         keys,
		SignatureThreshold: 1,
		NotAfterHeight:     uint64(f.ctx.BlockHeight()) + 100,
	}
	_, err := f.msgServer.UpdateAttestorSigners(f.ctx, m)
	require.ErrorIs(t, err, types.ErrRotationNotApproved)
	requireUnchanged(t, f, before)

	// The attestor keeps signing with its real keys.
	f.attest(t, 1)
}

func TestAttestorRotation_ThresholdOfCurrentSet(t *testing.T) {
	f := newActiveFixture(t)
	before := f.getAttestor(t)
	require.Equal(t, uint32(2), before.SignatureThreshold)

	// One of two required approvals: refused.
	m := rotationTo(f, t, []int{2, 1}, 2)
	f.approveRotation(t, m, 0)
	_, err := f.msgServer.UpdateAttestorSigners(f.ctx, m)
	require.ErrorIs(t, err, types.ErrRotationNotApproved)
	requireUnchanged(t, f, before)

	// Two approvals (positions 0 and 2): accepted, version bumped, bitmap stored.
	f.approveRotation(t, m, 0, 2)
	resp, err := f.msgServer.UpdateAttestorSigners(f.ctx, m)
	require.NoError(t, err)
	require.Equal(t, uint64(2), resp.SignerSetVersion)

	a := f.getAttestor(t)
	require.Equal(t, uint64(2), a.SignerSetVersion)
	require.Equal(t, []byte{0b101}, a.LastRotationApproverBitmap)

	// Event carries the approver bitmap.
	found := false
	for _, ev := range f.ctx.EventManager().Events() {
		if ev.Type != "attestor_signers_updated" {
			continue
		}
		for _, at := range ev.Attributes {
			if at.Key == "approved_by_bitmap" && at.Value == "05" {
				found = true
			}
		}
	}
	require.True(t, found, "attestor_signers_updated event with approved_by_bitmap=05")
}

func TestAttestorRotation_BadApprovalsRejectWholeMessage(t *testing.T) {
	f := newActiveFixture(t)
	before := f.getAttestor(t)

	t.Run("signature from a key outside the current set", func(t *testing.T) {
		m := rotationTo(f, t, []int{0, 1}, 2)
		f.approveRotation(t, m)
		outsider := ed25519.GenPrivKey()
		bz, err := types.AttestorRotationSignBytes(types.AttestorRotationSignDocFor(
			f.ctx.ChainID(), before, m.SignerKeys, m.SignatureThreshold, m.NotAfterHeight))
		require.NoError(t, err)
		sig, err := outsider.Sign(bz)
		require.NoError(t, err)
		m.CurrentSetSignatures[1].Signature = sig
		_, err = f.msgServer.UpdateAttestorSigners(f.ctx, m)
		require.ErrorIs(t, err, types.ErrRotationNotApproved)
	})

	t.Run("duplicate signer index does not count twice", func(t *testing.T) {
		m := rotationTo(f, t, []int{0, 1}, 2)
		f.approveRotation(t, m, 0, 0)
		_, err := f.msgServer.UpdateAttestorSigners(f.ctx, m)
		require.ErrorIs(t, err, types.ErrRotationNotApproved)
	})

	t.Run("out of range signer index", func(t *testing.T) {
		m := rotationTo(f, t, []int{0, 1}, 2)
		f.approveRotation(t, m)
		m.CurrentSetSignatures[1].SignerIndex = 7
		_, err := f.msgServer.UpdateAttestorSigners(f.ctx, m)
		require.ErrorIs(t, err, types.ErrRotationNotApproved)
	})

	t.Run("approval signed without the domain prefix", func(t *testing.T) {
		m := rotationTo(f, t, []int{0, 1}, 2)
		doc := types.AttestorRotationSignDocFor(f.ctx.ChainID(), before, m.SignerKeys, m.SignatureThreshold, m.NotAfterHeight)
		raw, err := doc.Marshal()
		require.NoError(t, err)
		for _, p := range []int{0, 1} {
			sig, err := f.signers[f.keyOrder[p]].Sign(raw)
			require.NoError(t, err)
			m.CurrentSetSignatures = append(m.CurrentSetSignatures, types.Signature{SignerIndex: uint32(p), Signature: sig})
		}
		_, err = f.msgServer.UpdateAttestorSigners(f.ctx, m)
		require.ErrorIs(t, err, types.ErrRotationNotApproved)
	})

	requireUnchanged(t, f, before)
}

func TestAttestorRotation_ApprovalIsBoundToOneRotation(t *testing.T) {
	f := newActiveFixture(t)

	// Approval for set {0,1} cannot be used to install a different set.
	approved := rotationTo(f, t, []int{0, 1}, 2)
	f.approveRotation(t, approved)
	_, attackerKeys := attackerSet(t)
	swapped := *approved
	swapped.SignerKeys = attackerKeys
	swapped.SignatureThreshold = 1
	_, err := f.msgServer.UpdateAttestorSigners(f.ctx, &swapped)
	require.ErrorIs(t, err, types.ErrRotationNotApproved)

	// Nor with a different threshold.
	lowered := *approved
	lowered.SignatureThreshold = 1
	_, err = f.msgServer.UpdateAttestorSigners(f.ctx, &lowered)
	require.ErrorIs(t, err, types.ErrRotationNotApproved)

	// Used once: fine.
	_, err = f.msgServer.UpdateAttestorSigners(f.ctx, approved)
	require.NoError(t, err)
	require.Equal(t, uint64(2), f.getAttestor(t).SignerSetVersion)

	// Replayed after the version moved on: refused, even though keys 0 and 1
	// are still in the new set at the same positions.
	_, err = f.msgServer.UpdateAttestorSigners(f.ctx, approved)
	require.ErrorIs(t, err, types.ErrRotationNotApproved)
	require.Equal(t, uint64(2), f.getAttestor(t).SignerSetVersion)
}

func TestAttestorRotation_Expiry(t *testing.T) {
	f := newActiveFixture(t)
	before := f.getAttestor(t)
	h := uint64(f.ctx.BlockHeight())

	t.Run("not_after_height is required", func(t *testing.T) {
		m := rotationTo(f, t, []int{0, 1}, 2)
		f.approveRotation(t, m)
		m.NotAfterHeight = 0
		_, err := f.msgServer.UpdateAttestorSigners(f.ctx, m)
		require.ErrorIs(t, err, types.ErrInvalidRequest)
	})

	t.Run("expired approval is refused", func(t *testing.T) {
		m := rotationTo(f, t, []int{0, 1}, 2)
		m.NotAfterHeight = h - 1
		f.approveRotation(t, m)
		_, err := f.msgServer.UpdateAttestorSigners(f.ctx, m)
		require.ErrorIs(t, err, types.ErrRotationNotApproved)
	})

	t.Run("expiry is part of the signed doc", func(t *testing.T) {
		m := rotationTo(f, t, []int{0, 1}, 2)
		m.NotAfterHeight = h
		f.approveRotation(t, m)
		m.NotAfterHeight = h + 1000 // stretched after signing
		_, err := f.msgServer.UpdateAttestorSigners(f.ctx, m)
		require.ErrorIs(t, err, types.ErrRotationNotApproved)
	})

	requireUnchanged(t, f, before)

	t.Run("last valid height is accepted", func(t *testing.T) {
		m := rotationTo(f, t, []int{0, 1}, 2)
		m.NotAfterHeight = h
		f.approveRotation(t, m)
		_, err := f.msgServer.UpdateAttestorSigners(f.ctx, m)
		require.NoError(t, err)
	})
}

func TestAttestorRotation_SuspendedNeedsApprovalToo(t *testing.T) {
	f := newActiveFixture(t)
	f.setAttestorStatus(t, types.AttestorStatus_ATTESTOR_STATUS_SUSPENDED)
	before := f.getAttestor(t)

	_, keys := attackerSet(t)
	_, err := f.msgServer.UpdateAttestorSigners(f.ctx, &types.MsgUpdateAttestorSigners{
		Operator: f.operatorS, Id: testAttestor, SignerKeys: keys, SignatureThreshold: 1,
		NotAfterHeight: uint64(f.ctx.BlockHeight()) + 100,
	})
	require.ErrorIs(t, err, types.ErrRotationNotApproved)
	requireUnchanged(t, f, before)

	// With approval a suspended attestor can rotate compromised keys out.
	m := rotationTo(f, t, []int{1, 2}, 2)
	f.approveRotation(t, m)
	_, err = f.msgServer.UpdateAttestorSigners(f.ctx, m)
	require.NoError(t, err)
	require.Equal(t, types.AttestorStatus_ATTESTOR_STATUS_SUSPENDED, f.getAttestor(t).Status)
}

func TestAttestorRotation_GasPerApproval(t *testing.T) {
	f := newActiveFixture(t)
	p, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)

	m := rotationTo(f, t, []int{0, 1}, 2)
	f.approveRotation(t, m)
	before := f.ctx.GasMeter().GasConsumed()
	_, err = f.msgServer.UpdateAttestorSigners(f.ctx, m)
	require.NoError(t, err)
	require.GreaterOrEqual(t, f.ctx.GasMeter().GasConsumed()-before, 2*p.SignatureVerificationGas)

	// Below threshold fails before any verification gas is spent.
	m = rotationTo(f, t, []int{0, 1}, 2)
	f.keyOrder = []int{0, 1}
	f.approveRotation(t, m, 0)
	before = f.ctx.GasMeter().GasConsumed()
	_, err = f.msgServer.UpdateAttestorSigners(f.ctx, m)
	require.ErrorIs(t, err, types.ErrRotationNotApproved)
	require.Less(t, f.ctx.GasMeter().GasConsumed()-before, p.SignatureVerificationGas)
}

func TestAttestorRotation_ApproverBitmapSurvivesGenesis(t *testing.T) {
	f := newActiveFixture(t)
	m := rotationTo(f, t, []int{0, 1, 2}, 2)
	f.approveRotation(t, m, 1, 2)
	_, err := f.msgServer.UpdateAttestorSigners(f.ctx, m)
	require.NoError(t, err)

	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.NoError(t, exported.Validate())
	require.Equal(t, []byte{0b110}, exported.Attestors[0].LastRotationApproverBitmap)

	// A bitmap longer than the signer set can describe is invalid genesis.
	exported.Attestors[0].LastRotationApproverBitmap = []byte{0xff, 0x01}
	require.Error(t, exported.Validate())
}
