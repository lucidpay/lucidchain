package keeper_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
	"time"

	"cosmossdk.io/collections"
	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/codec"
	addresscodec "github.com/cosmos/cosmos-sdk/codec/address"
	codectestutil "github.com/cosmos/cosmos-sdk/codec/testutil"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	"github.com/cosmos/cosmos-sdk/runtime"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/lucidpay/lucidchain/x/checkpoint/keeper"
	"github.com/lucidpay/lucidchain/x/checkpoint/types"
	sidechaintypes "github.com/lucidpay/lucidchain/x/sidechain/types"
)

// NOTE: the tests use ed25519 signer keys. The keeper is key-type agnostic (it
// only calls cryptotypes.PubKey.VerifySignature), so this exercises the same
// code path as ML-DSA-65. Add an ML-DSA-65 case once you have confirmed the
// key package path in your SDK version.

const (
	testChainID   = "lucidchain-test-1"
	testSidechain = "sc-1"
	testHeight    = int64(100)
)

var testBlockTime = time.Unix(1_700_000_000, 0).UTC()

// ---------------------------------------------------------------------------
// mocks
// ---------------------------------------------------------------------------

type recordCall struct {
	id     string
	seq    uint64
	height int64
	hash   []byte
}

type mockSidechainKeeper struct {
	sidechains map[string]sidechaintypes.Sidechain
	recorded   []recordCall
	recordErr  error
}

var _ types.SidechainKeeper = (*mockSidechainKeeper)(nil)

func (m *mockSidechainKeeper) GetSidechain(_ context.Context, id string) (sidechaintypes.Sidechain, error) {
	sc, ok := m.sidechains[id]
	if !ok {
		return sidechaintypes.Sidechain{}, fmt.Errorf("%w: sidechain %s", collections.ErrNotFound, id)
	}
	return sc, nil
}

func (m *mockSidechainKeeper) RecordCheckpoint(_ context.Context, id string, seq uint64, height int64, hash []byte) error {
	if m.recordErr != nil {
		return m.recordErr
	}
	m.recorded = append(m.recorded, recordCall{id, seq, height, hash})
	return nil
}

type bankCall struct {
	from   sdk.AccAddress
	module string
	amt    sdk.Coins
}

type mockBank struct {
	calls []bankCall
	err   error
}

var _ types.BankKeeper = (*mockBank)(nil)

// SpendableCoins satisfies the scaffolded BankKeeper interface (used by the
// module's AppModule, not by the keeper).
func (m *mockBank) SpendableCoins(_ context.Context, _ sdk.AccAddress) sdk.Coins {
	return nil
}

func (m *mockBank) SendCoinsFromAccountToModule(_ context.Context, from sdk.AccAddress, module string, amt sdk.Coins) error {
	if m.err != nil {
		return m.err
	}
	m.calls = append(m.calls, bankCall{from, module, amt})
	return nil
}

// ---------------------------------------------------------------------------
// fixture
// ---------------------------------------------------------------------------

type fixture struct {
	ctx        sdk.Context
	k          keeper.Keeper
	msgServer  types.MsgServer
	sidechains *mockSidechainKeeper
	bank       *mockBank
	signers    []*ed25519.PrivKey
	submitter  sdk.AccAddress
	submitterS string
}

