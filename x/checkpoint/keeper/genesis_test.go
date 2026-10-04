package keeper_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/lucidpay/lucidchain/x/checkpoint/types"
)

func TestGenesis(t *testing.T) {
	genesisState := types.GenesisState{
		Params: types.DefaultParams(),
	}

	f := initFixture(t)
	err := f.keeper.InitGenesis(f.ctx, genesisState)
	require.NoError(t, err)
	got, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.NotNil(t, got)

	require.EqualExportedValues(t, genesisState.Params, got.Params)
}

// initFixture keeps Ignite's scaffolded tests working.
func initFixture(t *testing.T) *fixture {
	return newFixture(t, 2, 3)
}

func h(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

// cp builds a checkpoint whose hash is h(hash) and whose link is h(prev);
// prev == 0 means "no previous hash".
func cp(id string, seq uint64, hash, prev byte) types.Checkpoint {
	c := types.Checkpoint{SidechainId: id, Sequence: seq, CheckpointHash: h(hash)}
	if prev != 0 {
		c.PreviousCheckpointHash = h(prev)
	}
	return c
}

func TestGenesisValidate(t *testing.T) {
	cases := []struct {
		name    string
		cps     []types.Checkpoint
		wantErr bool
	}{
		{"empty", nil, false},
		{
			"two chains, input out of order",
			[]types.Checkpoint{
				cp("b", 1, 0x21, 0), cp("a", 2, 0x12, 0x11), cp("b", 2, 0x22, 0x21), cp("a", 1, 0x11, 0),
			},
			false,
		},
		{"first checkpoint is not sequence 1", []types.Checkpoint{cp("a", 2, 0x12, 0x11)}, true},
		{"sequence 1 has a previous hash", []types.Checkpoint{cp("a", 1, 0x11, 0x99)}, true},
		{"gap in sequence", []types.Checkpoint{cp("a", 1, 0x11, 0), cp("a", 3, 0x13, 0x11)}, true},
		{"duplicate sequence", []types.Checkpoint{cp("a", 1, 0x11, 0), cp("a", 1, 0x11, 0)}, true},
		{"broken hash link", []types.Checkpoint{cp("a", 1, 0x11, 0), cp("a", 2, 0x12, 0x77)}, true},
		{"empty sidechain id", []types.Checkpoint{cp("", 1, 0x11, 0)}, true},
		{"sequence zero", []types.Checkpoint{cp("a", 0, 0x11, 0)}, true},
		{
			"short checkpoint hash",
			[]types.Checkpoint{{SidechainId: "a", Sequence: 1, CheckpointHash: []byte{1, 2, 3}}},
			true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gs := types.GenesisState{Params: types.DefaultParams(), Checkpoints: tc.cps}
			err := gs.Validate()
			if tc.wantErr {
				require.ErrorIs(t, err, types.ErrInvalidGenesis)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestGenesisValidate_InvalidParams(t *testing.T) {
	gs := *types.DefaultGenesis()
	gs.Params.SignatureVerificationGas = 0
	require.ErrorIs(t, gs.Validate(), types.ErrInvalidGenesis)
}

func TestDefaultGenesisIsValid(t *testing.T) {
	require.NoError(t, types.DefaultGenesis().Validate())
}

func TestParamsValidate(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(p *types.Params)
		ok     bool
	}{
		{"defaults", func(p *types.Params) {}, true},
		{"fee denoms differ", func(p *types.Params) { p.PerRecordFee = sdk.NewInt64Coin("other", 1) }, false},
		{"zero max records", func(p *types.Params) { p.MaxRecordsPerCheckpoint = 0 }, false},
		{"zero max data pointer", func(p *types.Params) { p.MaxDataPointerBytes = 0 }, false},
		{"zero max signatures", func(p *types.Params) { p.MaxSignaturesPerCheckpoint = 0 }, false},
		{"zero max signature bytes", func(p *types.Params) { p.MaxSignatureBytesPerCheckpoint = 0 }, false},
		{"zero verification gas", func(p *types.Params) { p.SignatureVerificationGas = 0 }, false},
		{"unset base fee", func(p *types.Params) { p.BaseFee = sdk.Coin{} }, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := types.DefaultParams()
			tc.mutate(&p)
			if tc.ok {
				require.NoError(t, p.Validate())
			} else {
				require.Error(t, p.Validate())
			}
		})
	}
}

func TestCheckpointSignBytes(t *testing.T) {
	doc := &types.CheckpointSignDoc{
		ChainId:          "lucidchain-1",
		SidechainId:      "sc-1",
		Sequence:         1,
		StateRoot:        h(0xAA),
		RecordCount:      3,
		SignerSetVersion: 1,
	}

	a, err := types.CheckpointSignBytes(doc)
	require.NoError(t, err)
	b, err := types.CheckpointSignBytes(doc)
	require.NoError(t, err)
	require.Equal(t, a, b, "sign bytes must be deterministic")
	require.True(t, bytes.HasPrefix(a, []byte(types.SignDocDomain)))

	// Every signed field must change the hash.
	mutations := map[string]func(d *types.CheckpointSignDoc){
		"chain_id":           func(d *types.CheckpointSignDoc) { d.ChainId = "other" },
		"sidechain_id":       func(d *types.CheckpointSignDoc) { d.SidechainId = "sc-2" },
		"sequence":           func(d *types.CheckpointSignDoc) { d.Sequence = 2 },
		"state_root":         func(d *types.CheckpointSignDoc) { d.StateRoot = h(0xBB) },
		"previous_hash":      func(d *types.CheckpointSignDoc) { d.PreviousCheckpointHash = h(0x01) },
		"record_count":       func(d *types.CheckpointSignDoc) { d.RecordCount = 4 },
		"signer_set_version": func(d *types.CheckpointSignDoc) { d.SignerSetVersion = 2 },
		"data_pointer":       func(d *types.CheckpointSignDoc) { d.DataPointer = "ipfs://x" },
	}
	base := types.CheckpointHash(a)
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			d := *doc
			mutate(&d)
			bz, err := types.CheckpointSignBytes(&d)
			require.NoError(t, err)
			require.NotEqual(t, base, types.CheckpointHash(bz))
		})
	}
}
