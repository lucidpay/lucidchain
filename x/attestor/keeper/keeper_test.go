package keeper_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"cosmossdk.io/collections"
	"cosmossdk.io/core/address"
	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/cosmos/cosmos-sdk/codec"
	addresscodec "github.com/cosmos/cosmos-sdk/codec/address"
	codectestutil "github.com/cosmos/cosmos-sdk/codec/testutil"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	"github.com/cosmos/cosmos-sdk/runtime"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/cosmos/cosmos-sdk/types/query"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/lucidpay/lucidchain/x/attestor/keeper"
	"github.com/lucidpay/lucidchain/x/attestor/types"
	checkpointtypes "github.com/lucidpay/lucidchain/x/checkpoint/types"
)

// NOTE: the tests use ed25519 signer keys and set allowed_pubkey_type_urls
// accordingly. The keeper only calls cryptotypes.PubKey.VerifySignature, so
// this exercises the same code path as ML-DSA-65. Add an ML-DSA-65 case once
// confirmed the key package path in the SDK version.

const (
	testChainID   = "lucidchain-test-1"
	testSidechain = "sc-1"
	testSchema    = "hospitality.revenue-report.v1"
	testAttestor  = "dxb-tourism-authority"
	testHeight    = int64(100)

	attestorBond = int64(1_000_000)
	disputeBond  = int64(200_000)
	startFunds   = int64(10_000_000)

	day = 24 * time.Hour
)

var testBlockTime = time.Unix(1_700_000_000, 0).UTC()

// ---------------------------------------------------------------------------
// mocks
// ---------------------------------------------------------------------------

type mockCheckpointKeeper struct {
	checkpoints map[string]checkpointtypes.Checkpoint
}

var _ types.CheckpointKeeper = (*mockCheckpointKeeper)(nil)

func cpKey(id string, seq uint64) string { return fmt.Sprintf("%s/%d", id, seq) }

func (m *mockCheckpointKeeper) GetCheckpoint(_ context.Context, id string, seq uint64) (checkpointtypes.Checkpoint, error) {
	cp, ok := m.checkpoints[cpKey(id, seq)]
	if !ok {
		return checkpointtypes.Checkpoint{}, fmt.Errorf("%w: checkpoint %s", collections.ErrNotFound, cpKey(id, seq))
	}
	return cp, nil
}

// add registers a checkpoint and returns its (deterministic) hash.
func (m *mockCheckpointKeeper) add(id string, seq uint64) []byte {
	h := sha256.Sum256([]byte(cpKey(id, seq)))
	m.checkpoints[cpKey(id, seq)] = checkpointtypes.Checkpoint{
		SidechainId:    id,
		Sequence:       seq,
		CheckpointHash: h[:],
	}
	return h[:]
}

// mockBank is a single-denom ledger so tests can assert exact escrow, payout
// and burn amounts.
type mockBank struct {
	accounts map[string]math.Int
	modules  map[string]math.Int
	burned   math.Int

	failToModule   bool
	failFromModule bool
}

var _ types.BankKeeper = (*mockBank)(nil)

func newMockBank() *mockBank {
	return &mockBank{
		accounts: map[string]math.Int{},
		modules:  map[string]math.Int{},
		burned:   math.ZeroInt(),
	}
}

func (b *mockBank) balance(addr sdk.AccAddress) math.Int {
	if v, ok := b.accounts[string(addr)]; ok {
		return v
	}
	return math.ZeroInt()
}

func (b *mockBank) moduleBalance(name string) math.Int {
	if v, ok := b.modules[name]; ok {
		return v
	}
	return math.ZeroInt()
}

func (b *mockBank) fund(addr sdk.AccAddress, amt int64) {
	b.accounts[string(addr)] = b.balance(addr).Add(math.NewInt(amt))
}

// SpendableCoins satisfies the scaffolded BankKeeper interface (used by the
// module's AppModule, not by the keeper).
func (b *mockBank) SpendableCoins(_ context.Context, addr sdk.AccAddress) sdk.Coins {
	return sdk.NewCoins(sdk.NewCoin(sdk.DefaultBondDenom, b.balance(addr)))
}

func singleAmount(coins sdk.Coins) (math.Int, error) {
	if len(coins) != 1 || coins[0].Denom != sdk.DefaultBondDenom {
		return math.Int{}, fmt.Errorf("unexpected coins %s", coins)
	}
	return coins[0].Amount, nil
}

func (b *mockBank) SendCoinsFromAccountToModule(_ context.Context, from sdk.AccAddress, module string, coins sdk.Coins) error {
	if b.failToModule {
		return errors.New("bank: send to module failed")
	}
	amt, err := singleAmount(coins)
	if err != nil {
		return err
	}
	bal := b.balance(from)
	if bal.LT(amt) {
		return errors.New("insufficient funds")
	}
	b.accounts[string(from)] = bal.Sub(amt)
	b.modules[module] = b.moduleBalance(module).Add(amt)
	return nil
}

func (b *mockBank) SendCoinsFromModuleToAccount(_ context.Context, module string, to sdk.AccAddress, coins sdk.Coins) error {
	if b.failFromModule {
		return errors.New("bank: send from module failed")
	}
	amt, err := singleAmount(coins)
	if err != nil {
		return err
	}
	bal := b.moduleBalance(module)
	if bal.LT(amt) {
		return errors.New("module account has insufficient funds")
	}
	b.modules[module] = bal.Sub(amt)
	b.accounts[string(to)] = b.balance(to).Add(amt)
	return nil
}

func (b *mockBank) BurnCoins(_ context.Context, module string, coins sdk.Coins) error {
	amt, err := singleAmount(coins)
	if err != nil {
		return err
	}
	bal := b.moduleBalance(module)
	if bal.LT(amt) {
		return errors.New("module account has insufficient funds to burn")
	}
	b.modules[module] = bal.Sub(amt)
	b.burned = b.burned.Add(amt)
	return nil
}

// ---------------------------------------------------------------------------
// fixture
// ---------------------------------------------------------------------------

type fixture struct {
	ctx          sdk.Context
	keeper       keeper.Keeper
	addressCodec address.Codec
	msgServer    types.MsgServer
	queryServer  types.QueryServer
	checkpoints  *mockCheckpointKeeper
	bank         *mockBank

	// signers are the available keys; keyOrder[i] is the index into signers of
	// the key currently registered at position i of the attestor's signer_keys.
	signers  []*ed25519.PrivKey
	keyOrder []int

	gov, operator, challenger, stranger     sdk.AccAddress
	govS, operatorS, challengerS, strangerS string
}

// initFixture keeps Ignite's scaffolded tests working.
func initFixture(t *testing.T) *fixture { return newFixture(t) }

// newFixture builds a keeper with default params (ed25519 allowed), funded
// accounts, and three signer keys. Nothing is registered yet.
func newFixture(t *testing.T) *fixture {
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

	mk := func(b byte) sdk.AccAddress { return sdk.AccAddress(bytes.Repeat([]byte{b}, 20)) }
	str := func(a sdk.AccAddress) string {
		s, err := addrCodec.BytesToString(a)
		require.NoError(t, err)
		return s
	}

	f := &fixture{
		ctx:          ctx,
		addressCodec: addrCodec,
		checkpoints:  &mockCheckpointKeeper{checkpoints: map[string]checkpointtypes.Checkpoint{}},
		bank:         newMockBank(),
		signers:      make([]*ed25519.PrivKey, 3),
		keyOrder:     []int{0, 1, 2},
		gov:          authtypes.NewModuleAddress(types.GovModuleName),
		operator:     mk(1),
		challenger:   mk(2),
		stranger:     mk(3),
	}
	for i := range f.signers {
		f.signers[i] = ed25519.GenPrivKey()
	}
	f.govS, f.operatorS, f.challengerS, f.strangerS = str(f.gov), str(f.operator), str(f.challenger), str(f.stranger)

	for _, a := range []sdk.AccAddress{f.operator, f.challenger, f.stranger} {
		f.bank.fund(a, startFunds)
	}

	f.keeper = keeper.NewKeeper(runtime.NewKVStoreService(key), cdc, addrCodec, f.gov, f.checkpoints, f.bank)
	f.msgServer = keeper.NewMsgServerImpl(f.keeper)
	f.queryServer = keeper.NewQueryServerImpl(f.keeper)

	p := types.DefaultParams()
	p.AllowedPubkeyTypeUrls = []string{sdk.MsgTypeURL(&ed25519.PubKey{})}
	require.NoError(t, f.keeper.SetParams(f.ctx, p))

	return f
}

// newActiveFixture also publishes the default schema, registers and activates
// the default attestor, and adds checkpoints 1 and 2 to the mock.
func newActiveFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.bootstrap(t, attestorBond, nil)
	return f
}

func defaultSchema(id string) types.AttestationSchema {
	return types.AttestationSchema{
		Id:                    id,
		Title:                 "Revenue report",
		Domain:                "hospitality",
		ClaimDescription:      "Reported revenue figures match the submitted source data",
		Exclusions:            "Does not verify the authenticity of underlying transactions",
		RequiredDataSources:   []string{"pos-export"},
		ValidityPeriodSeconds: uint64(365 * 24 * 3600),
		DisputeWindowSeconds:  uint64(7 * 24 * 3600),
	}
}

func (f *fixture) createSchema(t *testing.T, s types.AttestationSchema) {
	t.Helper()
	_, err := f.msgServer.CreateAttestationSchema(f.ctx, &types.MsgCreateAttestationSchema{
		Authority: f.govS,
		Schema:    &s,
	})
	require.NoError(t, err)
}

func (f *fixture) keyAnys(t *testing.T, order []int) []*codectypes.Any {
	t.Helper()
	out := make([]*codectypes.Any, len(order))
	for i, idx := range order {
		a, err := codectypes.NewAnyWithValue(f.signers[idx].PubKey())
		require.NoError(t, err)
		out[i] = a
	}
	return out
}

func (f *fixture) registerMsg(t *testing.T) *types.MsgRegisterAttestor {
	t.Helper()
	return &types.MsgRegisterAttestor{
		Operator:            f.operatorS,
		Id:                  testAttestor,
		Name:                "Dubai Tourism Authority",
		AuthorizedSchemaIds: []string{testSchema},
		SignerKeys:          f.keyAnys(t, f.keyOrder),
		SignatureThreshold:  2,
		BondAmount:          math.NewInt(attestorBond),
		CredentialUri:       "https://example.org/credentials",
	}
}

// bootstrap publishes the default schema, registers and activates the default
// attestor with the given bond, and adds checkpoints 1 and 2.
func (f *fixture) bootstrap(t *testing.T, bond int64, mutateSchema func(*types.AttestationSchema)) {
	t.Helper()

	s := defaultSchema(testSchema)
	if mutateSchema != nil {
		mutateSchema(&s)
	}
	f.createSchema(t, s)

	msg := f.registerMsg(t)
	msg.BondAmount = math.NewInt(bond)
	_, err := f.msgServer.RegisterAttestor(f.ctx, msg)
	require.NoError(t, err)

	f.activate(t, testAttestor)

	f.checkpoints.add(testSidechain, 1)
	f.checkpoints.add(testSidechain, 2)
}

