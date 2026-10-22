package keeper_test

// Place this in x/checkpoint/keeper/zk_anchor_tier_test.go. It reuses the
// fixtures in keeper_test.go (newFixture, signedMsg, submit, requireNoCheckpoint,
// root, the mocks).
//
// NOTE: the existing signBytesFor helper does not put ZkAnchor into the sign
// doc, so it cannot sign messages that carry an anchor. anchoredMsg below does
// it itself, so keeper_test.go does not need to change. If you later add
// `ZkAnchor: msg.ZkAnchor` to signBytesFor, anchoredMsg can be replaced by it.

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/lucidpay/lucidchain/x/checkpoint/types"
	sidechaintypes "github.com/lucidpay/lucidchain/x/sidechain/types"
)

const (
	tierNotarized = sidechaintypes.AssuranceTier_ASSURANCE_TIER_NOTARIZED
	tierAttested  = sidechaintypes.AssuranceTier_ASSURANCE_TIER_ATTESTED
	tierProven    = sidechaintypes.AssuranceTier_ASSURANCE_TIER_PROVEN
)

func validAnchor() *types.ZkAnchor {
	return &types.ZkAnchor{
		Scheme:           types.ZkHashScheme_ZK_HASH_SCHEME_MIMC_BN254,
		RecordsRoot:      root(0x01), // top byte 0x01 is far below the BN254 modulus
		RecordsTreeDepth: 20,
	}
}

func (f *fixture) setTier(tier sidechaintypes.AssuranceTier) {
	sc := f.sidechains.sidechains[testSidechain]
	sc.Tier = tier
	f.sidechains.sidechains[testSidechain] = sc
}

// anchoredMsg builds a message carrying anchor (which may be nil) and signs a
// sign doc that includes it, with signers 0 and 1.
func (f *fixture) anchoredMsg(t *testing.T, seq uint64, prev []byte, anchor *types.ZkAnchor) *types.MsgSubmitCheckpoint {
	t.Helper()
	// With an anchor the state root is not free: it is derived from the anchor
	// and the record count (finding N5). Malformed anchors keep an arbitrary
	// root; they are rejected before the binding check runs.
	stateRoot := root(0x02)
	if anchor != nil && types.ValidateZkAnchor(anchor) == nil {
		stateRoot = types.DeriveStateRoot(anchor, 5)
	}
	msg := &types.MsgSubmitCheckpoint{
		Submitter:              f.submitterS,
		SidechainId:            testSidechain,
		LcSequence:             seq,
		StateRoot:              stateRoot,
		PreviousCheckpointHash: prev,
		RecordCount:            5,
		DataPointer:            "ipfs://example",
		SignerSetVersion:       1,
		ZkAnchor:               anchor,
	}
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
	for _, i := range []uint32{0, 1} {
		sig, err := f.signers[i].Sign(bz)
		require.NoError(t, err)
		msg.Signatures = append(msg.Signatures, types.Signature{SignerIndex: i, Signature: sig})
	}
	return msg
}

func TestSubmitCheckpoint_ZkAnchorByTier(t *testing.T) {
	cases := []struct {
		name   string
		tier   sidechaintypes.AssuranceTier
		anchor *types.ZkAnchor
		want   error // nil means accepted
	}{
		{"PROVEN without anchor is rejected", tierProven, nil, types.ErrZkAnchorRequired},
		{"PROVEN with anchor is accepted", tierProven, validAnchor(), nil},
		{"ATTESTED without anchor is accepted", tierAttested, nil, nil},
		{"ATTESTED with anchor is accepted", tierAttested, validAnchor(), nil},
		{"NOTARIZED without anchor is accepted", tierNotarized, nil, nil},
		{"NOTARIZED with anchor is accepted", tierNotarized, validAnchor(), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, 2, 3)
			f.setTier(tc.tier)
			// A non-zero fee makes a wrongly charged rejection visible.
			f.setParams(t, func(p *types.Params) {
				p.BaseFee = sdk.NewInt64Coin(sdk.DefaultBondDenom, 10)
			})

			resp, err := f.submit(f.anchoredMsg(t, 1, nil, tc.anchor))
			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)
				f.requireNoCheckpoint(t)
				require.Empty(t, f.bank.calls, "a rejected checkpoint must not be charged")
				return
			}
			require.NoError(t, err)

			cp, err := f.k.GetCheckpoint(f.ctx, testSidechain, 1)
			require.NoError(t, err)
			require.Equal(t, resp.CheckpointHash, cp.CheckpointHash)
			if tc.anchor == nil {
				require.Nil(t, cp.ZkAnchor)
				return
			}
			require.NotNil(t, cp.ZkAnchor)
			require.Equal(t, tc.anchor.Scheme, cp.ZkAnchor.Scheme)
			require.True(t, bytes.Equal(tc.anchor.RecordsRoot, cp.ZkAnchor.RecordsRoot))
			require.Equal(t, tc.anchor.RecordsTreeDepth, cp.ZkAnchor.RecordsTreeDepth)
		})
	}
}