// newFixture builds a keeper with one ACTIVE sidechain ("sc-1") that has
// nSigners registered keys and the given signature threshold.
func newFixture(t *testing.T, threshold uint32, nSigners int) *fixture {
	t.Helper()

	registry := codectestutil.CodecOptions{}.NewInterfaceRegistry()
	cryptocodec.RegisterInterfaces(registry)
	cdc := codec.NewProtoCodec(registry)
	addrCodec := addresscodec.NewBech32Codec("cosmos")

	key := storetypes.NewKVStoreKey(types.StoreKey)
	tkey := storetypes.NewTransientStoreKey("transient_test")
	ctx := testutil.DefaultContextWithDB(t, key, tkey).Ctx.
		WithChainID(testChainID).
		WithBlockHeight(testHeight).
		WithBlockTime(testBlockTime)

	signers := make([]*ed25519.PrivKey, nSigners)
	anys := make([]*codectypes.Any, nSigners)
	for i := range signers {
		signers[i] = ed25519.GenPrivKey()
		a, err := codectypes.NewAnyWithValue(signers[i].PubKey())
		require.NoError(t, err)
		anys[i] = a
	}

	sc := &mockSidechainKeeper{sidechains: map[string]sidechaintypes.Sidechain{
		testSidechain: {
			Id:                 testSidechain,
			Status:             sidechaintypes.SidechainStatus_SIDECHAIN_STATUS_ACTIVE,
			SignerKeys:         anys,
			SignatureThreshold: threshold,
			SignerSetVersion:   1,
		},
	}}
	bank := &mockBank{}

	authority := authtypes.NewModuleAddress(types.GovModuleName)
	k := keeper.NewKeeper(runtime.NewKVStoreService(key), cdc, addrCodec, authority, sc, bank)
	require.NoError(t, k.SetParams(ctx, types.DefaultParams()))

	submitter := sdk.AccAddress(bytes.Repeat([]byte{7}, 20))
	submitterS, err := addrCodec.BytesToString(submitter)
	require.NoError(t, err)

	return &fixture{
		ctx:        ctx,
		k:          k,
		msgServer:  keeper.NewMsgServerImpl(k),
		sidechains: sc,
		bank:       bank,
		signers:    signers,
		submitter:  submitter,
		submitterS: submitterS,
	}
}

func (f *fixture) setParams(t *testing.T, mutate func(p *types.Params)) {
	t.Helper()
	p, err := f.k.GetParams(f.ctx)
	require.NoError(t, err)
	mutate(&p)
	require.NoError(t, f.k.SetParams(f.ctx, p))
}

func signBytesFor(t *testing.T, msg *types.MsgSubmitCheckpoint, chainID string) []byte {
	t.Helper()
	bz, err := types.CheckpointSignBytes(&types.CheckpointSignDoc{
		ChainId:                chainID,
		SidechainId:            msg.SidechainId,
		Sequence:               msg.Sequence,
		StateRoot:              msg.StateRoot,
		PreviousCheckpointHash: msg.PreviousCheckpointHash,
		RecordCount:            msg.RecordCount,
		SignerSetVersion:       msg.SignerSetVersion,
		DataPointer:            msg.DataPointer,
	})
	require.NoError(t, err)
	return bz
}

// sign replaces msg.Signatures with signatures from the given signer indices,
// in the order given, over the sign bytes for chainID.
func (f *fixture) sign(t *testing.T, msg *types.MsgSubmitCheckpoint, chainID string, idx ...uint32) {
	t.Helper()
	bz := signBytesFor(t, msg, chainID)
	msg.Signatures = nil
	for _, i := range idx {
		sig, err := f.signers[i].Sign(bz)
		require.NoError(t, err)
		msg.Signatures = append(msg.Signatures, types.Signature{SignerIndex: i, Signature: sig})
	}
}

func (f *fixture) signedMsg(t *testing.T, seq uint64, prev, root []byte, records uint64, idx ...uint32) *types.MsgSubmitCheckpoint {
	t.Helper()
	msg := &types.MsgSubmitCheckpoint{
		Submitter:              f.submitterS,
		SidechainId:            testSidechain,
		Sequence:               seq,
		StateRoot:              root,
		PreviousCheckpointHash: prev,
		RecordCount:            records,
		DataPointer:            "ipfs://example",
		SignerSetVersion:       1,
	}
	f.sign(t, msg, f.ctx.ChainID(), idx...)
	return msg
}

func (f *fixture) submit(msg *types.MsgSubmitCheckpoint) (*types.MsgSubmitCheckpointResponse, error) {
	return f.msgServer.SubmitCheckpoint(f.ctx, msg)
}

func (f *fixture) requireNoCheckpoint(t *testing.T) {
	t.Helper()
	_, err := f.k.LatestSequence.Get(f.ctx, testSidechain)
	require.ErrorIs(t, err, collections.ErrNotFound)
	require.Empty(t, f.sidechains.recorded)
}