func (f *fixture) activate(t *testing.T, id string) {
	t.Helper()
	_, err := f.msgServer.ActivateAttestor(f.ctx, &types.MsgActivateAttestor{Authority: f.govS, Id: id})
	require.NoError(t, err)
}

func (f *fixture) setParams(t *testing.T, mutate func(p *types.Params)) {
	t.Helper()
	p, err := f.keeper.GetParams(f.ctx)
	require.NoError(t, err)
	mutate(&p)
	require.NoError(t, f.keeper.SetParams(f.ctx, p))
}

func (f *fixture) advance(d time.Duration) {
	f.ctx = f.ctx.WithBlockTime(f.ctx.BlockTime().Add(d))
}

func (f *fixture) getAttestor(t *testing.T) types.Attestor {
	t.Helper()
	a, err := f.keeper.GetAttestor(f.ctx, testAttestor)
	require.NoError(t, err)
	return a
}

func (f *fixture) setAttestorStatus(t *testing.T, st types.AttestorStatus) {
	t.Helper()
	a := f.getAttestor(t)
	a.Status = st
	require.NoError(t, f.keeper.Attestors.Set(f.ctx, a.Id, a))
}

func (f *fixture) getAttestation(t *testing.T, id string) types.Attestation {
	t.Helper()
	a, err := f.keeper.GetAttestation(f.ctx, id)
	require.NoError(t, err)
	return a
}

func (f *fixture) getDispute(t *testing.T, id string) types.Dispute {
	t.Helper()
	d, err := f.keeper.GetDispute(f.ctx, id)
	require.NoError(t, err)
	return d
}

func (f *fixture) signerVersion(t *testing.T) uint64 {
	return f.getAttestor(t).SignerSetVersion
}

func (f *fixture) cpHash(seq uint64) []byte {
	cp, err := f.checkpoints.GetCheckpoint(context.Background(), testSidechain, seq)
	if err != nil {
		return []byte("no-such-checkpoint")
	}
	return cp.CheckpointHash
}

func (f *fixture) bal(addr sdk.AccAddress) int64 { return f.bank.balance(addr).Int64() }

func (f *fixture) moduleBal() int64 { return f.bank.moduleBalance(types.ModuleName).Int64() }

func (f *fixture) exitQueueLen(t *testing.T) int {
	t.Helper()
	n := 0
	require.NoError(t, f.keeper.ExitQueue.Walk(f.ctx, nil, func(_ collections.Pair[uint64, string]) (bool, error) {
		n++
		return false, nil
	}))
	return n
}

// requireEscrowInvariant checks that the module account holds exactly the sum
// of all attestor bonds and all unresolved dispute bonds.
func (f *fixture) requireEscrowInvariant(t *testing.T) {
	t.Helper()
	want := math.ZeroInt()
	require.NoError(t, f.keeper.Attestors.Walk(f.ctx, nil, func(_ string, a types.Attestor) (bool, error) {
		want = want.Add(a.BondAmount)
		return false, nil
	}))
	require.NoError(t, f.keeper.Disputes.Walk(f.ctx, nil, func(_ string, d types.Dispute) (bool, error) {
		if !d.Resolved {
			want = want.Add(d.BondAmount)
		}
		return false, nil
	}))
	got := f.bank.moduleBalance(types.ModuleName)
	require.True(t, want.Equal(got), "module account holds %s but bonds sum to %s", got, want)
}

// --- attestation helpers ---

func signDocBytes(t *testing.T, m *types.MsgSubmitAttestation, chainID string, cpHash []byte) []byte {
	t.Helper()
	bz, err := types.AttestationSignBytes(&types.AttestationSignDoc{
		ChainId:            chainID,
		SchemaId:           m.SchemaId,
		AttestorId:         m.AttestorId,
		SidechainId:        m.SidechainId,
		CheckpointSequence: m.CheckpointSequence,
		CheckpointHash:     cpHash,
		ClaimPayload:       m.ClaimPayload,
		SignerSetVersion:   m.SignerSetVersion,
	})
	require.NoError(t, err)
	return bz
}

// signAttestation replaces m.Signatures with signatures from the keys at the
// given positions of the attestor's signer list, in the order given.
func (f *fixture) signAttestation(t *testing.T, m *types.MsgSubmitAttestation, chainID string, cpHash []byte, positions ...uint32) {
	t.Helper()
	bz := signDocBytes(t, m, chainID, cpHash)
	m.Signatures = nil
	for _, pos := range positions {
		sig, err := f.signers[f.keyOrder[pos]].Sign(bz)
		require.NoError(t, err)
		m.Signatures = append(m.Signatures, types.Signature{SignerIndex: pos, Signature: sig})
	}
}

func (f *fixture) attestMsg(t *testing.T, seq uint64, positions ...uint32) *types.MsgSubmitAttestation {
	t.Helper()
	m := &types.MsgSubmitAttestation{
		Submitter:          f.operatorS,
		SchemaId:           testSchema,
		AttestorId:         testAttestor,
		SidechainId:        testSidechain,
		CheckpointSequence: seq,
		ClaimPayload:       []byte(`{"revenue":123456}`),
		SignerSetVersion:   f.signerVersion(t),
	}
	f.signAttestation(t, m, testChainID, f.cpHash(seq), positions...)
	return m
}

// attest submits a valid attestation (signers 0 and 1) for checkpoint seq.
func (f *fixture) attest(t *testing.T, seq uint64) string {
	t.Helper()
	resp, err := f.msgServer.SubmitAttestation(f.ctx, f.attestMsg(t, seq, 0, 1))
	require.NoError(t, err)
	return resp.AttestationId
}

func (f *fixture) raiseDispute(t *testing.T, attestationID string) string {
	t.Helper()
	resp, err := f.msgServer.RaiseDispute(f.ctx, &types.MsgRaiseDispute{
		Challenger:    f.challengerS,
		AttestationId: attestationID,
		EvidenceUri:   "ipfs://evidence",
		BondAmount:    math.NewInt(disputeBond),
	})
	require.NoError(t, err)
	return resp.DisputeId
}

func (f *fixture) resolve(disputeID string, upheld bool) error {
	_, err := f.msgServer.ResolveDispute(f.ctx, &types.MsgResolveDispute{
		Authority:    f.govS,
		DisputeId:    disputeID,
		Upheld:       upheld,
		RationaleUri: "ipfs://rationale",
	})
	return err
}

func (f *fixture) mustResolve(t *testing.T, disputeID string, upheld bool) {
	t.Helper()
	require.NoError(t, f.resolve(disputeID, upheld))
}

func (f *fixture) exitMsg() *types.MsgInitiateAttestorExit {
	return &types.MsgInitiateAttestorExit{Operator: f.operatorS, Id: testAttestor}
}

// ---------------------------------------------------------------------------
// params
// ---------------------------------------------------------------------------

func TestUpdateParams(t *testing.T) {
	f := newFixture(t)

	valid := types.DefaultParams()
	valid.AllowedPubkeyTypeUrls = []string{sdk.MsgTypeURL(&ed25519.PubKey{})}
	valid.MaxClaimPayloadBytes = 1234

	t.Run("wrong authority", func(t *testing.T) {
		_, err := f.msgServer.UpdateParams(f.ctx, &types.MsgUpdateParams{Authority: f.strangerS, Params: valid})
		require.ErrorIs(t, err, types.ErrUnauthorized)
	})

	t.Run("malformed authority", func(t *testing.T) {
		_, err := f.msgServer.UpdateParams(f.ctx, &types.MsgUpdateParams{Authority: "bogus", Params: valid})
		require.ErrorIs(t, err, sdkerrors.ErrInvalidAddress)
	})

	t.Run("bond denom is immutable", func(t *testing.T) {
		p := valid
		p.BondDenom = "uother"
		_, err := f.msgServer.UpdateParams(f.ctx, &types.MsgUpdateParams{Authority: f.govS, Params: p})
		require.ErrorIs(t, err, types.ErrInvalidRequest)
	})

	t.Run("invalid params", func(t *testing.T) {
		p := valid
		p.SlashFraction = math.LegacyNewDec(2)
		_, err := f.msgServer.UpdateParams(f.ctx, &types.MsgUpdateParams{Authority: f.govS, Params: p})
		require.ErrorIs(t, err, sdkerrors.ErrInvalidRequest)
	})

	t.Run("malformed dispute resolver address", func(t *testing.T) {
		p := valid
		p.DisputeResolverAuthority = "bogus"
		_, err := f.msgServer.UpdateParams(f.ctx, &types.MsgUpdateParams{Authority: f.govS, Params: p})
		require.ErrorContains(t, err, "dispute_resolver_authority")
	})

	t.Run("valid update", func(t *testing.T) {
		_, err := f.msgServer.UpdateParams(f.ctx, &types.MsgUpdateParams{Authority: f.govS, Params: valid})
		require.NoError(t, err)
		got, err := f.keeper.GetParams(f.ctx)
		require.NoError(t, err)
		require.Equal(t, uint32(1234), got.MaxClaimPayloadBytes)
	})
}

// ---------------------------------------------------------------------------
// schemas
// ---------------------------------------------------------------------------

func TestCreateAttestationSchema(t *testing.T) {
	t.Run("success; new schemas are always active", func(t *testing.T) {
		f := newFixture(t)
		s := defaultSchema(testSchema)
		s.Active = false
		f.createSchema(t, s)

		got, err := f.keeper.GetSchema(f.ctx, testSchema)
		require.NoError(t, err)
		require.True(t, got.Active)
		require.Equal(t, s.Exclusions, got.Exclusions)
	})

	t.Run("duplicate id", func(t *testing.T) {
		f := newFixture(t)
		f.createSchema(t, defaultSchema(testSchema))
		s := defaultSchema(testSchema)
		_, err := f.msgServer.CreateAttestationSchema(f.ctx, &types.MsgCreateAttestationSchema{Authority: f.govS, Schema: &s})
		require.ErrorIs(t, err, types.ErrSchemaExists)
	})

	t.Run("not the authority", func(t *testing.T) {
		f := newFixture(t)
		s := defaultSchema(testSchema)
		_, err := f.msgServer.CreateAttestationSchema(f.ctx, &types.MsgCreateAttestationSchema{Authority: f.strangerS, Schema: &s})
		require.ErrorIs(t, err, types.ErrUnauthorized)
	})

	t.Run("nil schema", func(t *testing.T) {
		f := newFixture(t)
		_, err := f.msgServer.CreateAttestationSchema(f.ctx, &types.MsgCreateAttestationSchema{Authority: f.govS})
		require.ErrorIs(t, err, types.ErrInvalidRequest)
	})

	invalid := map[string]func(s *types.AttestationSchema){
		"uppercase id":           func(s *types.AttestationSchema) { s.Id = "Bad.Schema" },
		"slash in id":            func(s *types.AttestationSchema) { s.Id = "bad/schema" },
		"empty id":               func(s *types.AttestationSchema) { s.Id = "" },
		"empty title":            func(s *types.AttestationSchema) { s.Title = "" },
		"empty domain":           func(s *types.AttestationSchema) { s.Domain = "" },
		"empty claim":            func(s *types.AttestationSchema) { s.ClaimDescription = "" },
		"empty exclusions":       func(s *types.AttestationSchema) { s.Exclusions = "" },
		"zero validity period":   func(s *types.AttestationSchema) { s.ValidityPeriodSeconds = 0 },
		"zero dispute window":    func(s *types.AttestationSchema) { s.DisputeWindowSeconds = 0 },
		"absurd validity period": func(s *types.AttestationSchema) { s.ValidityPeriodSeconds = types.MaxPeriodSeconds + 1 },
		"empty data source":      func(s *types.AttestationSchema) { s.RequiredDataSources = []string{""} },
	}
	for name, mutate := range invalid {
		t.Run("invalid: "+name, func(t *testing.T) {
			f := newFixture(t)
			s := defaultSchema(testSchema)
			mutate(&s)
			_, err := f.msgServer.CreateAttestationSchema(f.ctx, &types.MsgCreateAttestationSchema{Authority: f.govS, Schema: &s})
			require.Error(t, err)
			has, err := f.keeper.Schemas.Has(f.ctx, s.Id)
			require.NoError(t, err)
			require.False(t, has)
		})
	}
}

