package keeper_test

// Security regression tests from the external review of v0.4-Beta
// (16–17 Oct 2026). Finding N5: state_root (Tier 1) and zk_anchor.records_root
// (Tier 3) were two unrelated commitments under one signature set, and
// record_count was never checked against the tree depth. A proof verified
// against the anchor said nothing about the records under the state root.

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lucidpay/lucidchain/x/checkpoint/types"
)

// signWithAnchor signs over the full sign doc including zk_anchor (the
// existing helper signBytesFor omits it).
func (f *fixture) signWithAnchor(t *testing.T, msg *types.MsgSubmitCheckpoint, idx ...uint32) {
	t.Helper()
	bz, err := types.CheckpointSignBytes(&types.CheckpointSignDoc{
		ChainId:                f.ctx.ChainID(),
		SidechainId:            msg.SidechainId,
		LcSequence:             msg.LcSequence,
		StateRoot:              msg.StateRoot,
		PreviousCheckpointHash: msg.PreviousCheckpointHash,
		RecordCount:            msg.RecordCount,
		SignerSetVersion:       msg.SignerSetVersion,
		DataPointer:            msg.DataPointer,
		ZkAnchor:               msg.ZkAnchor,
	})
	require.NoError(t, err)
	msg.Signatures = nil
	for _, i := range idx {
		sig, err := f.signers[i].Sign(bz)
		require.NoError(t, err)
		msg.Signatures = append(msg.Signatures, types.Signature{SignerIndex: i, Signature: sig})
	}
}

func anchorOf(rootLastByte byte, depth uint32) *types.ZkAnchor {
	r := make([]byte, 32)
	r[31] = rootLastByte
	return &types.ZkAnchor{
		Scheme:           types.ZkHashScheme_ZK_HASH_SCHEME_MIMC_BN254,
		RecordsRoot:      r,
		RecordsTreeDepth: depth,
	}
}

func (f *fixture) boundMsg(seq uint64, prev []byte, a *types.ZkAnchor, stateRoot []byte, records uint64) *types.MsgSubmitCheckpoint {
	return &types.MsgSubmitCheckpoint{
		Submitter:              f.submitterS,
		SidechainId:            testSidechain,
		LcSequence:             seq,
		StateRoot:              stateRoot,
		PreviousCheckpointHash: prev,
		RecordCount:            records,
		DataPointer:            "ipfs://example",
		SignerSetVersion:       1,
		ZkAnchor:               a,
	}
}

// N5: a state_root unrelated to the anchor is rejected; the derived one is accepted.
func TestN5_StateRootMustBeDerivedFromAnchor(t *testing.T) {
	f := newFixture(t, 2, 3)
	a := anchorOf(1, 5)

	msg := f.boundMsg(1, nil, a, root(0xAA), 3) // arbitrary state root
	f.signWithAnchor(t, msg, 0, 1)
	_, err := f.submit(msg)
	require.ErrorIs(t, err, types.ErrInvalidCheckpoint)
	f.requireNoCheckpoint(t)

	msg = f.boundMsg(1, nil, a, types.DeriveStateRoot(a, 3), 3)
	f.signWithAnchor(t, msg, 0, 1)
	_, err = f.submit(msg)
	require.NoError(t, err)

	cp, err := f.k.GetCheckpoint(f.ctx, testSidechain, 1)
	require.NoError(t, err)
	require.Equal(t, types.DeriveStateRoot(a, 3), cp.StateRoot)
	require.Equal(t, a.RecordsRoot, cp.ZkAnchor.RecordsRoot)
}

// N5: the derived root binds the count too — the right root for 3 records is
// the wrong root for 4, so a signed checkpoint cannot be re-described.
func TestN5_DerivedRootBindsRecordCount(t *testing.T) {
	f := newFixture(t, 2, 3)
	a := anchorOf(1, 5)

	msg := f.boundMsg(1, nil, a, types.DeriveStateRoot(a, 3), 4)
	f.signWithAnchor(t, msg, 0, 1)
	_, err := f.submit(msg)
	require.ErrorIs(t, err, types.ErrInvalidCheckpoint)
	f.requireNoCheckpoint(t)
}

// N5: record_count must fit in a tree of records_tree_depth.
func TestN5_RecordCountMustFitTreeDepth(t *testing.T) {
	f := newFixture(t, 2, 3)
	a := anchorOf(1, 1) // two leaves

	msg := f.boundMsg(1, nil, a, types.DeriveStateRoot(a, 1000), 1000)
	f.signWithAnchor(t, msg, 0, 1)
	_, err := f.submit(msg)
	require.ErrorIs(t, err, types.ErrInvalidCheckpoint)
	f.requireNoCheckpoint(t)

	// Exactly full is fine.
	msg = f.boundMsg(1, nil, a, types.DeriveStateRoot(a, 2), 2)
	f.signWithAnchor(t, msg, 0, 1)
	_, err = f.submit(msg)
	require.NoError(t, err)
}

// Checkpoints without an anchor keep the operator-defined state root (Tier 1
// only), so the change is backwards compatible for existing sidechains.
func TestN5_NoAnchorKeepsFreeStateRoot(t *testing.T) {
	f := newFixture(t, 2, 3)
	_, err := f.submit(f.signedMsg(t, 1, nil, root(0xAA), 10, 0, 1))
	require.NoError(t, err)
}

func TestDeriveStateRoot_Vector(t *testing.T) {
	// Pin the construction so off-chain tooling (Go, Python, shell) can be
	// checked against one vector. Any change here is a protocol change.
	a := anchorOf(1, 5)
	got := types.DeriveStateRoot(a, 3)
	require.Equal(t,
		"62daea805d87aec991f46a0b887bdb9337bd53a92a565604831dda49cc3ec137",
		hex.EncodeToString(got),
		"pinned vector: scheme=1 root=0x00..01 depth=5 count=3 (readme §3.3, tools/signcheckpoint)")
	require.NotEqual(t, got, types.DeriveStateRoot(a, 4))
	require.NotEqual(t, got, types.DeriveStateRoot(anchorOf(1, 6), 3))
	require.NotEqual(t, got, types.DeriveStateRoot(anchorOf(2, 5), 3))
}

// v0.5 makes the anchor mandatory for PROVEN sidechains; the binding rule is
// what makes that requirement mean something. A PROVEN checkpoint whose
// state_root is not derived from its anchor is refused like any other.
func TestN5_ProvenTierRequiresBoundAnchor(t *testing.T) {
	f := newFixture(t, 2, 3)
	f.setTier(tierProven)
	a := anchorOf(1, 1) // two leaves

	msg := f.boundMsg(1, nil, a, root(0xAA), 1000) // unrelated root, count does not fit
	f.signWithAnchor(t, msg, 0, 1)
	_, err := f.submit(msg)
	require.ErrorIs(t, err, types.ErrInvalidCheckpoint)
	f.requireNoCheckpoint(t)

	msg = f.boundMsg(1, nil, a, types.DeriveStateRoot(a, 2), 2)
	f.signWithAnchor(t, msg, 0, 1)
	_, err = f.submit(msg)
	require.NoError(t, err)
}