func root(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

// ---------------------------------------------------------------------------
// success path
// ---------------------------------------------------------------------------

func TestSubmitCheckpoint_Success(t *testing.T) {
	f := newFixture(t, 2, 3)

	// Signatures deliberately submitted out of order (2 before 0).
	msg := f.signedMsg(t, 1, nil, root(0xAA), 10, 2, 0)
	sigByIdx := map[uint32][]byte{}
	for _, s := range msg.Signatures {
		sigByIdx[s.SignerIndex] = s.Signature
	}

	resp, err := f.submit(msg)
	require.NoError(t, err)

	wantHash := types.CheckpointHash(signBytesFor(t, msg, testChainID))
	require.Equal(t, wantHash, resp.CheckpointHash)
	require.Equal(t, uint64(testHeight), resp.FinalizedHeight)

	cp, err := f.k.GetCheckpoint(f.ctx, testSidechain, 1)
	require.NoError(t, err)
	require.Equal(t, testSidechain, cp.SidechainId)
	require.Equal(t, uint64(1), cp.Sequence)
	require.Equal(t, root(0xAA), cp.StateRoot)
	require.Empty(t, cp.PreviousCheckpointHash)
	require.Equal(t, uint64(10), cp.RecordCount)
	require.Equal(t, wantHash, cp.CheckpointHash)
	require.Equal(t, uint64(1), cp.SignerSetVersion)
	require.Equal(t, "ipfs://example", cp.DataPointer)
	require.Equal(t, testHeight, cp.FinalizedHeight)
	require.True(t, cp.FinalizedAt.Equal(testBlockTime))

	// Signers 0 and 2 => bits 0 and 2 of byte 0.
	require.Equal(t, []byte{0b00000101}, cp.SignerBitmap)

	// Digest is over the signatures ordered by signer_index (0, then 2).
	h := sha256.New()
	h.Write(sigByIdx[0])
	h.Write(sigByIdx[2])
	require.Equal(t, h.Sum(nil), cp.SignaturesDigest)

	latest, err := f.k.GetLatestCheckpoint(f.ctx, testSidechain)
	require.NoError(t, err)
	require.Equal(t, cp, latest)

	require.Equal(t, []recordCall{{testSidechain, 1, testHeight, wantHash}}, f.sidechains.recorded)
}

// ---------------------------------------------------------------------------
// chaining
// ---------------------------------------------------------------------------

func TestSubmitCheckpoint_Chaining(t *testing.T) {
	f := newFixture(t, 2, 3)

	resp1, err := f.submit(f.signedMsg(t, 1, nil, root(0x01), 5, 0, 1))
	require.NoError(t, err)

	cases := []struct {
		name string
		seq  uint64
		prev []byte
		want error
	}{
		{"replay of sequence 1", 1, nil, types.ErrInvalidSequence},
		{"skipped sequence", 3, resp1.CheckpointHash, types.ErrInvalidSequence},
		{"wrong previous hash", 2, root(0xEE), types.ErrInvalidPreviousHash},
		{"empty previous hash after sequence 1", 2, nil, types.ErrInvalidPreviousHash},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.submit(f.signedMsg(t, tc.seq, tc.prev, root(0x02), 5, 0, 1))
			require.ErrorIs(t, err, tc.want)
		})
	}

	// Rejections wrote nothing: latest is still sequence 1.
	seq, err := f.k.LatestSequence.Get(f.ctx, testSidechain)
	require.NoError(t, err)
	require.Equal(t, uint64(1), seq)

	resp2, err := f.submit(f.signedMsg(t, 2, resp1.CheckpointHash, root(0x02), 5, 0, 1))
	require.NoError(t, err)

	cp2, err := f.k.GetCheckpoint(f.ctx, testSidechain, 2)
	require.NoError(t, err)
	require.Equal(t, resp1.CheckpointHash, cp2.PreviousCheckpointHash)
	require.Equal(t, resp2.CheckpointHash, cp2.CheckpointHash)
}

func TestSubmitCheckpoint_FirstCheckpointRejectsPreviousHash(t *testing.T) {
	f := newFixture(t, 2, 3)

	_, err := f.submit(f.signedMsg(t, 1, root(0xEE), root(0x01), 5, 0, 1))
	require.ErrorIs(t, err, types.ErrInvalidPreviousHash)
	f.requireNoCheckpoint(t)
}

// ---------------------------------------------------------------------------
// signatures
// ---------------------------------------------------------------------------

func TestSubmitCheckpoint_ThresholdNotMet(t *testing.T) {
	f := newFixture(t, 2, 3)

	before := f.ctx.GasMeter().GasConsumed()
	_, err := f.submit(f.signedMsg(t, 1, nil, root(0x01), 5, 0))
	require.ErrorIs(t, err, types.ErrThresholdNotMet)
	f.requireNoCheckpoint(t)

	// The threshold check runs before any signature is verified, so no
	// verification gas should have been charged.
	spent := f.ctx.GasMeter().GasConsumed() - before
	require.Less(t, spent, types.DefaultSignatureVerificationGas)
}