func TestRetireAttestationSchema(t *testing.T) {
	f := newFixture(t)
	f.createSchema(t, defaultSchema(testSchema))

	_, err := f.msgServer.RetireAttestationSchema(f.ctx, &types.MsgRetireAttestationSchema{Authority: f.strangerS, SchemaId: testSchema})
	require.ErrorIs(t, err, types.ErrUnauthorized)

	_, err = f.msgServer.RetireAttestationSchema(f.ctx, &types.MsgRetireAttestationSchema{Authority: f.govS, SchemaId: "nope.v1"})
	require.ErrorIs(t, err, types.ErrSchemaNotFound)

	_, err = f.msgServer.RetireAttestationSchema(f.ctx, &types.MsgRetireAttestationSchema{Authority: f.govS, SchemaId: testSchema})
	require.NoError(t, err)
	s, err := f.keeper.GetSchema(f.ctx, testSchema)
	require.NoError(t, err)
	require.False(t, s.Active)

	_, err = f.msgServer.RetireAttestationSchema(f.ctx, &types.MsgRetireAttestationSchema{Authority: f.govS, SchemaId: testSchema})
	require.ErrorIs(t, err, types.ErrSchemaInactive)
}

func TestRetiredSchemaBlocksNewActivity(t *testing.T) {
	f := newActiveFixture(t)
	existing := f.attest(t, 1)

	_, err := f.msgServer.RetireAttestationSchema(f.ctx, &types.MsgRetireAttestationSchema{Authority: f.govS, SchemaId: testSchema})
	require.NoError(t, err)

	// No new attestations under the retired schema...
	_, err = f.msgServer.SubmitAttestation(f.ctx, f.attestMsg(t, 2, 0, 1))
	require.ErrorIs(t, err, types.ErrSchemaInactive)

	// ...nor new attestors authorized for it...
	m := f.registerMsg(t)
	m.Id = "second-attestor"
	_, err = f.msgServer.RegisterAttestor(f.ctx, m)
	require.ErrorIs(t, err, types.ErrSchemaInactive)

	// ...but history stays disputable.
	f.raiseDispute(t, existing)
}

// ---------------------------------------------------------------------------
// attestor registration and lifecycle
// ---------------------------------------------------------------------------

func TestRegisterAttestor_Success(t *testing.T) {
	f := newFixture(t)
	f.createSchema(t, defaultSchema(testSchema))

	msg := f.registerMsg(t)
	resp, err := f.msgServer.RegisterAttestor(f.ctx, msg)
	require.NoError(t, err)
	require.Equal(t, testAttestor, resp.Id)

	a := f.getAttestor(t)
	require.Equal(t, types.AttestorStatus_ATTESTOR_STATUS_PENDING, a.Status)
	require.Equal(t, uint64(1), a.SignerSetVersion)
	require.Equal(t, uint32(2), a.SignatureThreshold)
	require.Len(t, a.SignerKeys, 3)
	require.Equal(t, []string{testSchema}, a.AuthorizedSchemaIds)
	require.True(t, a.RegisteredAt.Equal(testBlockTime))
	require.Equal(t, attestorBond, a.BondAmount.Int64())
	require.Zero(t, a.ReputationScore)
	require.Zero(t, a.OpenDisputes)

	require.Equal(t, startFunds-attestorBond, f.bal(f.operator))
	require.Equal(t, attestorBond, f.moduleBal())
	f.requireEscrowInvariant(t)
}

func TestRegisterAttestor_Failures(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(t *testing.T, f *fixture, m *types.MsgRegisterAttestor)
		want    error
		wantMsg string
	}{
		{"bond below minimum", func(t *testing.T, f *fixture, m *types.MsgRegisterAttestor) {
			m.BondAmount = math.NewInt(attestorBond - 1)
		}, types.ErrInsufficientBond, ""},
		{"unset bond", func(t *testing.T, f *fixture, m *types.MsgRegisterAttestor) {
			m.BondAmount = math.Int{}
		}, types.ErrInsufficientBond, ""},
		{"unknown schema", func(t *testing.T, f *fixture, m *types.MsgRegisterAttestor) {
			m.AuthorizedSchemaIds = []string{"nope.v1"}
		}, types.ErrSchemaNotFound, ""},
		{"no schemas", func(t *testing.T, f *fixture, m *types.MsgRegisterAttestor) {
			m.AuthorizedSchemaIds = nil
		}, types.ErrInvalidRequest, ""},
		{"duplicate schema ids", func(t *testing.T, f *fixture, m *types.MsgRegisterAttestor) {
			m.AuthorizedSchemaIds = []string{testSchema, testSchema}
		}, types.ErrInvalidRequest, ""},
		{"uppercase id", func(t *testing.T, f *fixture, m *types.MsgRegisterAttestor) {
			m.Id = "Bad ID"
		}, types.ErrInvalidRequest, ""},
		{"id too long", func(t *testing.T, f *fixture, m *types.MsgRegisterAttestor) {
			m.Id = strings.Repeat("a", types.MaxIDBytes+1)
		}, types.ErrLimitExceeded, ""},
		{"empty name", func(t *testing.T, f *fixture, m *types.MsgRegisterAttestor) {
			m.Name = ""
		}, types.ErrInvalidRequest, ""},
		{"credential uri too long", func(t *testing.T, f *fixture, m *types.MsgRegisterAttestor) {
			m.CredentialUri = strings.Repeat("u", 513)
		}, types.ErrLimitExceeded, ""},
		{"no signer keys", func(t *testing.T, f *fixture, m *types.MsgRegisterAttestor) {
			m.SignerKeys = nil
		}, types.ErrInvalidSigners, ""},
		{"zero threshold", func(t *testing.T, f *fixture, m *types.MsgRegisterAttestor) {
			m.SignatureThreshold = 0
		}, types.ErrInvalidSigners, ""},
		{"threshold above key count", func(t *testing.T, f *fixture, m *types.MsgRegisterAttestor) {
			m.SignatureThreshold = 4
		}, types.ErrInvalidSigners, ""},
		{"more keys than max_signer_keys", func(t *testing.T, f *fixture, m *types.MsgRegisterAttestor) {
			f.setParams(t, func(p *types.Params) { p.MaxSignerKeys = 2 })
		}, types.ErrInvalidSigners, ""},
		{"duplicate signer key", func(t *testing.T, f *fixture, m *types.MsgRegisterAttestor) {
			m.SignerKeys = f.keyAnys(t, []int{0, 0})
		}, types.ErrInvalidSigners, ""},
		{"key type not allowed", func(t *testing.T, f *fixture, m *types.MsgRegisterAttestor) {
			a, err := codectypes.NewAnyWithValue(secp256k1.GenPrivKey().PubKey())
			require.NoError(t, err)
			m.SignerKeys = []*codectypes.Any{a}
			m.SignatureThreshold = 1
		}, types.ErrInvalidSigners, ""},
		{"malformed operator address", func(t *testing.T, f *fixture, m *types.MsgRegisterAttestor) {
			m.Operator = "bogus"
		}, sdkerrors.ErrInvalidAddress, ""},
		{"operator cannot afford the bond", func(t *testing.T, f *fixture, m *types.MsgRegisterAttestor) {
			m.BondAmount = math.NewInt(startFunds + 1)
		}, nil, "insufficient funds"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.createSchema(t, defaultSchema(testSchema))
			m := f.registerMsg(t)
			tc.mutate(t, f, m)

			_, err := f.msgServer.RegisterAttestor(f.ctx, m)
			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)
			} else {
				require.ErrorContains(t, err, tc.wantMsg)
			}

			// Nothing was stored and nothing was escrowed.
			has, herr := f.keeper.Attestors.Has(f.ctx, m.Id)
			require.NoError(t, herr)
			require.False(t, has)
			require.Zero(t, f.moduleBal())
			require.Equal(t, startFunds, f.bal(f.operator))
		})
	}
}

func TestRegisterAttestor_Duplicate(t *testing.T) {
	f := newActiveFixture(t)
	_, err := f.msgServer.RegisterAttestor(f.ctx, f.registerMsg(t))
	require.ErrorIs(t, err, types.ErrAttestorExists)
	require.Equal(t, attestorBond, f.moduleBal()) // second bond was not taken
}

func TestActivateAttestor(t *testing.T) {
	f := newFixture(t)
	f.createSchema(t, defaultSchema(testSchema))
	_, err := f.msgServer.RegisterAttestor(f.ctx, f.registerMsg(t))
	require.NoError(t, err)

	_, err = f.msgServer.ActivateAttestor(f.ctx, &types.MsgActivateAttestor{Authority: f.strangerS, Id: testAttestor})
	require.ErrorIs(t, err, types.ErrUnauthorized)

	_, err = f.msgServer.ActivateAttestor(f.ctx, &types.MsgActivateAttestor{Authority: f.govS, Id: "nope"})
	require.ErrorIs(t, err, types.ErrAttestorNotFound)

	f.activate(t, testAttestor)
	require.Equal(t, types.AttestorStatus_ATTESTOR_STATUS_ACTIVE, f.getAttestor(t).Status)

	// Only PENDING attestors can be activated.
	_, err = f.msgServer.ActivateAttestor(f.ctx, &types.MsgActivateAttestor{Authority: f.govS, Id: testAttestor})
	require.ErrorIs(t, err, types.ErrInvalidAttestorState)
}

func TestPendingAttestorCannotAttest(t *testing.T) {
	f := newFixture(t)
	f.createSchema(t, defaultSchema(testSchema))
	_, err := f.msgServer.RegisterAttestor(f.ctx, f.registerMsg(t))
	require.NoError(t, err)
	f.checkpoints.add(testSidechain, 1)

	_, err = f.msgServer.SubmitAttestation(f.ctx, f.attestMsg(t, 1, 0, 1))
	require.ErrorIs(t, err, types.ErrAttestorNotActive)
}

