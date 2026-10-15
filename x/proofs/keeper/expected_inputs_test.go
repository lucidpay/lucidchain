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
		ZkAnchor:    &checkpointtypes.ZkAnchor{RecordsRoot: root},
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
}