func TestSubmitCheckpoint_DuplicateSignatureCannotMeetThreshold(t *testing.T) {
	f := newFixture(t, 2, 3)

	// Two signatures, but both from signer 1: passes the count check, must
	// still be rejected as a duplicate.
	_, err := f.submit(f.signedMsg(t, 1, nil, root(0x01), 5, 1, 1))
	require.ErrorIs(t, err, types.ErrInvalidSignature)
	require.ErrorContains(t, err, "duplicate")
	f.requireNoCheckpoint(t)
}

func TestSubmitCheckpoint_InvalidSignatures(t *testing.T) {
	t.Run("tampered state root after signing", func(t *testing.T) {
		f := newFixture(t, 2, 3)
		msg := f.signedMsg(t, 1, nil, root(0x01), 5, 0, 1)
		msg.StateRoot = root(0x99)

		_, err := f.submit(msg)
		require.ErrorIs(t, err, types.ErrInvalidSignature)
		f.requireNoCheckpoint(t)
	})

	t.Run("signature from an unregistered key", func(t *testing.T) {
		f := newFixture(t, 2, 3)
		msg := f.signedMsg(t, 1, nil, root(0x01), 5, 0, 1)

		rogue := ed25519.GenPrivKey()
		sig, err := rogue.Sign(signBytesFor(t, msg, testChainID))
		require.NoError(t, err)
		msg.Signatures[0].Signature = sig

		_, err = f.submit(msg)
		require.ErrorIs(t, err, types.ErrInvalidSignature)
		f.requireNoCheckpoint(t)
	})

	t.Run("signer index out of range", func(t *testing.T) {
		f := newFixture(t, 2, 3)
		msg := f.signedMsg(t, 1, nil, root(0x01), 5, 0, 1)
		msg.Signatures = append(msg.Signatures, types.Signature{SignerIndex: 5, Signature: []byte("x")})

		_, err := f.submit(msg)
		require.ErrorIs(t, err, types.ErrInvalidSignature)
		require.ErrorContains(t, err, "out of range")
		f.requireNoCheckpoint(t)
	})

	t.Run("signatures made for another chain id", func(t *testing.T) {
		f := newFixture(t, 2, 3)
		msg := f.signedMsg(t, 1, nil, root(0x01), 5, 0, 1)
		f.sign(t, msg, "some-other-chain-1", 0, 1)

		_, err := f.submit(msg)
		require.ErrorIs(t, err, types.ErrInvalidSignature)
		f.requireNoCheckpoint(t)
	})
}

func TestSubmitCheckpoint_ChargesVerificationGas(t *testing.T) {
	f := newFixture(t, 2, 3)

	before := f.ctx.GasMeter().GasConsumed()
	_, err := f.submit(f.signedMsg(t, 1, nil, root(0x01), 5, 0, 1))
	require.NoError(t, err)

	spent := f.ctx.GasMeter().GasConsumed() - before
	require.GreaterOrEqual(t, spent, 2*types.DefaultSignatureVerificationGas)
}

// ---------------------------------------------------------------------------
// sidechain state
// ---------------------------------------------------------------------------

func TestSubmitCheckpoint_SidechainState(t *testing.T) {
	t.Run("unknown sidechain", func(t *testing.T) {
		f := newFixture(t, 2, 3)
		msg := f.signedMsg(t, 1, nil, root(0x01), 5, 0, 1)
		msg.SidechainId = "does-not-exist"

		_, err := f.submit(msg)
		require.ErrorIs(t, err, types.ErrSidechainNotFound)
	})

	for _, st := range []sidechaintypes.SidechainStatus{
		sidechaintypes.SidechainStatus_SIDECHAIN_STATUS_PENDING,
		sidechaintypes.SidechainStatus_SIDECHAIN_STATUS_SUSPENDED,
		sidechaintypes.SidechainStatus_SIDECHAIN_STATUS_SLASHED,
		sidechaintypes.SidechainStatus_SIDECHAIN_STATUS_EXITED,
	} {
		t.Run("status "+st.String(), func(t *testing.T) {
			f := newFixture(t, 2, 3)
			sc := f.sidechains.sidechains[testSidechain]
			sc.Status = st
			f.sidechains.sidechains[testSidechain] = sc

			_, err := f.submit(f.signedMsg(t, 1, nil, root(0x01), 5, 0, 1))
			require.ErrorIs(t, err, types.ErrSidechainNotActive)
			f.requireNoCheckpoint(t)
		})
	}

	t.Run("signer set version mismatch", func(t *testing.T) {
		f := newFixture(t, 2, 3)
		msg := f.signedMsg(t, 1, nil, root(0x01), 5, 0, 1)
		msg.SignerSetVersion = 2

		_, err := f.submit(msg)
		require.ErrorIs(t, err, types.ErrSignerSetMismatch)
		f.requireNoCheckpoint(t)
	})
}