// ---------------------------------------------------------------------------
// signer rotation
// ---------------------------------------------------------------------------

func TestUpdateAttestorSigners(t *testing.T) {
	updateMsg := func(f *fixture, t *testing.T, order []int, threshold uint32) *types.MsgUpdateAttestorSigners {
		return &types.MsgUpdateAttestorSigners{
			Operator:           f.operatorS,
			Id:                 testAttestor,
			SignerKeys:         f.keyAnys(t, order),
			SignatureThreshold: threshold,
		}
	}

	t.Run("rotation bumps the version and changes which keys are valid", func(t *testing.T) {
		f := newActiveFixture(t)
		oldID := f.attest(t, 1)
		oldMsg := f.attestMsg(t, 2, 0, 1) // signed under version 1

		// New set: keys 2 and 0, in that order, threshold 2.
		_, err := f.msgServer.UpdateAttestorSigners(f.ctx, updateMsg(f, t, []int{2, 0}, 2))
		require.NoError(t, err)
		f.keyOrder = []int{2, 0}

		a := f.getAttestor(t)
		require.Equal(t, uint64(2), a.SignerSetVersion)
		require.Len(t, a.SignerKeys, 2)

		// A message built for the old version is rejected.
		_, err = f.msgServer.SubmitAttestation(f.ctx, oldMsg)
		require.ErrorIs(t, err, types.ErrSignerSetMismatch)

		// A message under the new set works.
		id := f.attest(t, 2)
		att := f.getAttestation(t, id)
		require.Equal(t, uint64(2), att.SignerSetVersion)

		// The earlier attestation keeps the version it was signed under.
		require.Equal(t, uint64(1), f.getAttestation(t, oldID).SignerSetVersion)
	})

	t.Run("a removed key can no longer sign", func(t *testing.T) {
		f := newActiveFixture(t)
		_, err := f.msgServer.UpdateAttestorSigners(f.ctx, updateMsg(f, t, []int{0, 1}, 2))
		require.NoError(t, err)

		// Sign with the removed key (signers[2]) in position 1.
		m := f.attestMsg(t, 1, 0)
		bz := signDocBytes(t, m, testChainID, f.cpHash(1))
		sig, err := f.signers[2].Sign(bz)
		require.NoError(t, err)
		m.Signatures = append(m.Signatures, types.Signature{SignerIndex: 1, Signature: sig})

		_, err = f.msgServer.SubmitAttestation(f.ctx, m)
		require.ErrorIs(t, err, types.ErrInvalidSignature)
	})

	t.Run("PENDING attestors can update", func(t *testing.T) {
		f := newFixture(t)
		f.createSchema(t, defaultSchema(testSchema))
		_, err := f.msgServer.RegisterAttestor(f.ctx, f.registerMsg(t))
		require.NoError(t, err)

		_, err = f.msgServer.UpdateAttestorSigners(f.ctx, updateMsg(f, t, []int{0, 1}, 1))
		require.NoError(t, err)
		require.Equal(t, uint64(2), f.getAttestor(t).SignerSetVersion)
	})

	t.Run("failures", func(t *testing.T) {
		f := newActiveFixture(t)

		bad := updateMsg(f, t, []int{0, 1}, 2)
		bad.Operator = f.strangerS
		_, err := f.msgServer.UpdateAttestorSigners(f.ctx, bad)
		require.ErrorIs(t, err, types.ErrUnauthorized)

		bad = updateMsg(f, t, []int{0, 1}, 2)
		bad.Id = "nope"
		_, err = f.msgServer.UpdateAttestorSigners(f.ctx, bad)
		require.ErrorIs(t, err, types.ErrAttestorNotFound)

		_, err = f.msgServer.UpdateAttestorSigners(f.ctx, updateMsg(f, t, []int{0, 1}, 0))
		require.ErrorIs(t, err, types.ErrInvalidSigners)

		_, err = f.msgServer.UpdateAttestorSigners(f.ctx, updateMsg(f, t, []int{0, 1}, 3))
		require.ErrorIs(t, err, types.ErrInvalidSigners)

		require.Equal(t, uint64(1), f.getAttestor(t).SignerSetVersion)

		for _, st := range []types.AttestorStatus{
			types.AttestorStatus_ATTESTOR_STATUS_SLASHED,
			types.AttestorStatus_ATTESTOR_STATUS_EXITED,
		} {
			f.setAttestorStatus(t, st)
			_, err = f.msgServer.UpdateAttestorSigners(f.ctx, updateMsg(f, t, []int{0, 1}, 2))
			require.ErrorIs(t, err, types.ErrInvalidAttestorState, st.String())
		}
	})
}

// ---------------------------------------------------------------------------
// submitting attestations
// ---------------------------------------------------------------------------

func TestSubmitAttestation_Success(t *testing.T) {
	f := newActiveFixture(t)

	// Signatures deliberately submitted out of order (position 1 before 0).
	msg := f.attestMsg(t, 1, 1, 0)
	sigByPos := map[uint32][]byte{}
	for _, s := range msg.Signatures {
		sigByPos[s.SignerIndex] = s.Signature
	}

	resp, err := f.msgServer.SubmitAttestation(f.ctx, msg)
	require.NoError(t, err)

	wantID := types.AttestationID(testSchema, testSidechain, 1, testAttestor)
	require.Equal(t, wantID, resp.AttestationId)

	att := f.getAttestation(t, wantID)
	require.Equal(t, testSchema, att.SchemaId)
	require.Equal(t, testAttestor, att.AttestorId)
	require.Equal(t, testSidechain, att.SidechainId)
	require.Equal(t, uint64(1), att.CheckpointSequence)
	require.Equal(t, []byte(`{"revenue":123456}`), att.ClaimPayload)
	require.Equal(t, types.AttestationStatus_ATTESTATION_STATUS_ACTIVE, att.Status)
	require.Equal(t, uint64(testHeight), att.IssuedHeight)
	require.True(t, att.IssuedAt.Equal(testBlockTime))
	require.True(t, att.ExpiresAt.Equal(testBlockTime.Add(365*day)))
	require.Equal(t, f.cpHash(1), att.CheckpointHash)
	require.Equal(t, uint64(1), att.SignerSetVersion)

	// Signers at positions 0 and 1 => bits 0 and 1 of byte 0.
	require.Equal(t, []byte{0b00000011}, att.SignerBitmap)

	// Digest is over the signatures ordered by signer index (0, then 1).
	h := sha256.New()
	h.Write(sigByPos[0])
	h.Write(sigByPos[1])
	require.Equal(t, h.Sum(nil), att.SignaturesDigest)

	require.Equal(t, int64(1), f.getAttestor(t).ReputationScore)

	// The by-checkpoint index finds it.
	res, err := f.queryServer.AttestationsForCheckpoint(f.ctx, &types.QueryAttestationsForCheckpointRequest{
		SidechainId: testSidechain, CheckpointSequence: 1,
	})
	require.NoError(t, err)
	require.Len(t, res.Attestations, 1)
	require.Equal(t, wantID, res.Attestations[0].Id)
}

func TestSubmitAttestation_Failures(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(t *testing.T, f *fixture, m *types.MsgSubmitAttestation)
		want   error
	}{
		{"unknown schema", func(t *testing.T, f *fixture, m *types.MsgSubmitAttestation) {
			m.SchemaId = "nope.v1"
		}, types.ErrSchemaNotFound},
		{"retired schema", func(t *testing.T, f *fixture, m *types.MsgSubmitAttestation) {
			_, err := f.msgServer.RetireAttestationSchema(f.ctx, &types.MsgRetireAttestationSchema{Authority: f.govS, SchemaId: testSchema})
			require.NoError(t, err)
		}, types.ErrSchemaInactive},
		{"unknown attestor", func(t *testing.T, f *fixture, m *types.MsgSubmitAttestation) {
			m.AttestorId = "nope"
		}, types.ErrAttestorNotFound},
		{"submitter is not the operator", func(t *testing.T, f *fixture, m *types.MsgSubmitAttestation) {
			m.Submitter = f.strangerS
		}, types.ErrUnauthorized},
		{"malformed submitter", func(t *testing.T, f *fixture, m *types.MsgSubmitAttestation) {
			m.Submitter = "bogus"
		}, sdkerrors.ErrInvalidAddress},
		{"attestor suspended", func(t *testing.T, f *fixture, m *types.MsgSubmitAttestation) {
			f.setAttestorStatus(t, types.AttestorStatus_ATTESTOR_STATUS_SUSPENDED)
		}, types.ErrAttestorNotActive},
		{"attestor slashed", func(t *testing.T, f *fixture, m *types.MsgSubmitAttestation) {
			f.setAttestorStatus(t, types.AttestorStatus_ATTESTOR_STATUS_SLASHED)
		}, types.ErrAttestorNotActive},
		{"schema not authorized for this attestor", func(t *testing.T, f *fixture, m *types.MsgSubmitAttestation) {
			f.createSchema(t, defaultSchema("other.v1"))
			m.SchemaId = "other.v1"
		}, types.ErrSchemaNotAuthorized},
		{"signer set version mismatch", func(t *testing.T, f *fixture, m *types.MsgSubmitAttestation) {
			m.SignerSetVersion = 99
		}, types.ErrSignerSetMismatch},
		{"empty sidechain id", func(t *testing.T, f *fixture, m *types.MsgSubmitAttestation) {
			m.SidechainId = ""
		}, types.ErrInvalidRequest},
		{"checkpoint sequence zero", func(t *testing.T, f *fixture, m *types.MsgSubmitAttestation) {
			m.CheckpointSequence = 0
		}, types.ErrInvalidRequest},
		{"empty claim payload", func(t *testing.T, f *fixture, m *types.MsgSubmitAttestation) {
			m.ClaimPayload = nil
		}, types.ErrInvalidRequest},
		{"claim payload is not JSON", func(t *testing.T, f *fixture, m *types.MsgSubmitAttestation) {
			m.ClaimPayload = []byte("not json")
		}, types.ErrInvalidRequest},
		{"claim payload too large", func(t *testing.T, f *fixture, m *types.MsgSubmitAttestation) {
			m.ClaimPayload = bytes.Repeat([]byte("a"), 4097)
		}, types.ErrLimitExceeded},
		{"checkpoint does not exist", func(t *testing.T, f *fixture, m *types.MsgSubmitAttestation) {
			m.CheckpointSequence = 99
		}, types.ErrCheckpointNotFound},
		{"threshold not met", func(t *testing.T, f *fixture, m *types.MsgSubmitAttestation) {
			f.signAttestation(t, m, testChainID, f.cpHash(1), 0)
		}, types.ErrThresholdNotMet},
		{"no signatures", func(t *testing.T, f *fixture, m *types.MsgSubmitAttestation) {
			m.Signatures = nil
		}, types.ErrThresholdNotMet},
		{"duplicate signature cannot meet the threshold", func(t *testing.T, f *fixture, m *types.MsgSubmitAttestation) {
			f.signAttestation(t, m, testChainID, f.cpHash(1), 1, 1)
		}, types.ErrInvalidSignature},
		{"claim payload tampered after signing", func(t *testing.T, f *fixture, m *types.MsgSubmitAttestation) {
			m.ClaimPayload = []byte(`{"revenue":999999}`)
		}, types.ErrInvalidSignature},
		{"signed for another chain id", func(t *testing.T, f *fixture, m *types.MsgSubmitAttestation) {
			f.signAttestation(t, m, "some-other-chain-1", f.cpHash(1), 0, 1)
		}, types.ErrInvalidSignature},
		{"signed for a different checkpoint hash", func(t *testing.T, f *fixture, m *types.MsgSubmitAttestation) {
			f.signAttestation(t, m, testChainID, []byte("some-other-hash"), 0, 1)
		}, types.ErrInvalidSignature},
		{"signer index out of range", func(t *testing.T, f *fixture, m *types.MsgSubmitAttestation) {
			m.Signatures = append(m.Signatures, types.Signature{SignerIndex: 7, Signature: []byte("x")})
		}, types.ErrInvalidSignature},
		{"more signatures than signer keys", func(t *testing.T, f *fixture, m *types.MsgSubmitAttestation) {
			f.signAttestation(t, m, testChainID, f.cpHash(1), 0, 1, 2)
			m.Signatures = append(m.Signatures, m.Signatures[0])
		}, types.ErrInvalidSignature},
		{"signature from an unregistered key", func(t *testing.T, f *fixture, m *types.MsgSubmitAttestation) {
			rogue := ed25519.GenPrivKey()
			sig, err := rogue.Sign(signDocBytes(t, m, testChainID, f.cpHash(1)))
			require.NoError(t, err)
			m.Signatures[0].Signature = sig
		}, types.ErrInvalidSignature},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newActiveFixture(t)
			msg := f.attestMsg(t, 1, 0, 1)
			tc.mutate(t, f, msg)

			_, err := f.msgServer.SubmitAttestation(f.ctx, msg)
			require.ErrorIs(t, err, tc.want)

			// Nothing was stored and the attestor's reputation is untouched.
			id := types.AttestationID(testSchema, testSidechain, 1, testAttestor)
			has, herr := f.keeper.Attestations.Has(f.ctx, id)
			require.NoError(t, herr)
			require.False(t, has)
			require.Zero(t, f.getAttestor(t).ReputationScore)
		})
	}
}

