package keeper

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"

	checkpointtypes "github.com/lucidpay/lucidchain/x/checkpoint/types"
	"github.com/lucidpay/lucidchain/x/proofs/types"
)

func TestExpectedInputs(t *testing.T) {
	root := make([]byte, 32)
	root[31] = 7
	cp := checkpointtypes.Checkpoint{
		RecordCount: 5,
		ZkAnchor: &checkpointtypes.ZkAnchor{
			Scheme:           checkpointtypes.ZkHashScheme_ZK_HASH_SCHEME_MIMC_BN254,
			RecordsRoot:      root,
			RecordsTreeDepth: 20,
		},
	}

	t.Run("every source accepted at registration yields a value", func(t *testing.T) {
		layout := []types.InputBinding{
			{Index: 2, Source: types.InputSource_INPUT_SOURCE_CHECKPOINT_RECORDS_ROOT},
			{Index: 3, Source: types.InputSource_INPUT_SOURCE_FIXED_VALUE, Value: []byte{0x00, 0x2a}},
			{Index: 6, Source: types.InputSource_INPUT_SOURCE_CHECKPOINT_RECORD_COUNT},
		}
		require.NoError(t, validateLayout("plonk-bn254", layout, 2, 7))

		got, err := Keeper{}.expectedInputs(layout, cp)
		require.NoError(t, err)
		require.Equal(t, []types.ExpectedInput{
			{Index: 2, Value: []byte{7}},
			{Index: 3, Value: big.NewInt(42).Bytes()},
			{Index: 6, Value: []byte{5}},
		}, got)
	})

	t.Run("records root needs a zk anchor", func(t *testing.T) {
		layout := []types.InputBinding{{Index: 2, Source: types.InputSource_INPUT_SOURCE_CHECKPOINT_RECORDS_ROOT}}
		_, err := Keeper{}.expectedInputs(layout, checkpointtypes.Checkpoint{RecordCount: 5})
		require.ErrorContains(t, err, "checkpoint has no zk anchor")
	})

	t.Run("unknown source", func(t *testing.T) {
		layout := []types.InputBinding{{Index: 2, Source: types.InputSource_INPUT_SOURCE_UNSPECIFIED}}
		_, err := Keeper{}.expectedInputs(layout, cp)
		require.ErrorIs(t, err, types.ErrInvalidRequest)
	})

	t.Run("fixed value must be below the field modulus", func(t *testing.T) {
		over := checkpointtypes.Bn254Fr.FillBytes(make([]byte, 32)) // == r
		layout := []types.InputBinding{{Index: 2, Source: types.InputSource_INPUT_SOURCE_FIXED_VALUE, Value: over}}
		require.ErrorIs(t, validateLayout("plonk-bn254", layout, 2, 7), types.ErrInvalidRequest)

		layout[0].Value = new(big.Int).Sub(checkpointtypes.Bn254Fr, big.NewInt(1)).FillBytes(make([]byte, 32))
		require.NoError(t, validateLayout("plonk-bn254", layout, 2, 7))
	})

	t.Run("rejects non-canonical root and wrong scheme", func(t *testing.T) {
		layout := []types.InputBinding{{Index: 2, Source: types.InputSource_INPUT_SOURCE_CHECKPOINT_RECORDS_ROOT}}

		bad := checkpointtypes.Checkpoint{ZkAnchor: &checkpointtypes.ZkAnchor{
			Scheme:      checkpointtypes.ZkHashScheme_ZK_HASH_SCHEME_MIMC_BN254,
			RecordsRoot: checkpointtypes.Bn254Fr.FillBytes(make([]byte, 32)),
		}}
		_, err := Keeper{}.expectedInputs(layout, bad)
		require.ErrorContains(t, err, "not a canonical field element")

		bad.ZkAnchor = &checkpointtypes.ZkAnchor{RecordsRoot: root} // scheme unspecified
		_, err = Keeper{}.expectedInputs(layout, bad)
		require.ErrorContains(t, err, "is not MiMC-BN254")
	})

	t.Run("fixed value must be below the field modulus", func(t *testing.T) {
		over := checkpointtypes.Bn254Fr.FillBytes(make([]byte, 32)) // == r
		layout := []types.InputBinding{{Index: 2, Source: types.InputSource_INPUT_SOURCE_FIXED_VALUE, Value: over}}
		require.ErrorIs(t, validateLayout("plonk-bn254", layout, 2, 7), types.ErrInvalidRequest)

		layout[0].Value = new(big.Int).Sub(checkpointtypes.Bn254Fr, big.NewInt(1)).FillBytes(make([]byte, 32))
		require.NoError(t, validateLayout("plonk-bn254", layout, 2, 7))
	})

	t.Run("rejects non-canonical root and wrong scheme", func(t *testing.T) {
		layout := []types.InputBinding{{Index: 2, Source: types.InputSource_INPUT_SOURCE_CHECKPOINT_RECORDS_ROOT}}

		bad := checkpointtypes.Checkpoint{ZkAnchor: &checkpointtypes.ZkAnchor{
			Scheme:      checkpointtypes.ZkHashScheme_ZK_HASH_SCHEME_MIMC_BN254,
			RecordsRoot: checkpointtypes.Bn254Fr.FillBytes(make([]byte, 32)),
		}}
		_, err := Keeper{}.expectedInputs(layout, bad)
		require.ErrorContains(t, err, "not a canonical field element")

		bad.ZkAnchor = &checkpointtypes.ZkAnchor{RecordsRoot: root} // scheme unspecified
		_, err = Keeper{}.expectedInputs(layout, bad)
		require.ErrorContains(t, err, "is not MiMC-BN254")
	})

}
