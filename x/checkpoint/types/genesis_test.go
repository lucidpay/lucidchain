package types_test

import (
	"testing"

	"github.com/lucidpay/lucidchain/x/checkpoint/types"
	"github.com/stretchr/testify/require"
)

func TestGenesisState_Validate(t *testing.T) {
	tests := []struct {
		desc     string
		genState *types.GenesisState
		valid    bool
	}{
		{
			desc:     "default is valid",
			genState: types.DefaultGenesis(),
			valid:    true,
		},
		{
			desc:     "empty genesis state has no valid params",
			genState: &types.GenesisState{},
			valid:    false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			err := tc.genState.Validate()
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

// Genesis import applies the same anchor binding as the submit path (review
// follow-up): an anchored checkpoint whose state_root is not derived from its
// anchor, or whose record_count does not fit the tree, is refused.
func TestGenesisState_Validate_AnchorBinding(t *testing.T) {
	anchor := &types.ZkAnchor{
		Scheme:           types.ZkHashScheme_ZK_HASH_SCHEME_MIMC_BN254,
		RecordsRoot:      append(make([]byte, 31), 1),
		RecordsTreeDepth: 1,
	}
	hash := make([]byte, 32)
	hash[0] = 0xC0
	mk := func(stateRoot []byte, count uint64) *types.GenesisState {
		return &types.GenesisState{
			Params: types.DefaultParams(),
			Checkpoints: []types.Checkpoint{{
				SidechainId: "sc-1", LcSequence: 1, StateRoot: stateRoot,
				RecordCount: count, CheckpointHash: hash, ZkAnchor: anchor,
			}},
		}
	}
	arbitrary := make([]byte, 32)
	arbitrary[0] = 0xAA

	require.Error(t, mk(arbitrary, 2).Validate(), "unrelated state_root")
	require.Error(t, mk(types.DeriveStateRoot(anchor, 1000), 1000).Validate(), "count does not fit depth 1")
	require.NoError(t, mk(types.DeriveStateRoot(anchor, 2), 2).Validate(), "derived root, count fits")
}