func TestSubmitAttestation_OnlyOncePerTuple(t *testing.T) {
	f := newActiveFixture(t)
	id := f.attest(t, 1)

	_, err := f.msgServer.SubmitAttestation(f.ctx, f.attestMsg(t, 1, 0, 1))
	require.ErrorIs(t, err, types.ErrAttestationExists)

	// Still true after revocation: the tuple cannot be re-attested.
	_, err = f.msgServer.RevokeAttestation(f.ctx, &types.MsgRevokeAttestation{Operator: f.operatorS, AttestationId: id})
	require.NoError(t, err)
	_, err = f.msgServer.SubmitAttestation(f.ctx, f.attestMsg(t, 1, 0, 1))
	require.ErrorIs(t, err, types.ErrAttestationExists)

	// A different checkpoint is fine.
	f.attest(t, 2)
}

func TestSubmitAttestation_Gas(t *testing.T) {
	f := newActiveFixture(t)
	p, err := f.keeper.GetParams(f.ctx)
	require.NoError(t, err)

	// Threshold failures cost no verification gas.
	before := f.ctx.GasMeter().GasConsumed()
	_, err = f.msgServer.SubmitAttestation(f.ctx, f.attestMsg(t, 1, 0))
	require.ErrorIs(t, err, types.ErrThresholdNotMet)
	require.Less(t, f.ctx.GasMeter().GasConsumed()-before, p.SignatureVerificationGas)

	// Two verified signatures cost at least twice the per-signature gas.
	before = f.ctx.GasMeter().GasConsumed()
	f.attest(t, 1)
	require.GreaterOrEqual(t, f.ctx.GasMeter().GasConsumed()-before, 2*p.SignatureVerificationGas)
}

func TestRevokeAttestation(t *testing.T) {
	f := newActiveFixture(t)
	id := f.attest(t, 1)

	_, err := f.msgServer.RevokeAttestation(f.ctx, &types.MsgRevokeAttestation{Operator: f.strangerS, AttestationId: id})
	require.ErrorIs(t, err, types.ErrUnauthorized)

	_, err = f.msgServer.RevokeAttestation(f.ctx, &types.MsgRevokeAttestation{Operator: f.operatorS, AttestationId: "nope"})
	require.ErrorIs(t, err, types.ErrAttestationNotFound)

	_, err = f.msgServer.RevokeAttestation(f.ctx, &types.MsgRevokeAttestation{Operator: f.operatorS, AttestationId: id})
	require.NoError(t, err)
	require.Equal(t, types.AttestationStatus_ATTESTATION_STATUS_REVOKED, f.getAttestation(t, id).Status)

	_, err = f.msgServer.RevokeAttestation(f.ctx, &types.MsgRevokeAttestation{Operator: f.operatorS, AttestationId: id})
	require.ErrorIs(t, err, types.ErrInvalidAttestationState)

	// A revoked attestation can no longer be disputed.
	_, err = f.msgServer.RaiseDispute(f.ctx, &types.MsgRaiseDispute{
		Challenger: f.challengerS, AttestationId: id, EvidenceUri: "ipfs://e", BondAmount: math.NewInt(disputeBond),
	})
	require.ErrorIs(t, err, types.ErrInvalidAttestationState)
}

func TestRevokeAttestation_DisputedCannotBeRevoked(t *testing.T) {
	f := newActiveFixture(t)
	id := f.attest(t, 1)
	f.raiseDispute(t, id)

	_, err := f.msgServer.RevokeAttestation(f.ctx, &types.MsgRevokeAttestation{Operator: f.operatorS, AttestationId: id})
	require.ErrorIs(t, err, types.ErrInvalidAttestationState)
}

// ---------------------------------------------------------------------------
// raising disputes
// ---------------------------------------------------------------------------

func TestRaiseDispute_Success(t *testing.T) {
	f := newActiveFixture(t)
	attID := f.attest(t, 1)

	disputeID := f.raiseDispute(t, attID)
	require.Equal(t, types.DisputeID(attID), disputeID)

	d := f.getDispute(t, disputeID)
	require.Equal(t, attID, d.AttestationId)
	require.Equal(t, f.challengerS, d.Challenger)
	require.Equal(t, "ipfs://evidence", d.EvidenceUri)
	require.Equal(t, disputeBond, d.BondAmount.Int64())
	require.False(t, d.Resolved)
	require.False(t, d.Upheld)
	require.True(t, d.RaisedAt.Equal(testBlockTime))
	require.Nil(t, d.ResolvedAt)

	require.Equal(t, types.AttestationStatus_ATTESTATION_STATUS_DISPUTED, f.getAttestation(t, attID).Status)

	a := f.getAttestor(t)
	require.Equal(t, types.AttestorStatus_ATTESTOR_STATUS_SUSPENDED, a.Status)
	require.Equal(t, uint64(1), a.OpenDisputes)

	require.Equal(t, startFunds-disputeBond, f.bal(f.challenger))
	require.Equal(t, attestorBond+disputeBond, f.moduleBal())
	f.requireEscrowInvariant(t)

	// A suspended attestor cannot attest.
	_, err := f.msgServer.SubmitAttestation(f.ctx, f.attestMsg(t, 2, 0, 1))
	require.ErrorIs(t, err, types.ErrAttestorNotActive)
}

func TestRaiseDispute_WindowBoundary(t *testing.T) {
	window := 7 * day

	t.Run("allowed exactly at the deadline", func(t *testing.T) {
		f := newActiveFixture(t)
		attID := f.attest(t, 1)
		f.advance(window)
		f.raiseDispute(t, attID)
	})

	t.Run("rejected one second later", func(t *testing.T) {
		f := newActiveFixture(t)
		attID := f.attest(t, 1)
		f.advance(window + time.Second)

		_, err := f.msgServer.RaiseDispute(f.ctx, &types.MsgRaiseDispute{
			Challenger: f.challengerS, AttestationId: attID, EvidenceUri: "ipfs://e", BondAmount: math.NewInt(disputeBond),
		})
		require.ErrorIs(t, err, types.ErrDisputeWindowClosed)
		require.Equal(t, types.AttestationStatus_ATTESTATION_STATUS_ACTIVE, f.getAttestation(t, attID).Status)
	})
}

func TestRaiseDispute_Failures(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(t *testing.T, f *fixture, m *types.MsgRaiseDispute)
		want    error
		wantMsg string
	}{
		{"bond below minimum", func(t *testing.T, f *fixture, m *types.MsgRaiseDispute) {
			m.BondAmount = math.NewInt(99_999)
		}, types.ErrInsufficientBond, ""},
		{"unset bond", func(t *testing.T, f *fixture, m *types.MsgRaiseDispute) {
			m.BondAmount = math.Int{}
		}, types.ErrInsufficientBond, ""},
		{"empty evidence", func(t *testing.T, f *fixture, m *types.MsgRaiseDispute) {
			m.EvidenceUri = ""
		}, types.ErrInvalidRequest, ""},
		{"evidence uri too long", func(t *testing.T, f *fixture, m *types.MsgRaiseDispute) {
			m.EvidenceUri = strings.Repeat("e", 513)
		}, types.ErrLimitExceeded, ""},
		{"unknown attestation", func(t *testing.T, f *fixture, m *types.MsgRaiseDispute) {
			m.AttestationId = "nope"
		}, types.ErrAttestationNotFound, ""},
		{"malformed challenger", func(t *testing.T, f *fixture, m *types.MsgRaiseDispute) {
			m.Challenger = "bogus"
		}, sdkerrors.ErrInvalidAddress, ""},
		{"the attestor's operator cannot dispute its own attestation", func(t *testing.T, f *fixture, m *types.MsgRaiseDispute) {
			m.Challenger = f.operatorS
		}, types.ErrUnauthorized, ""},
		{"already disputed", func(t *testing.T, f *fixture, m *types.MsgRaiseDispute) {
			f.raiseDispute(t, m.AttestationId)
		}, types.ErrInvalidAttestationState, ""},
		{"challenger cannot afford the bond", func(t *testing.T, f *fixture, m *types.MsgRaiseDispute) {
			m.BondAmount = math.NewInt(startFunds + 1)
		}, nil, "insufficient funds"},
		{"bank failure", func(t *testing.T, f *fixture, m *types.MsgRaiseDispute) {
			f.bank.failToModule = true
		}, nil, "send to module failed"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newActiveFixture(t)
			attID := f.attest(t, 1)
			m := &types.MsgRaiseDispute{
				Challenger:    f.challengerS,
				AttestationId: attID,
				EvidenceUri:   "ipfs://evidence",
				BondAmount:    math.NewInt(disputeBond),
			}
			tc.mutate(t, f, m)
			moduleBefore := f.moduleBal()

			_, err := f.msgServer.RaiseDispute(f.ctx, m)
			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)
			} else {
				require.ErrorContains(t, err, tc.wantMsg)
			}

			if tc.name != "already disputed" {
				has, herr := f.keeper.Disputes.Has(f.ctx, types.DisputeID(attID))
				require.NoError(t, herr)
				require.False(t, has)
				require.Equal(t, types.AttestationStatus_ATTESTATION_STATUS_ACTIVE, f.getAttestation(t, attID).Status)
				require.Equal(t, types.AttestorStatus_ATTESTOR_STATUS_ACTIVE, f.getAttestor(t).Status)
			}
			require.Equal(t, moduleBefore, f.moduleBal())
		})
	}
}