// The anchor rule runs before signature verification, so a missing anchor
// costs no ML-DSA verification gas.
func TestSubmitCheckpoint_MissingAnchorRejectedBeforeSignatureVerification(t *testing.T) {
	f := newFixture(t, 2, 3)
	f.setTier(tierProven)

	before := f.ctx.GasMeter().GasConsumed()
	_, err := f.submit(f.anchoredMsg(t, 1, nil, nil))
	require.ErrorIs(t, err, types.ErrZkAnchorRequired)

	spent := f.ctx.GasMeter().GasConsumed() - before
	require.Less(t, spent, types.DefaultSignatureVerificationGas)
}

// Upgrading mid-stream: earlier checkpoints stay valid, and the requirement
// applies from the next submission on.
func TestSubmitCheckpoint_AnchorRequiredOnlyAfterUpgrade(t *testing.T) {
	f := newFixture(t, 2, 3)
	f.setTier(tierNotarized)

	resp1, err := f.submit(f.anchoredMsg(t, 1, nil, nil))
	require.NoError(t, err)

	f.setTier(tierProven)

	_, err = f.submit(f.anchoredMsg(t, 2, resp1.CheckpointHash, nil))
	require.ErrorIs(t, err, types.ErrZkAnchorRequired)
	seq, err := f.k.LatestSequence.Get(f.ctx, testSidechain)
	require.NoError(t, err)
	require.Equal(t, uint64(1), seq, "the rejected checkpoint wrote nothing")

	_, err = f.submit(f.anchoredMsg(t, 2, resp1.CheckpointHash, validAnchor()))
	require.NoError(t, err)

	cp1, err := f.k.GetCheckpoint(f.ctx, testSidechain, 1)
	require.NoError(t, err)
	require.Nil(t, cp1.ZkAnchor, "the pre-upgrade checkpoint is unchanged")
	cp2, err := f.k.GetCheckpoint(f.ctx, testSidechain, 2)
	require.NoError(t, err)
	require.NotNil(t, cp2.ZkAnchor)
}

// A malformed anchor must be rejected on the submit path at every tier, not
// only in genesis validation. If this test fails, validateSubmission does not
// call types.ValidateZkAnchor: add that call.
func TestSubmitCheckpoint_MalformedAnchorRejected(t *testing.T) {
	cases := map[string]*types.ZkAnchor{
		"empty non-nil anchor (scheme unspecified)": {},
		"short records_root": {
			Scheme:           types.ZkHashScheme_ZK_HASH_SCHEME_MIMC_BN254,
			RecordsRoot:      bytes.Repeat([]byte{0x01}, 31),
			RecordsTreeDepth: 20,
		},
		"non-canonical records_root (>= BN254 modulus)": {
			Scheme:           types.ZkHashScheme_ZK_HASH_SCHEME_MIMC_BN254,
			RecordsRoot:      bytes.Repeat([]byte{0xFF}, 32),
			RecordsTreeDepth: 20,
		},
		"depth zero": {
			Scheme:           types.ZkHashScheme_ZK_HASH_SCHEME_MIMC_BN254,
			RecordsRoot:      root(0x01),
			RecordsTreeDepth: 0,
		},
		"depth above the maximum": {
			Scheme:           types.ZkHashScheme_ZK_HASH_SCHEME_MIMC_BN254,
			RecordsRoot:      root(0x01),
			RecordsTreeDepth: types.MaxRecordsTreeDepth + 1,
		},
	}
	for _, tier := range []sidechaintypes.AssuranceTier{tierNotarized, tierProven} {
		for name, anchor := range cases {
			t.Run(tier.String()+"/"+name, func(t *testing.T) {
				f := newFixture(t, 2, 3)
				f.setTier(tier)

				_, err := f.submit(f.anchoredMsg(t, 1, nil, anchor))
				require.ErrorIs(t, err, types.ErrInvalidCheckpoint)
				f.requireNoCheckpoint(t)
			})
		}
	}
}