// ---------------------------------------------------------------------------
// limits
// ---------------------------------------------------------------------------

func TestSubmitCheckpoint_Limits(t *testing.T) {
	cases := []struct {
		name   string
		params func(p *types.Params)
		msg    func(m *types.MsgSubmitCheckpoint)
		want   error
	}{
		{
			name:   "record_count above max",
			params: func(p *types.Params) { p.MaxRecordsPerCheckpoint = 5 },
			msg:    func(m *types.MsgSubmitCheckpoint) { m.RecordCount = 6 },
			want:   types.ErrLimitExceeded,
		},
		{
			name:   "data_pointer above max",
			params: func(p *types.Params) { p.MaxDataPointerBytes = 4 },
			msg:    func(m *types.MsgSubmitCheckpoint) { m.DataPointer = "ipfs://too-long" },
			want:   types.ErrLimitExceeded,
		},
		{
			name:   "too many signatures",
			params: func(p *types.Params) { p.MaxSignaturesPerCheckpoint = 1 },
			msg:    func(m *types.MsgSubmitCheckpoint) {},
			want:   types.ErrLimitExceeded,
		},
		{
			name:   "too many signature bytes",
			params: func(p *types.Params) { p.MaxSignatureBytesPerCheckpoint = 10 },
			msg:    func(m *types.MsgSubmitCheckpoint) {},
			want:   types.ErrLimitExceeded,
		},
		{
			name:   "state_root above max",
			params: func(p *types.Params) {},
			msg:    func(m *types.MsgSubmitCheckpoint) { m.StateRoot = bytes.Repeat([]byte{1}, types.MaxStateRootBytes+1) },
			want:   types.ErrLimitExceeded,
		},
		{
			name:   "empty state_root",
			params: func(p *types.Params) {},
			msg:    func(m *types.MsgSubmitCheckpoint) { m.StateRoot = nil },
			want:   types.ErrInvalidCheckpoint,
		},
		{
			name:   "sequence zero",
			params: func(p *types.Params) {},
			msg:    func(m *types.MsgSubmitCheckpoint) { m.Sequence = 0 },
			want:   types.ErrInvalidSequence,
		},
		{
			name:   "no signatures",
			params: func(p *types.Params) {},
			msg:    func(m *types.MsgSubmitCheckpoint) { m.Signatures = nil },
			want:   types.ErrThresholdNotMet,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, 2, 3)
			f.setParams(t, tc.params)

			// Limits are checked before signatures, so mutating the message
			// after signing is fine.
			msg := f.signedMsg(t, 1, nil, root(0x01), 5, 0, 1)
			tc.msg(msg)

			_, err := f.submit(msg)
			require.ErrorIs(t, err, tc.want)
			f.requireNoCheckpoint(t)
		})
	}
}

// ---------------------------------------------------------------------------
// fees
// ---------------------------------------------------------------------------