// ---------------------------------------------------------------------------
// resolving disputes
// ---------------------------------------------------------------------------

func TestResolveDispute_Upheld(t *testing.T) {
	f := newActiveFixture(t)
	attID := f.attest(t, 1)
	disputeID := f.raiseDispute(t, attID)

	f.mustResolve(t, disputeID, true)

	d := f.getDispute(t, disputeID)
	require.True(t, d.Resolved)
	require.True(t, d.Upheld)
	require.NotNil(t, d.ResolvedAt)
	require.Equal(t, "ipfs://rationale", d.RationaleUri)

	require.Equal(t, types.AttestationStatus_ATTESTATION_STATUS_OVERTURNED, f.getAttestation(t, attID).Status)

	a := f.getAttestor(t)
	require.Equal(t, types.AttestorStatus_ATTESTOR_STATUS_SLASHED, a.Status)
	require.Zero(t, a.OpenDisputes)
	require.Equal(t, int64(-1), a.ReputationScore) // +1 for submitting, -2 for the overturn
	require.Equal(t, attestorBond-100_000, a.BondAmount.Int64())

	// Default params: slash 10%, half of it to the challenger, half burned.
	require.Equal(t, startFunds+50_000, f.bal(f.challenger))
	require.Equal(t, int64(50_000), f.bank.burned.Int64())
	require.Equal(t, attestorBond-100_000, f.moduleBal())
	f.requireEscrowInvariant(t)

	// A slashed attestor cannot attest, but can exit to recover what is left.
	_, err := f.msgServer.SubmitAttestation(f.ctx, f.attestMsg(t, 2, 0, 1))
	require.ErrorIs(t, err, types.ErrAttestorNotActive)
	_, err = f.msgServer.InitiateAttestorExit(f.ctx, f.exitMsg())
	require.NoError(t, err)
}

func TestResolveDispute_NotUpheld(t *testing.T) {
	f := newActiveFixture(t)
	attID := f.attest(t, 1)
	disputeID := f.raiseDispute(t, attID)

	f.mustResolve(t, disputeID, false)

	d := f.getDispute(t, disputeID)
	require.True(t, d.Resolved)
	require.False(t, d.Upheld)

	require.Equal(t, types.AttestationStatus_ATTESTATION_STATUS_UPHELD, f.getAttestation(t, attID).Status)

	a := f.getAttestor(t)
	require.Equal(t, types.AttestorStatus_ATTESTOR_STATUS_ACTIVE, a.Status)
	require.Zero(t, a.OpenDisputes)
	require.Equal(t, int64(1), a.ReputationScore)
	require.Equal(t, attestorBond, a.BondAmount.Int64())

	// The challenger's bond is burned.
	require.Equal(t, startFunds-disputeBond, f.bal(f.challenger))
	require.Equal(t, disputeBond, f.bank.burned.Int64())
	require.Equal(t, attestorBond, f.moduleBal())
	f.requireEscrowInvariant(t)

	// The reinstated attestor can attest again, and the upheld attestation is final.
	f.attest(t, 2)
	_, err := f.msgServer.RaiseDispute(f.ctx, &types.MsgRaiseDispute{
		Challenger: f.challengerS, AttestationId: attID, EvidenceUri: "ipfs://e", BondAmount: math.NewInt(disputeBond),
	})
	require.ErrorIs(t, err, types.ErrInvalidAttestationState)
}

func TestResolveDispute_Arithmetic(t *testing.T) {
	cases := []struct {
		name        string
		minBond     int64
		bond        int64
		slash       string
		reward      string
		wantSlashed int64
		wantReward  int64
		wantBurned  int64
	}{
		{"10% slash, half to the challenger", 1_000_000, 1_000_000, "0.10", "0.50", 100_000, 50_000, 50_000},
		{"fractions truncate", 1_000_000, 1_000_001, "0.333", "0.5", 333_000, 166_500, 166_500},
		{"full slash, full reward", 1_000_000, 1_000_000, "1", "1", 1_000_000, 1_000_000, 0},
		{"zero slash", 1_000_000, 1_000_000, "0", "0.5", 0, 0, 0},
		{"zero reward burns everything", 1_000_000, 1_000_000, "0.2", "0", 200_000, 0, 200_000},
		{"odd amounts", 1, 999, "0.5", "0.5", 499, 249, 250},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.setParams(t, func(p *types.Params) {
				p.MinAttestorBond = math.NewInt(tc.minBond)
				p.SlashFraction = math.LegacyMustNewDecFromStr(tc.slash)
				p.ChallengerRewardFraction = math.LegacyMustNewDecFromStr(tc.reward)
			})
			f.bootstrap(t, tc.bond, nil)

			attID := f.attest(t, 1)
			disputeID := f.raiseDispute(t, attID)
			challengerBefore := f.bal(f.challenger) + disputeBond // balance before the bond was escrowed

			f.mustResolve(t, disputeID, true)

			require.Equal(t, tc.bond-tc.wantSlashed, f.getAttestor(t).BondAmount.Int64())
			require.Equal(t, challengerBefore+tc.wantReward, f.bal(f.challenger))
			require.Equal(t, tc.wantBurned, f.bank.burned.Int64())
			require.Equal(t, tc.bond-tc.wantSlashed, f.moduleBal())
			require.Equal(t, tc.wantSlashed, tc.wantReward+tc.wantBurned)
			f.requireEscrowInvariant(t)
		})
	}
}

func TestResolveDispute_Failures(t *testing.T) {
	f := newActiveFixture(t)
	attID := f.attest(t, 1)
	disputeID := f.raiseDispute(t, attID)

	_, e := f.msgServer.ResolveDispute(f.ctx, &types.MsgResolveDispute{Authority: f.strangerS, DisputeId: disputeID})
	require.ErrorIs(t, e, types.ErrUnauthorized)

	_, e = f.msgServer.ResolveDispute(f.ctx, &types.MsgResolveDispute{Authority: "bogus", DisputeId: disputeID})
	require.ErrorIs(t, e, sdkerrors.ErrInvalidAddress)

	_, e = f.msgServer.ResolveDispute(f.ctx, &types.MsgResolveDispute{Authority: f.govS, DisputeId: "nope"})
	require.ErrorIs(t, e, types.ErrDisputeNotFound)

	_, e = f.msgServer.ResolveDispute(f.ctx, &types.MsgResolveDispute{
		Authority: f.govS, DisputeId: disputeID, RationaleUri: strings.Repeat("r", 513),
	})
	require.ErrorIs(t, e, types.ErrLimitExceeded)

	// A failed attempt changed nothing.
	require.False(t, f.getDispute(t, disputeID).Resolved)
	require.Equal(t, types.AttestationStatus_ATTESTATION_STATUS_DISPUTED, f.getAttestation(t, attID).Status)

	f.mustResolve(t, disputeID, false)
	require.ErrorIs(t, f.resolve(disputeID, true), types.ErrDisputeResolved)
}

func TestResolveDispute_BankFailureLeavesStateUntouched(t *testing.T) {
	f := newActiveFixture(t)
	disputeID := f.raiseDispute(t, f.attest(t, 1))
	f.bank.failFromModule = true

	// Run in a cached context, as the msg router would, and discard it on error.
	cacheCtx, _ := f.ctx.CacheContext()
	_, err := f.msgServer.ResolveDispute(cacheCtx, &types.MsgResolveDispute{Authority: f.govS, DisputeId: disputeID, Upheld: true})
	require.ErrorContains(t, err, "failed to pay challenger")

	require.False(t, f.getDispute(t, disputeID).Resolved)
	require.Equal(t, types.AttestorStatus_ATTESTOR_STATUS_SUSPENDED, f.getAttestor(t).Status)
}

func TestResolveDispute_CustomResolverAuthority(t *testing.T) {
	f := newActiveFixture(t)
	f.setParams(t, func(p *types.Params) { p.DisputeResolverAuthority = f.strangerS })
	disputeID := f.raiseDispute(t, f.attest(t, 1))

	// With a custom resolver, gov can no longer resolve...
	require.ErrorIs(t, f.resolve(disputeID, true), types.ErrUnauthorized)

	// ...and the configured resolver can.
	_, err := f.msgServer.ResolveDispute(f.ctx, &types.MsgResolveDispute{Authority: f.strangerS, DisputeId: disputeID, Upheld: false})
	require.NoError(t, err)
	require.True(t, f.getDispute(t, disputeID).Resolved)
}

func TestMultipleOpenDisputes(t *testing.T) {
	t.Run("attestor is reinstated only when the last dispute is resolved", func(t *testing.T) {
		f := newActiveFixture(t)
		d1 := f.raiseDispute(t, f.attest(t, 1))
		// The second attestation must exist before the attestor is suspended.
		// Re-activate the attestor to attest again, then dispute both.
		f.setAttestorStatus(t, types.AttestorStatus_ATTESTOR_STATUS_ACTIVE)
		att2 := f.attest(t, 2)
		d2 := f.raiseDispute(t, att2)

		require.Equal(t, uint64(2), f.getAttestor(t).OpenDisputes)

		f.mustResolve(t, d1, false)
		a := f.getAttestor(t)
		require.Equal(t, uint64(1), a.OpenDisputes)
		require.Equal(t, types.AttestorStatus_ATTESTOR_STATUS_SUSPENDED, a.Status)

		f.mustResolve(t, d2, false)
		a = f.getAttestor(t)
		require.Zero(t, a.OpenDisputes)
		require.Equal(t, types.AttestorStatus_ATTESTOR_STATUS_ACTIVE, a.Status)
		f.requireEscrowInvariant(t)
	})

	t.Run("a later not-upheld resolution does not undo a slash", func(t *testing.T) {
		f := newActiveFixture(t)
		d1 := f.raiseDispute(t, f.attest(t, 1))
		f.setAttestorStatus(t, types.AttestorStatus_ATTESTOR_STATUS_ACTIVE)
		d2 := f.raiseDispute(t, f.attest(t, 2))

		f.mustResolve(t, d1, true)
		require.Equal(t, types.AttestorStatus_ATTESTOR_STATUS_SLASHED, f.getAttestor(t).Status)

		f.mustResolve(t, d2, false)
		a := f.getAttestor(t)
		require.Zero(t, a.OpenDisputes)
		require.Equal(t, types.AttestorStatus_ATTESTOR_STATUS_SLASHED, a.Status)
		f.requireEscrowInvariant(t)
	})
}

// ---------------------------------------------------------------------------
// exit and bond return
// ---------------------------------------------------------------------------

