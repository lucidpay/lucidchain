package types_test

import (
	"testing"

	"github.com/lucidpay/lucidchain/x/checkpoint/types"
	"github.com/stretchr/testify/require"
)

func TestSignBytesZkAnchor(t *testing.T) {
	base := &types.CheckpointSignDoc{
		ChainId: "c", SidechainId: "sc", LcSequence: 1,
		StateRoot: []byte{1}, RecordCount: 3, SignerSetVersion: 1,
	}
	noAnchor, err := types.CheckpointSignBytes(base)
	require.NoError(t, err)
	// require.Equal(t, "<hex from pre-change build>", hex.EncodeToString(noAnchor))

	root := make([]byte, 32)
	root[31] = 7
	withAnchor := *base
	withAnchor.ZkAnchor = &types.ZkAnchor{
		Scheme:           types.ZkHashScheme_ZK_HASH_SCHEME_MIMC_BN254,
		RecordsRoot:      root,
		RecordsTreeDepth: 20,
	}
	got, err := types.CheckpointSignBytes(&withAnchor)
	require.NoError(t, err)
	require.NotEqual(t, noAnchor, got)

	other := withAnchor
	other.ZkAnchor = &types.ZkAnchor{
		Scheme:           withAnchor.ZkAnchor.Scheme,
		RecordsRoot:      append(append([]byte(nil), root[:31]...), 8),
		RecordsTreeDepth: 20,
	}
	otherBytes, _ := types.CheckpointSignBytes(&other)
	require.NotEqual(t, types.CheckpointHash(got), types.CheckpointHash(otherBytes))
}