func TestSubmitCheckpoint_Fee(t *testing.T) {
	t.Run("base plus per-record fee goes to the fee collector", func(t *testing.T) {
		f := newFixture(t, 2, 3)
		f.setParams(t, func(p *types.Params) {
			p.BaseFee = sdk.NewInt64Coin(sdk.DefaultBondDenom, 10)
			p.PerRecordFee = sdk.NewInt64Coin(sdk.DefaultBondDenom, 2)
		})

		_, err := f.submit(f.signedMsg(t, 1, nil, root(0x01), 7, 0, 1))
		require.NoError(t, err)

		require.Len(t, f.bank.calls, 1)
		call := f.bank.calls[0]
		require.Equal(t, f.submitter, call.from)
		require.Equal(t, authtypes.FeeCollectorName, call.module)
		require.Equal(t, sdk.NewCoins(sdk.NewInt64Coin(sdk.DefaultBondDenom, 24)), call.amt) // 10 + 2*7
	})

	t.Run("zero fee does not touch the bank", func(t *testing.T) {
		f := newFixture(t, 2, 3)

		_, err := f.submit(f.signedMsg(t, 1, nil, root(0x01), 7, 0, 1))
		require.NoError(t, err)
		require.Empty(t, f.bank.calls)
	})

	t.Run("bank failure rejects the checkpoint", func(t *testing.T) {
		f := newFixture(t, 2, 3)
		f.setParams(t, func(p *types.Params) {
			p.BaseFee = sdk.NewInt64Coin(sdk.DefaultBondDenom, 10)
		})
		f.bank.err = errors.New("insufficient funds")

		_, err := f.submit(f.signedMsg(t, 1, nil, root(0x01), 7, 0, 1))
		require.ErrorContains(t, err, "insufficient funds")
		f.requireNoCheckpoint(t)
	})
}

// ---------------------------------------------------------------------------
// atomicity
// ---------------------------------------------------------------------------

// If x/sidechain rejects RecordCheckpoint after the checkpoint was stored, the
// whole message must revert. In a real tx the msg router runs handlers in a
// cached context and discards it on error; this test does the same.
func TestSubmitCheckpoint_RecordCheckpointFailureReverts(t *testing.T) {
	f := newFixture(t, 2, 3)
	f.sidechains.recordErr = errors.New("sidechain keeper boom")

	cacheCtx, _ := f.ctx.CacheContext() // write func intentionally not called
	_, err := f.msgServer.SubmitCheckpoint(cacheCtx, f.signedMsg(t, 1, nil, root(0x01), 5, 0, 1))
	require.ErrorContains(t, err, "boom")

	f.requireNoCheckpoint(t)
	_, err = f.k.GetCheckpoint(f.ctx, testSidechain, 1)
	require.ErrorIs(t, err, collections.ErrNotFound)
}

// ---------------------------------------------------------------------------
// params and genesis
// ---------------------------------------------------------------------------

func TestUpdateParams_Authority(t *testing.T) {
	f := newFixture(t, 2, 3)
	addrCodec := f.k.AddressCodec()

	gov, err := addrCodec.BytesToString(f.k.GetAuthority())
	require.NoError(t, err)

	newParams := types.DefaultParams()
	newParams.MaxRecordsPerCheckpoint = 123

	_, err = f.msgServer.UpdateParams(f.ctx, &types.MsgUpdateParams{Authority: f.submitterS, Params: newParams})
	require.ErrorContains(t, err, "invalid authority")

	_, err = f.msgServer.UpdateParams(f.ctx, &types.MsgUpdateParams{Authority: gov, Params: newParams})
	require.NoError(t, err)

	got, err := f.k.GetParams(f.ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(123), got.MaxRecordsPerCheckpoint)

	// Invalid params are rejected even from the right authority.
	bad := types.DefaultParams()
	bad.MaxRecordsPerCheckpoint = 0
	_, err = f.msgServer.UpdateParams(f.ctx, &types.MsgUpdateParams{Authority: gov, Params: bad})
	require.Error(t, err)
}

func TestGenesisRoundTrip(t *testing.T) {
	f := newFixture(t, 2, 3)

	resp1, err := f.submit(f.signedMsg(t, 1, nil, root(0x01), 5, 0, 1))
	require.NoError(t, err)
	_, err = f.submit(f.signedMsg(t, 2, resp1.CheckpointHash, root(0x02), 6, 1, 2))
	require.NoError(t, err)

	exported, err := f.k.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.Len(t, exported.Checkpoints, 2)
	require.NoError(t, exported.Validate())

	g := newFixture(t, 2, 3)
	require.NoError(t, g.k.InitGenesis(g.ctx, *exported))

	latest, err := g.k.GetLatestCheckpoint(g.ctx, testSidechain)
	require.NoError(t, err)
	require.Equal(t, uint64(2), latest.Sequence)

	// Chaining continues from the imported state.
	_, err = g.submit(g.signedMsg(t, 3, latest.CheckpointHash, root(0x03), 7, 0, 2))
	require.NoError(t, err)

	reExported, err := g.k.ExportGenesis(g.ctx)
	require.NoError(t, err)
	require.Equal(t, exported.Checkpoints, reExported.Checkpoints[:2])
	require.True(t, exported.Params.Equal(reExported.Params))
}