func TestInitiateAttestorExit_Cooldown(t *testing.T) {
	t.Run("uses exit_cooldown_seconds when it is the longest", func(t *testing.T) {
		f := newActiveFixture(t) // dispute window 7d, cooldown 14d
		_, err := f.msgServer.InitiateAttestorExit(f.ctx, f.exitMsg())
		require.NoError(t, err)

		a := f.getAttestor(t)
		require.Equal(t, types.AttestorStatus_ATTESTOR_STATUS_EXITED, a.Status)
		require.NotNil(t, a.ExitRequestedAt)
		require.True(t, a.ExitRequestedAt.Equal(testBlockTime))
		require.True(t, a.BondReturnAt.Equal(testBlockTime.Add(14*day)))
		require.Equal(t, 1, f.exitQueueLen(t))
	})

	t.Run("uses the longest dispute window of its schemas when that is longer", func(t *testing.T) {
		f := newFixture(t)
		f.bootstrap(t, attestorBond, func(s *types.AttestationSchema) { s.DisputeWindowSeconds = uint64(30 * 24 * 3600) })

		_, err := f.msgServer.InitiateAttestorExit(f.ctx, f.exitMsg())
		require.NoError(t, err)
		require.True(t, f.getAttestor(t).BondReturnAt.Equal(testBlockTime.Add(30*day)))
	})

	t.Run("PENDING attestors can exit", func(t *testing.T) {
		f := newFixture(t)
		f.createSchema(t, defaultSchema(testSchema))
		_, err := f.msgServer.RegisterAttestor(f.ctx, f.registerMsg(t))
		require.NoError(t, err)

		_, err = f.msgServer.InitiateAttestorExit(f.ctx, f.exitMsg())
		require.NoError(t, err)
	})

	t.Run("failures", func(t *testing.T) {
		f := newActiveFixture(t)

		m := f.exitMsg()
		m.Operator = f.strangerS
		_, err := f.msgServer.InitiateAttestorExit(f.ctx, m)
		require.ErrorIs(t, err, types.ErrUnauthorized)

		m = f.exitMsg()
		m.Id = "nope"
		_, err = f.msgServer.InitiateAttestorExit(f.ctx, m)
		require.ErrorIs(t, err, types.ErrAttestorNotFound)

		_, err = f.msgServer.InitiateAttestorExit(f.ctx, f.exitMsg())
		require.NoError(t, err)
		_, err = f.msgServer.InitiateAttestorExit(f.ctx, f.exitMsg())
		require.ErrorIs(t, err, types.ErrInvalidAttestorState)
	})

	t.Run("an exited attestor cannot attest", func(t *testing.T) {
		f := newActiveFixture(t)
		_, err := f.msgServer.InitiateAttestorExit(f.ctx, f.exitMsg())
		require.NoError(t, err)

		_, err = f.msgServer.SubmitAttestation(f.ctx, f.attestMsg(t, 1, 0, 1))
		require.ErrorIs(t, err, types.ErrAttestorNotActive)
	})
}

func TestProcessExits_ReturnsBondAfterCooldown(t *testing.T) {
	f := newActiveFixture(t)
	_, err := f.msgServer.InitiateAttestorExit(f.ctx, f.exitMsg())
	require.NoError(t, err)
	operatorBefore := f.bal(f.operator) // bond is still escrowed

	// One second before the cooldown ends: nothing happens.
	f.advance(14*day - time.Second)
	require.NoError(t, f.keeper.ProcessExits(f.ctx))
	require.Equal(t, operatorBefore, f.bal(f.operator))
	require.Equal(t, attestorBond, f.moduleBal())
	require.Equal(t, 1, f.exitQueueLen(t))

	// At the cooldown: the bond is returned and the entry is removed.
	f.advance(time.Second)
	require.NoError(t, f.keeper.ProcessExits(f.ctx))
	require.Equal(t, operatorBefore+attestorBond, f.bal(f.operator))
	require.Zero(t, f.moduleBal())
	require.Zero(t, f.getAttestor(t).BondAmount.Int64())
	require.Equal(t, types.AttestorStatus_ATTESTOR_STATUS_EXITED, f.getAttestor(t).Status)
	require.Zero(t, f.exitQueueLen(t))
	f.requireEscrowInvariant(t)

	// Running again changes nothing.
	require.NoError(t, f.keeper.ProcessExits(f.ctx))
	require.Equal(t, operatorBefore+attestorBond, f.bal(f.operator))
}

func TestProcessExits_BlockedByOpenDispute(t *testing.T) {
	f := newActiveFixture(t)
	attID := f.attest(t, 1)

	_, err := f.msgServer.InitiateAttestorExit(f.ctx, f.exitMsg())
	require.NoError(t, err)

	// Disputes can still be raised against an exited attestor's attestations,
	// and raising one does not change its EXITED status.
	f.advance(day)
	disputeID := f.raiseDispute(t, attID)
	a := f.getAttestor(t)
	require.Equal(t, types.AttestorStatus_ATTESTOR_STATUS_EXITED, a.Status)
	require.Equal(t, uint64(1), a.OpenDisputes)

	// Past the cooldown, the bond stays put while the dispute is open.
	f.advance(14 * day)
	operatorBefore := f.bal(f.operator)
	require.NoError(t, f.keeper.ProcessExits(f.ctx))
	require.Equal(t, operatorBefore, f.bal(f.operator))
	require.Equal(t, 1, f.exitQueueLen(t))

	// Once resolved, the next pass pays out.
	f.mustResolve(t, disputeID, false)
	require.NoError(t, f.keeper.ProcessExits(f.ctx))
	require.Equal(t, operatorBefore+attestorBond, f.bal(f.operator))
	require.Zero(t, f.exitQueueLen(t))
	f.requireEscrowInvariant(t)
}

func TestProcessExits_UpheldDisputeAfterExitReducesReturnedBond(t *testing.T) {
	f := newActiveFixture(t)
	attID := f.attest(t, 1)
	_, err := f.msgServer.InitiateAttestorExit(f.ctx, f.exitMsg())
	require.NoError(t, err)

	disputeID := f.raiseDispute(t, attID)
	f.mustResolve(t, disputeID, true)

	// An exited attestor stays EXITED when slashed; its bond just shrinks.
	a := f.getAttestor(t)
	require.Equal(t, types.AttestorStatus_ATTESTOR_STATUS_EXITED, a.Status)
	require.Equal(t, attestorBond-100_000, a.BondAmount.Int64())

	operatorBefore := f.bal(f.operator)
	f.advance(14*day + time.Second)
	require.NoError(t, f.keeper.ProcessExits(f.ctx))

	require.Equal(t, operatorBefore+attestorBond-100_000, f.bal(f.operator))
	require.Zero(t, f.moduleBal())
	f.requireEscrowInvariant(t)
}

func TestProcessExits_BankFailureIsSkippedAndRetried(t *testing.T) {
	f := newActiveFixture(t)
	_, err := f.msgServer.InitiateAttestorExit(f.ctx, f.exitMsg())
	require.NoError(t, err)
	f.advance(15 * day)
	operatorBefore := f.bal(f.operator)

	// A failing payout must not return an error (that would halt the chain)
	// and must leave the attestor and queue entry untouched.
	f.bank.failFromModule = true
	require.NoError(t, f.keeper.ProcessExits(f.ctx))
	require.Equal(t, operatorBefore, f.bal(f.operator))
	require.Equal(t, attestorBond, f.getAttestor(t).BondAmount.Int64())
	require.Equal(t, 1, f.exitQueueLen(t))

	// Once the bank recovers, the next pass pays out.
	f.bank.failFromModule = false
	require.NoError(t, f.keeper.ProcessExits(f.ctx))
	require.Equal(t, operatorBefore+attestorBond, f.bal(f.operator))
	require.Zero(t, f.exitQueueLen(t))
}

// ---------------------------------------------------------------------------
// queries
// ---------------------------------------------------------------------------

func TestQuery_NotFoundAndInvalidArgument(t *testing.T) {
	f := newActiveFixture(t)
	ctx := f.ctx

	_, err := f.queryServer.Attestor(ctx, &types.QueryAttestorRequest{Id: "nope"})
	require.Equal(t, codes.NotFound, status.Code(err))
	_, err = f.queryServer.Attestor(ctx, &types.QueryAttestorRequest{})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = f.queryServer.Attestor(ctx, nil)
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	_, err = f.queryServer.Schema(ctx, &types.QuerySchemaRequest{Id: "nope"})
	require.Equal(t, codes.NotFound, status.Code(err))
	_, err = f.queryServer.Schema(ctx, &types.QuerySchemaRequest{})
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	_, err = f.queryServer.Attestation(ctx, &types.QueryAttestationRequest{Id: "nope"})
	require.Equal(t, codes.NotFound, status.Code(err))
	_, err = f.queryServer.Attestation(ctx, &types.QueryAttestationRequest{})
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	_, err = f.queryServer.Dispute(ctx, &types.QueryDisputeRequest{Id: "nope"})
	require.Equal(t, codes.NotFound, status.Code(err))
	_, err = f.queryServer.Dispute(ctx, &types.QueryDisputeRequest{})
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	_, err = f.queryServer.AttestationsForCheckpoint(ctx, &types.QueryAttestationsForCheckpointRequest{SidechainId: testSidechain})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = f.queryServer.Params(ctx, nil)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestQuery_ByIDAndParams(t *testing.T) {
	f := newActiveFixture(t)
	attID := f.attest(t, 1)
	disputeID := f.raiseDispute(t, attID)

	a, err := f.queryServer.Attestor(f.ctx, &types.QueryAttestorRequest{Id: testAttestor})
	require.NoError(t, err)
	require.Equal(t, testAttestor, a.Attestor.Id)
	require.Len(t, a.Attestor.SignerKeys, 3)

	s, err := f.queryServer.Schema(f.ctx, &types.QuerySchemaRequest{Id: testSchema})
	require.NoError(t, err)
	require.Equal(t, testSchema, s.Schema.Id)

	att, err := f.queryServer.Attestation(f.ctx, &types.QueryAttestationRequest{Id: attID})
	require.NoError(t, err)
	require.Equal(t, types.AttestationStatus_ATTESTATION_STATUS_DISPUTED, att.Attestation.Status)

	d, err := f.queryServer.Dispute(f.ctx, &types.QueryDisputeRequest{Id: disputeID})
	require.NoError(t, err)
	require.Equal(t, attID, d.Dispute.AttestationId)

	p, err := f.queryServer.Params(f.ctx, &types.QueryParamsRequest{})
	require.NoError(t, err)
	require.Equal(t, uint32(4096), p.Params.MaxClaimPayloadBytes)
}

func TestQuery_FiltersAndPagination(t *testing.T) {
	f := newActiveFixture(t) // attestor 1: ACTIVE, schema domain "hospitality"

	// Second schema in another domain, and a second (PENDING) attestor for it.
	other := defaultSchema("tourism.report.v1")
	other.Domain = "tourism"
	f.createSchema(t, other)

	m := f.registerMsg(t)
	m.Id = "second-attestor"
	m.AuthorizedSchemaIds = []string{"tourism.report.v1"}
	_, err := f.msgServer.RegisterAttestor(f.ctx, m)
	require.NoError(t, err)

	ids := func(res *types.QueryAttestorsResponse) []string {
		var out []string
		for _, a := range res.Attestors {
			out = append(out, a.Id)
		}
		return out
	}
	list := func(req *types.QueryAttestorsRequest) []string {
		res, err := f.queryServer.Attestors(f.ctx, req)
		require.NoError(t, err)
		return ids(res)
	}

	require.ElementsMatch(t, []string{testAttestor, "second-attestor"}, list(&types.QueryAttestorsRequest{}))
	require.Equal(t, []string{testAttestor}, list(&types.QueryAttestorsRequest{Domain: "hospitality"}))
	require.Equal(t, []string{"second-attestor"}, list(&types.QueryAttestorsRequest{Domain: "tourism"}))
	require.Empty(t, list(&types.QueryAttestorsRequest{Domain: "nonexistent"}))
	require.Equal(t, []string{testAttestor}, list(&types.QueryAttestorsRequest{Status: types.AttestorStatus_ATTESTOR_STATUS_ACTIVE}))
	require.Equal(t, []string{"second-attestor"}, list(&types.QueryAttestorsRequest{Status: types.AttestorStatus_ATTESTOR_STATUS_PENDING}))
	require.Empty(t, list(&types.QueryAttestorsRequest{Domain: "tourism", Status: types.AttestorStatus_ATTESTOR_STATUS_ACTIVE}))

	schemas, err := f.queryServer.Schemas(f.ctx, &types.QuerySchemasRequest{})
	require.NoError(t, err)
	require.Len(t, schemas.Schemas, 2)
	schemas, err = f.queryServer.Schemas(f.ctx, &types.QuerySchemasRequest{Domain: "tourism"})
	require.NoError(t, err)
	require.Len(t, schemas.Schemas, 1)
	require.Equal(t, "tourism.report.v1", schemas.Schemas[0].Id)
}

func TestQuery_AttestationsForCheckpointPagination(t *testing.T) {
	f := newActiveFixture(t)

	// Two attestors attest the same checkpoint.
	m := f.registerMsg(t)
	m.Id = "second-attestor"
	_, err := f.msgServer.RegisterAttestor(f.ctx, m)
	require.NoError(t, err)
	f.activate(t, "second-attestor")

	first := f.attest(t, 1)
	msg2 := f.attestMsg(t, 1, 0, 1)
	msg2.AttestorId = "second-attestor"
	msg2.SignerSetVersion = 1
	f.signAttestation(t, msg2, testChainID, f.cpHash(1), 0, 1)
	resp2, err := f.msgServer.SubmitAttestation(f.ctx, msg2)
	require.NoError(t, err)
	f.attest(t, 2) // different checkpoint, must not show up

	req := &types.QueryAttestationsForCheckpointRequest{SidechainId: testSidechain, CheckpointSequence: 1}

	all, err := f.queryServer.AttestationsForCheckpoint(f.ctx, req)
	require.NoError(t, err)
	require.Len(t, all.Attestations, 2)

	// Page through one at a time.
	req.Pagination = &query.PageRequest{Limit: 1}
	page1, err := f.queryServer.AttestationsForCheckpoint(f.ctx, req)
	require.NoError(t, err)
	require.Len(t, page1.Attestations, 1)
	require.NotEmpty(t, page1.Pagination.NextKey)

	req.Pagination = &query.PageRequest{Limit: 1, Key: page1.Pagination.NextKey}
	page2, err := f.queryServer.AttestationsForCheckpoint(f.ctx, req)
	require.NoError(t, err)
	require.Len(t, page2.Attestations, 1)

	got := []string{page1.Attestations[0].Id, page2.Attestations[0].Id}
	require.ElementsMatch(t, []string{first, resp2.AttestationId}, got)

	// Unknown checkpoint: empty, not an error.
	none, err := f.queryServer.AttestationsForCheckpoint(f.ctx, &types.QueryAttestationsForCheckpointRequest{
		SidechainId: testSidechain, CheckpointSequence: 42,
	})
	require.NoError(t, err)
	require.Empty(t, none.Attestations)
}

// ---------------------------------------------------------------------------
// genesis
// ---------------------------------------------------------------------------

func TestGenesis_DefaultRoundTrip(t *testing.T) {
	f := newFixture(t)
	gs := types.DefaultGenesis()
	gs.Params.AllowedPubkeyTypeUrls = []string{sdk.MsgTypeURL(&ed25519.PubKey{})}
	require.NoError(t, gs.Validate())

	require.NoError(t, f.keeper.InitGenesis(f.ctx, *gs))
	got, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.True(t, gs.Params.Equal(got.Params))
	require.Empty(t, got.Attestors)
	require.Empty(t, got.Schemas)
	require.Empty(t, got.Attestations)
	require.Empty(t, got.Disputes)
}

func TestGenesis_RoundTripRebuildsIndexes(t *testing.T) {
	f := newActiveFixture(t)
	att1 := f.attest(t, 1)
	f.attest(t, 2)
	disputeID := f.raiseDispute(t, att1) // open dispute at export time
	f.requireEscrowInvariant(t)

	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.NoError(t, exported.Validate())
	require.Len(t, exported.Attestors, 1)
	require.Len(t, exported.Schemas, 1)
	require.Len(t, exported.Attestations, 2)
	require.Len(t, exported.Disputes, 1)

	// Import into a fresh chain whose module account is funded as bank genesis would.
	g := newFixture(t)
	g.bank.modules[types.ModuleName] = f.bank.moduleBalance(types.ModuleName)
	require.NoError(t, g.keeper.InitGenesis(g.ctx, *exported))
	g.requireEscrowInvariant(t)

	re, err := g.keeper.ExportGenesis(g.ctx)
	require.NoError(t, err)
	require.True(t, exported.Params.Equal(re.Params))
	require.Len(t, re.Attestors, 1)
	require.True(t, exported.Attestors[0].Equal(re.Attestors[0]))
	require.Len(t, re.Schemas, 1)
	require.True(t, exported.Schemas[0].Equal(re.Schemas[0]))
	require.Len(t, re.Attestations, 2)
	for i := range exported.Attestations {
		require.True(t, exported.Attestations[i].Equal(re.Attestations[i]))
	}
	require.Len(t, re.Disputes, 1)
	require.True(t, exported.Disputes[0].Equal(re.Disputes[0]))

	// The by-checkpoint index was rebuilt.
	res, err := g.queryServer.AttestationsForCheckpoint(g.ctx, &types.QueryAttestationsForCheckpointRequest{
		SidechainId: testSidechain, CheckpointSequence: 1,
	})
	require.NoError(t, err)
	require.Len(t, res.Attestations, 1)
	require.Equal(t, att1, res.Attestations[0].Id)

	// The imported open dispute can be resolved.
	g.mustResolve(t, disputeID, false)
	require.Zero(t, g.getAttestor(t).OpenDisputes)
	g.requireEscrowInvariant(t)
}

func TestGenesis_RoundTripRebuildsExitQueue(t *testing.T) {
	f := newActiveFixture(t)
	_, err := f.msgServer.InitiateAttestorExit(f.ctx, f.exitMsg())
	require.NoError(t, err)

	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.NoError(t, exported.Validate())

	g := newFixture(t)
	g.bank.modules[types.ModuleName] = f.bank.moduleBalance(types.ModuleName)
	require.NoError(t, g.keeper.InitGenesis(g.ctx, *exported))
	require.Equal(t, 1, g.exitQueueLen(t))

	operatorBefore := g.bal(g.operator)
	g.advance(15 * day)
	require.NoError(t, g.keeper.ProcessExits(g.ctx))
	require.Equal(t, operatorBefore+attestorBond, g.bal(g.operator))
	require.Zero(t, g.exitQueueLen(t))
	g.requireEscrowInvariant(t)
}

func TestGenesisValidate_RejectsInconsistentState(t *testing.T) {
	f := newActiveFixture(t)
	att1 := f.attest(t, 1)
	f.raiseDispute(t, att1)

	base, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.NoError(t, base.Validate())

	clone := func() types.GenesisState {
		out, err := f.keeper.ExportGenesis(f.ctx)
		require.NoError(t, err)
		return *out
	}

	cases := map[string]func(gs *types.GenesisState){
		"attestation id does not match its fields":  func(gs *types.GenesisState) { gs.Attestations[0].Id = "bogus" },
		"dispute id does not match its attestation": func(gs *types.GenesisState) { gs.Disputes[0].Id = "bogus" },
		"attestation references unknown attestor":   func(gs *types.GenesisState) { gs.Attestations[0].AttestorId = "nope" },
		"attestor references unknown schema":        func(gs *types.GenesisState) { gs.Attestors[0].AuthorizedSchemaIds = []string{"nope.v1"} },
		"open_disputes disagrees with disputes":     func(gs *types.GenesisState) { gs.Attestors[0].OpenDisputes = 5 },
		"duplicate schema":                          func(gs *types.GenesisState) { gs.Schemas = append(gs.Schemas, gs.Schemas[0]) },
		"duplicate attestor":                        func(gs *types.GenesisState) { gs.Attestors = append(gs.Attestors, gs.Attestors[0]) },
		"open dispute but attestation not disputed": func(gs *types.GenesisState) {
			for i := range gs.Attestations {
				if gs.Attestations[i].Id == att1 {
					gs.Attestations[i].Status = types.AttestationStatus_ATTESTATION_STATUS_ACTIVE
				}
			}
		},
		"EXITED attestor without bond_return_at": func(gs *types.GenesisState) {
			gs.Attestors[0].Status = types.AttestorStatus_ATTESTOR_STATUS_EXITED
			gs.Attestors[0].BondReturnAt = nil
		},
		"negative bond": func(gs *types.GenesisState) { gs.Attestors[0].BondAmount = math.NewInt(-1) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			gs := clone()
			mutate(&gs)
			require.ErrorIs(t, gs.Validate(), types.ErrInvalidGenesis)
		})
	}
}

// ---------------------------------------------------------------------------
// authority checks shared by governance-only messages
// ---------------------------------------------------------------------------

func TestGovernanceOnlyMessagesRejectOtherAuthorities(t *testing.T) {
	f := newActiveFixture(t)
	s := defaultSchema("another.v1")

	for _, authority := range []string{f.strangerS, f.operatorS} {
		_, err := f.msgServer.CreateAttestationSchema(f.ctx, &types.MsgCreateAttestationSchema{Authority: authority, Schema: &s})
		require.ErrorIs(t, err, types.ErrUnauthorized)

		_, err = f.msgServer.RetireAttestationSchema(f.ctx, &types.MsgRetireAttestationSchema{Authority: authority, SchemaId: testSchema})
		require.ErrorIs(t, err, types.ErrUnauthorized)

		_, err = f.msgServer.ActivateAttestor(f.ctx, &types.MsgActivateAttestor{Authority: authority, Id: testAttestor})
		require.ErrorIs(t, err, types.ErrUnauthorized)

		_, err = f.msgServer.ResolveDispute(f.ctx, &types.MsgResolveDispute{Authority: authority, DisputeId: "x"})
		require.ErrorIs(t, err, types.ErrUnauthorized)
	}
}
