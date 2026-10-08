package keeper_test

import (
	"bytes"
	"context"
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

	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"

	"github.com/cosmos/cosmos-sdk/codec"
	addresscodec "github.com/cosmos/cosmos-sdk/codec/address"
	codectestutil "github.com/cosmos/cosmos-sdk/codec/testutil"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/cosmos/cosmos-sdk/types/query"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/lucidpay/lucidchain/x/sidechain/keeper"
	"github.com/lucidpay/lucidchain/x/sidechain/types"
)

// NOTE: the tests use ed25519 signer keys and set allowed_pubkey_type_urls
// accordingly. The keeper only validates key types and stores the keys, so
// this exercises the same code paths as ML-DSA-65.

const (
	testChainID = "lucidchain-test-1"
	testID      = "hospitality-platform-01"
	testHeight  = int64(100)

	tier1Bond  = int64(1_000_000)
	tier2Bond  = int64(5_000_000)
	tier3Bond  = int64(10_000_000)
	startFunds = int64(100_000_000)

	day = 24 * time.Hour
)

var testBlockTime = time.Unix(1_700_000_000, 0).UTC()

// ---------------------------------------------------------------------------
// bank mock
// ---------------------------------------------------------------------------

// mockBank is a single-denom ledger so tests can assert exact bond movements.
type mockBank struct {
	accounts map[string]math.Int
	modules  map[string]math.Int

	failToModule   bool
	failFromModule bool
}

var _ types.BankKeeper = (*mockBank)(nil)

func newMockBank() *mockBank {
	return &mockBank{accounts: map[string]math.Int{}, modules: map[string]math.Int{}}
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

func singleAmount(coins sdk.Coins) (math.Int, error) {
	if len(coins) != 1 || coins[0].Denom != sdk.DefaultBondDenom {
		return math.Int{}, fmt.Errorf("unexpected coins %s", coins)
	}
	return coins[0].Amount, nil
}

// SpendableCoins satisfies the scaffolded BankKeeper interface (used by the
// module's AppModule, not by the keeper).
func (b *mockBank) SpendableCoins(_ context.Context, addr sdk.AccAddress) sdk.Coins {
	return sdk.NewCoins(sdk.NewCoin(sdk.DefaultBondDenom, b.balance(addr)))
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

// ---------------------------------------------------------------------------
// fixture
// ---------------------------------------------------------------------------

type fixture struct {
	ctx          sdk.Context
	keeper       keeper.Keeper
	addressCodec address.Codec
	msgServer    types.MsgServer
	queryServer  types.QueryServer
	bank         *mockBank
	signers      []*ed25519.PrivKey

	gov, operator, stranger    sdk.AccAddress
	govS, operatorS, strangerS string
}

// initFixture keeps Ignite's scaffolded tests working.
func initFixture(t *testing.T) *fixture { return newFixture(t) }

// newFixture builds a keeper with default params (ed25519 allowed, governance
// activation required), funded accounts, and three signer keys. Nothing is
// registered yet.
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
		bank:         newMockBank(),
		signers:      make([]*ed25519.PrivKey, 3),
		gov:          authtypes.NewModuleAddress(types.GovModuleName),
		operator:     mk(1),
		stranger:     mk(3),
	}
	for i := range f.signers {
		f.signers[i] = ed25519.GenPrivKey()
	}
	f.govS, f.operatorS, f.strangerS = str(f.gov), str(f.operator), str(f.stranger)
	f.bank.fund(f.operator, startFunds)
	f.bank.fund(f.stranger, startFunds)

	f.keeper = keeper.NewKeeper(runtime.NewKVStoreService(key), cdc, addrCodec, f.gov, f.bank)
	f.msgServer = keeper.NewMsgServerImpl(f.keeper)
	f.queryServer = keeper.NewQueryServerImpl(f.keeper)

	p := types.DefaultParams()
	p.AllowedPubkeyTypeUrls = []string{sdk.MsgTypeURL(&ed25519.PubKey{})}
	require.NoError(t, f.keeper.SetParams(f.ctx, p))

	return f
}

// newActiveFixture registers the default sidechain and activates it.
func newActiveFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.register(t)
	f.activate(t, testID)
	return f
}

func (f *fixture) keyAnys(t *testing.T, idx ...int) []*codectypes.Any {
	t.Helper()
	out := make([]*codectypes.Any, len(idx))
	for i, k := range idx {
		a, err := codectypes.NewAnyWithValue(f.signers[k].PubKey())
		require.NoError(t, err)
		out[i] = a
	}
	return out
}

func (f *fixture) registerMsg(t *testing.T) *types.MsgRegisterSidechain {
	t.Helper()
	return &types.MsgRegisterSidechain{
		Operator:                  f.operatorS,
		Id:                        testID,
		Name:                      "Hospitality Platform",
		SignerKeys:                f.keyAnys(t, 0, 1, 2),
		SignatureThreshold:        2,
		Tier:                      types.AssuranceTier_ASSURANCE_TIER_NOTARIZED,
		Bond:                      sdk.NewInt64Coin(sdk.DefaultBondDenom, tier1Bond),
		CheckpointIntervalSeconds: 300,
		MetadataUri:               "https://example.org/meta.json",
	}
}

func (f *fixture) register(t *testing.T) {
	t.Helper()
	_, err := f.msgServer.RegisterSidechain(f.ctx, f.registerMsg(t))
	require.NoError(t, err)
}

func (f *fixture) activate(t *testing.T, id string) {
	t.Helper()
	_, err := f.msgServer.ActivateSidechain(f.ctx, &types.MsgActivateSidechain{Authority: f.govS, Id: id})
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

func (f *fixture) get(t *testing.T, id string) types.Sidechain {
	t.Helper()
	sc, err := f.keeper.GetSidechain(f.ctx, id)
	require.NoError(t, err)
	return sc
}

func (f *fixture) setStatus(t *testing.T, id string, st types.SidechainStatus) {
	t.Helper()
	sc := f.get(t, id)
	sc.Status = st
	require.NoError(t, f.keeper.SetSidechain(f.ctx, sc))
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

func (f *fixture) exitMsg() *types.MsgInitiateSidechainExit {
	return &types.MsgInitiateSidechainExit{Operator: f.operatorS, Id: testID}
}

// requireEscrowInvariant checks that the module account holds exactly the sum
// of all sidechain bonds.
func (f *fixture) requireEscrowInvariant(t *testing.T) {
	t.Helper()
	want := math.ZeroInt()
	require.NoError(t, f.keeper.Sidechains.Walk(f.ctx, nil, func(_ string, sc types.Sidechain) (bool, error) {
		want = want.Add(sc.Bond.Amount)
		return false, nil
	}))
	got := f.bank.moduleBalance(types.ModuleName)
	require.True(t, want.Equal(got), "module account holds %s but bonds sum to %s", got, want)
}

// ---------------------------------------------------------------------------
// params
// ---------------------------------------------------------------------------

func TestUpdateParams(t *testing.T) {
	f := newFixture(t)

	valid := types.DefaultParams()
	valid.AllowedPubkeyTypeUrls = []string{sdk.MsgTypeURL(&ed25519.PubKey{})}
	valid.MaxSignerKeys = 5

	t.Run("wrong authority", func(t *testing.T) {
		_, err := f.msgServer.UpdateParams(f.ctx, &types.MsgUpdateParams{Authority: f.strangerS, Params: valid})
		require.ErrorIs(t, err, types.ErrUnauthorized)
	})

	t.Run("malformed authority", func(t *testing.T) {
		_, err := f.msgServer.UpdateParams(f.ctx, &types.MsgUpdateParams{Authority: "bogus", Params: valid})
		require.ErrorIs(t, err, sdkerrors.ErrInvalidAddress)
	})

	invalid := map[string]func(p *types.Params){
		"no tiers":           func(p *types.Params) { p.MinBondByTier = nil },
		"unspecified tier":   func(p *types.Params) { p.MinBondByTier[0].Tier = types.AssuranceTier_ASSURANCE_TIER_UNSPECIFIED },
		"duplicate tier":     func(p *types.Params) { p.MinBondByTier[1].Tier = p.MinBondByTier[0].Tier },
		"zero min bond":      func(p *types.Params) { p.MinBondByTier[0].MinBond = sdk.NewInt64Coin(sdk.DefaultBondDenom, 0) },
		"zero cooldown":      func(p *types.Params) { p.ExitCooldownSeconds = 0 },
		"zero max keys":      func(p *types.Params) { p.MaxSignerKeys = 0 },
		"no allowed types":   func(p *types.Params) { p.AllowedPubkeyTypeUrls = nil },
		"type without slash": func(p *types.Params) { p.AllowedPubkeyTypeUrls = []string{"cosmos.crypto.ed25519.PubKey"} },
		"duplicate type": func(p *types.Params) {
			p.AllowedPubkeyTypeUrls = []string{"/a.B", "/a.B"}
		},
	}
	for name, mutate := range invalid {
		t.Run("invalid: "+name, func(t *testing.T) {
			p := types.DefaultParams()
			p.AllowedPubkeyTypeUrls = valid.AllowedPubkeyTypeUrls
			mutate(&p)
			_, err := f.msgServer.UpdateParams(f.ctx, &types.MsgUpdateParams{Authority: f.govS, Params: p})
			require.ErrorIs(t, err, sdkerrors.ErrInvalidRequest)
		})
	}

	t.Run("valid update", func(t *testing.T) {
		_, err := f.msgServer.UpdateParams(f.ctx, &types.MsgUpdateParams{Authority: f.govS, Params: valid})
		require.NoError(t, err)
		got, err := f.keeper.GetParams(f.ctx)
		require.NoError(t, err)
		require.Equal(t, uint32(5), got.MaxSignerKeys)
	})
}

// ---------------------------------------------------------------------------
// registration
// ---------------------------------------------------------------------------

func TestRegisterSidechain_Success(t *testing.T) {
	f := newFixture(t)

	resp, err := f.msgServer.RegisterSidechain(f.ctx, f.registerMsg(t))
	require.NoError(t, err)
	require.Equal(t, testID, resp.Id)
	require.Equal(t, types.SidechainStatus_SIDECHAIN_STATUS_PENDING, resp.Status)

	sc := f.get(t, testID)
	require.Equal(t, "Hospitality Platform", sc.Name)
	require.Equal(t, f.operatorS, sc.Operator)
	require.Equal(t, types.SidechainStatus_SIDECHAIN_STATUS_PENDING, sc.Status)
	require.Equal(t, types.AssuranceTier_ASSURANCE_TIER_NOTARIZED, sc.Tier)
	require.Equal(t, uint64(1), sc.SignerSetVersion)
	require.Equal(t, uint32(2), sc.SignatureThreshold)
	require.Len(t, sc.SignerKeys, 3)
	require.Equal(t, tier1Bond, sc.Bond.Amount.Int64())
	require.Equal(t, sdk.DefaultBondDenom, sc.Bond.Denom)
	require.Equal(t, uint64(300), sc.CheckpointIntervalSeconds)
	require.Equal(t, "https://example.org/meta.json", sc.MetadataUri)
	require.True(t, sc.RegisteredAt.Equal(testBlockTime))
	require.Nil(t, sc.ActivatedAt)
	require.Nil(t, sc.ExitRequestedAt)
	require.Nil(t, sc.BondReturnAt)
	require.Zero(t, sc.LastCheckpointSequence)
	require.Empty(t, sc.LastCheckpointHash)

	require.Equal(t, startFunds-tier1Bond, f.bal(f.operator))
	require.Equal(t, tier1Bond, f.moduleBal())
	f.requireEscrowInvariant(t)
}

func TestRegisterSidechain_StoredSignerKeysAreUnpacked(t *testing.T) {
	// x/checkpoint reads GetCachedValue() on these keys to verify signatures.
	f := newFixture(t)
	f.register(t)

	sc := f.get(t, testID)
	for i, a := range sc.SignerKeys {
		pk, ok := a.GetCachedValue().(cryptotypes.PubKey)
		require.True(t, ok, "signer key %d is not unpacked after a store round trip", i)
		require.True(t, pk.Equals(f.signers[i].PubKey()))
	}
}

func TestRegisterSidechain_AutoActivate(t *testing.T) {
	f := newFixture(t)
	f.setParams(t, func(p *types.Params) { p.AutoActivate = true })

	resp, err := f.msgServer.RegisterSidechain(f.ctx, f.registerMsg(t))
	require.NoError(t, err)
	require.Equal(t, types.SidechainStatus_SIDECHAIN_STATUS_ACTIVE, resp.Status)

	sc := f.get(t, testID)
	require.Equal(t, types.SidechainStatus_SIDECHAIN_STATUS_ACTIVE, sc.Status)
	require.NotNil(t, sc.ActivatedAt)
	require.True(t, sc.ActivatedAt.Equal(testBlockTime))

	// ACTIVE straight away: it can take a checkpoint.
	require.NoError(t, f.keeper.RecordCheckpoint(f.ctx, testID, 1, testHeight, []byte("hash-1")))
}

func TestRegisterSidechain_PerTierBond(t *testing.T) {
	cases := []struct {
		name  string
		tier  types.AssuranceTier
		bond  int64
		valid bool
	}{
		{"tier 1 exact minimum", types.AssuranceTier_ASSURANCE_TIER_NOTARIZED, tier1Bond, true},
		{"tier 1 below minimum", types.AssuranceTier_ASSURANCE_TIER_NOTARIZED, tier1Bond - 1, false},
		{"tier 2 exact minimum", types.AssuranceTier_ASSURANCE_TIER_ATTESTED, tier2Bond, true},
		{"tier 2 with the tier 1 bond", types.AssuranceTier_ASSURANCE_TIER_ATTESTED, tier1Bond, false},
		{"tier 3 above minimum", types.AssuranceTier_ASSURANCE_TIER_PROVEN, tier3Bond + 7, true},
		{"tier 3 with the tier 2 bond", types.AssuranceTier_ASSURANCE_TIER_PROVEN, tier2Bond, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			m := f.registerMsg(t)
			m.Tier = tc.tier
			m.Bond = sdk.NewInt64Coin(sdk.DefaultBondDenom, tc.bond)

			_, err := f.msgServer.RegisterSidechain(f.ctx, m)
			if tc.valid {
				require.NoError(t, err)
				// The full bond is escrowed and recorded, not just the minimum.
				require.Equal(t, tc.bond, f.get(t, testID).Bond.Amount.Int64())
				require.Equal(t, tc.bond, f.moduleBal())
			} else {
				require.ErrorIs(t, err, types.ErrInsufficientBond)
				require.Zero(t, f.moduleBal())
			}
		})
	}
}

func TestRegisterSidechain_TierWithoutMinBond(t *testing.T) {
	f := newFixture(t)
	f.setParams(t, func(p *types.Params) { p.MinBondByTier = p.MinBondByTier[:1] }) // only tier 1

	m := f.registerMsg(t)
	m.Tier = types.AssuranceTier_ASSURANCE_TIER_PROVEN
	m.Bond = sdk.NewInt64Coin(sdk.DefaultBondDenom, tier3Bond)
	_, err := f.msgServer.RegisterSidechain(f.ctx, m)
	require.ErrorIs(t, err, types.ErrTierNotAvailable)
}

func TestRegisterSidechain_Failures(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(t *testing.T, f *fixture, m *types.MsgRegisterSidechain)
		want    error
		wantMsg string
	}{
		{"uppercase id", func(t *testing.T, f *fixture, m *types.MsgRegisterSidechain) { m.Id = "Bad ID" }, types.ErrInvalidRequest, ""},
		{"slash in id", func(t *testing.T, f *fixture, m *types.MsgRegisterSidechain) { m.Id = "a/b" }, types.ErrInvalidRequest, ""},
		{"empty id", func(t *testing.T, f *fixture, m *types.MsgRegisterSidechain) { m.Id = "" }, types.ErrInvalidRequest, ""},
		{"id too long", func(t *testing.T, f *fixture, m *types.MsgRegisterSidechain) {
			m.Id = strings.Repeat("a", types.MaxIDBytes+1)
		}, types.ErrLimitExceeded, ""},
		{"empty name", func(t *testing.T, f *fixture, m *types.MsgRegisterSidechain) { m.Name = "" }, types.ErrInvalidRequest, ""},
		{"name too long", func(t *testing.T, f *fixture, m *types.MsgRegisterSidechain) {
			m.Name = strings.Repeat("n", types.MaxNameBytes+1)
		}, types.ErrLimitExceeded, ""},
		{"metadata uri too long", func(t *testing.T, f *fixture, m *types.MsgRegisterSidechain) {
			m.MetadataUri = strings.Repeat("u", types.MaxURIBytes+1)
		}, types.ErrLimitExceeded, ""},
		{"zero checkpoint interval", func(t *testing.T, f *fixture, m *types.MsgRegisterSidechain) {
			m.CheckpointIntervalSeconds = 0
		}, types.ErrInvalidRequest, ""},
		{"absurd checkpoint interval", func(t *testing.T, f *fixture, m *types.MsgRegisterSidechain) {
			m.CheckpointIntervalSeconds = types.MaxPeriodSeconds + 1
		}, types.ErrInvalidRequest, ""},
		{"unspecified tier", func(t *testing.T, f *fixture, m *types.MsgRegisterSidechain) {
			m.Tier = types.AssuranceTier_ASSURANCE_TIER_UNSPECIFIED
		}, types.ErrInvalidRequest, ""},
		{"unknown tier", func(t *testing.T, f *fixture, m *types.MsgRegisterSidechain) {
			m.Tier = types.AssuranceTier(99)
		}, types.ErrInvalidRequest, ""},
		{"no signer keys", func(t *testing.T, f *fixture, m *types.MsgRegisterSidechain) { m.SignerKeys = nil }, types.ErrInvalidSigners, ""},
		{"zero threshold", func(t *testing.T, f *fixture, m *types.MsgRegisterSidechain) { m.SignatureThreshold = 0 }, types.ErrInvalidSigners, ""},
		{"threshold above key count", func(t *testing.T, f *fixture, m *types.MsgRegisterSidechain) {
			m.SignatureThreshold = 4
		}, types.ErrInvalidSigners, ""},
		{"more keys than max_signer_keys", func(t *testing.T, f *fixture, m *types.MsgRegisterSidechain) {
			f.setParams(t, func(p *types.Params) { p.MaxSignerKeys = 2 })
		}, types.ErrInvalidSigners, ""},
		{"duplicate signer key", func(t *testing.T, f *fixture, m *types.MsgRegisterSidechain) {
			m.SignerKeys = f.keyAnys(t, 0, 0)
		}, types.ErrInvalidSigners, ""},
		{"key type not allowed", func(t *testing.T, f *fixture, m *types.MsgRegisterSidechain) {
			a, err := codectypes.NewAnyWithValue(secp256k1.GenPrivKey().PubKey())
			require.NoError(t, err)
			m.SignerKeys = []*codectypes.Any{a}
			m.SignatureThreshold = 1
		}, types.ErrInvalidSigners, ""},
		{"bond in the wrong denom", func(t *testing.T, f *fixture, m *types.MsgRegisterSidechain) {
			m.Bond = sdk.NewInt64Coin("uother", tier1Bond)
		}, types.ErrInsufficientBond, ""},
		{"bond below the tier minimum", func(t *testing.T, f *fixture, m *types.MsgRegisterSidechain) {
			m.Bond = sdk.NewInt64Coin(sdk.DefaultBondDenom, tier1Bond-1)
		}, types.ErrInsufficientBond, ""},
		{"unset bond", func(t *testing.T, f *fixture, m *types.MsgRegisterSidechain) { m.Bond = sdk.Coin{} }, types.ErrInsufficientBond, ""},
		{"malformed operator", func(t *testing.T, f *fixture, m *types.MsgRegisterSidechain) { m.Operator = "bogus" }, sdkerrors.ErrInvalidAddress, ""},
		{"operator cannot afford the bond", func(t *testing.T, f *fixture, m *types.MsgRegisterSidechain) {
			m.Bond = sdk.NewInt64Coin(sdk.DefaultBondDenom, startFunds+1)
		}, nil, "insufficient funds"},
		{"bank failure", func(t *testing.T, f *fixture, m *types.MsgRegisterSidechain) {
			f.bank.failToModule = true
		}, nil, "send to module failed"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			m := f.registerMsg(t)
			tc.mutate(t, f, m)

			_, err := f.msgServer.RegisterSidechain(f.ctx, m)
			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)
			} else {
				require.ErrorContains(t, err, tc.wantMsg)
			}

			// Nothing was stored and nothing was escrowed.
			has, herr := f.keeper.Sidechains.Has(f.ctx, m.Id)
			require.NoError(t, herr)
			require.False(t, has)
			require.Zero(t, f.moduleBal())
			require.Equal(t, startFunds, f.bal(f.operator))
		})
	}
}

func TestRegisterSidechain_Duplicate(t *testing.T) {
	f := newFixture(t)
	f.register(t)

	_, err := f.msgServer.RegisterSidechain(f.ctx, f.registerMsg(t))
	require.ErrorIs(t, err, types.ErrSidechainExists)
	require.Equal(t, tier1Bond, f.moduleBal()) // the second bond was not taken
}

func TestRegisterSidechain_ReusingAnExitedIdIsRejected(t *testing.T) {
	f := newActiveFixture(t)
	_, err := f.msgServer.InitiateSidechainExit(f.ctx, f.exitMsg())
	require.NoError(t, err)

	// The record is kept after exit, so ids are never recycled (checkpoints,
	// attestations and proofs refer to them).
	_, err = f.msgServer.RegisterSidechain(f.ctx, f.registerMsg(t))
	require.ErrorIs(t, err, types.ErrSidechainExists)
}

// ---------------------------------------------------------------------------
// activation, suspension
// ---------------------------------------------------------------------------

func TestActivateSidechain(t *testing.T) {
	f := newFixture(t)
	f.register(t)

	_, err := f.msgServer.ActivateSidechain(f.ctx, &types.MsgActivateSidechain{Authority: f.strangerS, Id: testID})
	require.ErrorIs(t, err, types.ErrUnauthorized)
	_, err = f.msgServer.ActivateSidechain(f.ctx, &types.MsgActivateSidechain{Authority: f.operatorS, Id: testID})
	require.ErrorIs(t, err, types.ErrUnauthorized, "the operator cannot activate its own sidechain")
	_, err = f.msgServer.ActivateSidechain(f.ctx, &types.MsgActivateSidechain{Authority: "bogus", Id: testID})
	require.ErrorIs(t, err, sdkerrors.ErrInvalidAddress)
	_, err = f.msgServer.ActivateSidechain(f.ctx, &types.MsgActivateSidechain{Authority: f.govS, Id: "nope"})
	require.ErrorIs(t, err, types.ErrSidechainNotFound)

	f.advance(time.Hour)
	f.activate(t, testID)
	sc := f.get(t, testID)
	require.Equal(t, types.SidechainStatus_SIDECHAIN_STATUS_ACTIVE, sc.Status)
	require.NotNil(t, sc.ActivatedAt)
	require.True(t, sc.ActivatedAt.Equal(testBlockTime.Add(time.Hour)))

	// Only PENDING sidechains can be activated.
	_, err = f.msgServer.ActivateSidechain(f.ctx, &types.MsgActivateSidechain{Authority: f.govS, Id: testID})
	require.ErrorIs(t, err, types.ErrInvalidSidechainState)
}

func TestPendingSidechainCannotRecordCheckpoints(t *testing.T) {
	f := newFixture(t)
	f.register(t)

	err := f.keeper.RecordCheckpoint(f.ctx, testID, 1, testHeight, []byte("h"))
	require.ErrorIs(t, err, types.ErrInvalidSidechainState)
}

func TestSuspendAndResume(t *testing.T) {
	f := newActiveFixture(t)
	suspend := &types.MsgSuspendSidechain{Authority: f.govS, Id: testID}
	resume := &types.MsgResumeSidechain{Authority: f.govS, Id: testID}

	// Authority only.
	_, err := f.msgServer.SuspendSidechain(f.ctx, &types.MsgSuspendSidechain{Authority: f.operatorS, Id: testID})
	require.ErrorIs(t, err, types.ErrUnauthorized)
	_, err = f.msgServer.ResumeSidechain(f.ctx, &types.MsgResumeSidechain{Authority: f.operatorS, Id: testID})
	require.ErrorIs(t, err, types.ErrUnauthorized)

	// Resume needs SUSPENDED; suspend needs ACTIVE.
	_, err = f.msgServer.ResumeSidechain(f.ctx, resume)
	require.ErrorIs(t, err, types.ErrInvalidSidechainState)

	_, err = f.msgServer.SuspendSidechain(f.ctx, suspend)
	require.NoError(t, err)
	require.Equal(t, types.SidechainStatus_SIDECHAIN_STATUS_SUSPENDED, f.get(t, testID).Status)
	_, err = f.msgServer.SuspendSidechain(f.ctx, suspend)
	require.ErrorIs(t, err, types.ErrInvalidSidechainState)

	// A suspended sidechain takes no checkpoints...
	err = f.keeper.RecordCheckpoint(f.ctx, testID, 1, testHeight, []byte("h"))
	require.ErrorIs(t, err, types.ErrInvalidSidechainState)

	// ...until it is resumed.
	_, err = f.msgServer.ResumeSidechain(f.ctx, resume)
	require.NoError(t, err)
	require.Equal(t, types.SidechainStatus_SIDECHAIN_STATUS_ACTIVE, f.get(t, testID).Status)
	require.NoError(t, f.keeper.RecordCheckpoint(f.ctx, testID, 1, testHeight, []byte("h")))

	_, err = f.msgServer.SuspendSidechain(f.ctx, &types.MsgSuspendSidechain{Authority: f.govS, Id: "nope"})
	require.ErrorIs(t, err, types.ErrSidechainNotFound)
}

// ---------------------------------------------------------------------------
// signer rotation and updates
// ---------------------------------------------------------------------------

func TestUpdateSidechainSigners(t *testing.T) {
	updateMsg := func(f *fixture, t *testing.T, threshold uint32, idx ...int) *types.MsgUpdateSidechainSigners {
		return &types.MsgUpdateSidechainSigners{
			Operator:           f.operatorS,
			Id:                 testID,
			SignerKeys:         f.keyAnys(t, idx...),
			SignatureThreshold: threshold,
		}
	}

	t.Run("rotation bumps the version", func(t *testing.T) {
		f := newActiveFixture(t)
		resp, err := f.msgServer.UpdateSidechainSigners(f.ctx, updateMsg(f, t, 1, 2, 0))
		require.NoError(t, err)
		require.Equal(t, uint64(2), resp.SignerSetVersion)

		sc := f.get(t, testID)
		require.Equal(t, uint64(2), sc.SignerSetVersion)
		require.Equal(t, uint32(1), sc.SignatureThreshold)
		require.Len(t, sc.SignerKeys, 2)
		// Order is significant: position 0 is now signers[2].
		pk, ok := sc.SignerKeys[0].GetCachedValue().(cryptotypes.PubKey)
		require.True(t, ok)
		require.True(t, pk.Equals(f.signers[2].PubKey()))

		resp, err = f.msgServer.UpdateSidechainSigners(f.ctx, updateMsg(f, t, 2, 0, 1, 2))
		require.NoError(t, err)
		require.Equal(t, uint64(3), resp.SignerSetVersion)
	})

	t.Run("allowed while PENDING and SUSPENDED", func(t *testing.T) {
		f := newFixture(t)
		f.register(t)
		_, err := f.msgServer.UpdateSidechainSigners(f.ctx, updateMsg(f, t, 1, 0, 1))
		require.NoError(t, err)

		f.activate(t, testID)
		f.setStatus(t, testID, types.SidechainStatus_SIDECHAIN_STATUS_SUSPENDED)
		_, err = f.msgServer.UpdateSidechainSigners(f.ctx, updateMsg(f, t, 1, 0))
		require.NoError(t, err)
	})

	t.Run("failures change nothing", func(t *testing.T) {
		f := newActiveFixture(t)

		m := updateMsg(f, t, 2, 0, 1)
		m.Operator = f.strangerS
		_, err := f.msgServer.UpdateSidechainSigners(f.ctx, m)
		require.ErrorIs(t, err, types.ErrUnauthorized)

		m = updateMsg(f, t, 2, 0, 1)
		m.Id = "nope"
		_, err = f.msgServer.UpdateSidechainSigners(f.ctx, m)
		require.ErrorIs(t, err, types.ErrSidechainNotFound)

		_, err = f.msgServer.UpdateSidechainSigners(f.ctx, updateMsg(f, t, 0, 0, 1))
		require.ErrorIs(t, err, types.ErrInvalidSigners)
		_, err = f.msgServer.UpdateSidechainSigners(f.ctx, updateMsg(f, t, 3, 0, 1))
		require.ErrorIs(t, err, types.ErrInvalidSigners)
		_, err = f.msgServer.UpdateSidechainSigners(f.ctx, updateMsg(f, t, 1, 1, 1))
		require.ErrorIs(t, err, types.ErrInvalidSigners)

		require.Equal(t, uint64(1), f.get(t, testID).SignerSetVersion)

		for _, st := range []types.SidechainStatus{
			types.SidechainStatus_SIDECHAIN_STATUS_SLASHED,
			types.SidechainStatus_SIDECHAIN_STATUS_EXITED,
		} {
			f.setStatus(t, testID, st)
			_, err = f.msgServer.UpdateSidechainSigners(f.ctx, updateMsg(f, t, 1, 0, 1))
			require.ErrorIs(t, err, types.ErrInvalidSidechainState, st.String())
		}
	})
}

func TestUpdateSidechain(t *testing.T) {
	f := newActiveFixture(t)
	m := &types.MsgUpdateSidechain{
		Operator:                  f.operatorS,
		Id:                        testID,
		Name:                      "Renamed",
		MetadataUri:               "ipfs://new",
		CheckpointIntervalSeconds: 600,
	}

	_, err := f.msgServer.UpdateSidechain(f.ctx, m)
	require.NoError(t, err)
	sc := f.get(t, testID)
	require.Equal(t, "Renamed", sc.Name)
	require.Equal(t, "ipfs://new", sc.MetadataUri)
	require.Equal(t, uint64(600), sc.CheckpointIntervalSeconds)
	require.Equal(t, uint64(1), sc.SignerSetVersion, "metadata changes do not rotate signers")

	bad := *m
	bad.Operator = f.strangerS
	_, err = f.msgServer.UpdateSidechain(f.ctx, &bad)
	require.ErrorIs(t, err, types.ErrUnauthorized)

	bad = *m
	bad.Id = "nope"
	_, err = f.msgServer.UpdateSidechain(f.ctx, &bad)
	require.ErrorIs(t, err, types.ErrSidechainNotFound)

	bad = *m
	bad.Name = ""
	_, err = f.msgServer.UpdateSidechain(f.ctx, &bad)
	require.ErrorIs(t, err, types.ErrInvalidRequest)

	bad = *m
	bad.CheckpointIntervalSeconds = 0
	_, err = f.msgServer.UpdateSidechain(f.ctx, &bad)
	require.ErrorIs(t, err, types.ErrInvalidRequest)

	bad = *m
	bad.MetadataUri = strings.Repeat("u", types.MaxURIBytes+1)
	_, err = f.msgServer.UpdateSidechain(f.ctx, &bad)
	require.ErrorIs(t, err, types.ErrLimitExceeded)

	f.setStatus(t, testID, types.SidechainStatus_SIDECHAIN_STATUS_EXITED)
	_, err = f.msgServer.UpdateSidechain(f.ctx, m)
	require.ErrorIs(t, err, types.ErrInvalidSidechainState)
}

// ---------------------------------------------------------------------------
// exit and bond return
// ---------------------------------------------------------------------------

func TestInitiateSidechainExit(t *testing.T) {
	t.Run("sets the cooldown and queues the bond return", func(t *testing.T) {
		f := newActiveFixture(t)
		_, err := f.msgServer.InitiateSidechainExit(f.ctx, f.exitMsg())
		require.NoError(t, err)

		sc := f.get(t, testID)
		require.Equal(t, types.SidechainStatus_SIDECHAIN_STATUS_EXITED, sc.Status)
		require.NotNil(t, sc.ExitRequestedAt)
		require.True(t, sc.ExitRequestedAt.Equal(testBlockTime))
		require.True(t, sc.BondReturnAt.Equal(testBlockTime.Add(14*day)))
		require.Equal(t, 1, f.exitQueueLen(t))
		require.Equal(t, tier1Bond, sc.Bond.Amount.Int64(), "the bond stays escrowed until the cooldown ends")
	})

	t.Run("follows exit_cooldown_seconds", func(t *testing.T) {
		f := newActiveFixture(t)
		f.setParams(t, func(p *types.Params) { p.ExitCooldownSeconds = 3600 })
		_, err := f.msgServer.InitiateSidechainExit(f.ctx, f.exitMsg())
		require.NoError(t, err)
		require.True(t, f.get(t, testID).BondReturnAt.Equal(testBlockTime.Add(time.Hour)))
	})

	t.Run("allowed from PENDING, SUSPENDED and SLASHED", func(t *testing.T) {
		for _, st := range []types.SidechainStatus{
			types.SidechainStatus_SIDECHAIN_STATUS_PENDING,
			types.SidechainStatus_SIDECHAIN_STATUS_SUSPENDED,
			types.SidechainStatus_SIDECHAIN_STATUS_SLASHED,
		} {
			f := newActiveFixture(t)
			f.setStatus(t, testID, st)
			_, err := f.msgServer.InitiateSidechainExit(f.ctx, f.exitMsg())
			require.NoError(t, err, st.String())
		}
	})

	t.Run("failures", func(t *testing.T) {
		f := newActiveFixture(t)

		m := f.exitMsg()
		m.Operator = f.strangerS
		_, err := f.msgServer.InitiateSidechainExit(f.ctx, m)
		require.ErrorIs(t, err, types.ErrUnauthorized)

		m = f.exitMsg()
		m.Id = "nope"
		_, err = f.msgServer.InitiateSidechainExit(f.ctx, m)
		require.ErrorIs(t, err, types.ErrSidechainNotFound)

		_, err = f.msgServer.InitiateSidechainExit(f.ctx, f.exitMsg())
		require.NoError(t, err)
		_, err = f.msgServer.InitiateSidechainExit(f.ctx, f.exitMsg())
		require.ErrorIs(t, err, types.ErrInvalidSidechainState)
	})

	t.Run("an exited sidechain takes no more checkpoints", func(t *testing.T) {
		f := newActiveFixture(t)
		require.NoError(t, f.keeper.RecordCheckpoint(f.ctx, testID, 1, testHeight, []byte("h1")))
		_, err := f.msgServer.InitiateSidechainExit(f.ctx, f.exitMsg())
		require.NoError(t, err)

		err = f.keeper.RecordCheckpoint(f.ctx, testID, 2, testHeight, []byte("h2"))
		require.ErrorIs(t, err, types.ErrInvalidSidechainState)
		// Its history is intact.
		require.Equal(t, uint64(1), f.get(t, testID).LastCheckpointSequence)
	})
}

func TestProcessExits_ReturnsBondAfterCooldown(t *testing.T) {
	f := newActiveFixture(t)
	_, err := f.msgServer.InitiateSidechainExit(f.ctx, f.exitMsg())
	require.NoError(t, err)
	operatorBefore := f.bal(f.operator) // the bond is still escrowed

	// One second before the cooldown ends: nothing happens.
	f.advance(14*day - time.Second)
	require.NoError(t, f.keeper.ProcessExits(f.ctx))
	require.Equal(t, operatorBefore, f.bal(f.operator))
	require.Equal(t, tier1Bond, f.moduleBal())
	require.Equal(t, 1, f.exitQueueLen(t))

	// At the cooldown: the bond is returned and the entry is removed.
	f.advance(time.Second)
	require.NoError(t, f.keeper.ProcessExits(f.ctx))
	require.Equal(t, operatorBefore+tier1Bond, f.bal(f.operator))
	require.Zero(t, f.moduleBal())

	sc := f.get(t, testID)
	require.True(t, sc.Bond.IsZero())
	require.Equal(t, sdk.DefaultBondDenom, sc.Bond.Denom, "the denom is kept so the record stays valid")
	require.Equal(t, types.SidechainStatus_SIDECHAIN_STATUS_EXITED, sc.Status)
	require.Zero(t, f.exitQueueLen(t))
	f.requireEscrowInvariant(t)

	// Running again changes nothing.
	require.NoError(t, f.keeper.ProcessExits(f.ctx))
	require.Equal(t, operatorBefore+tier1Bond, f.bal(f.operator))
}

func TestProcessExits_BankFailureIsSkippedAndRetried(t *testing.T) {
	f := newActiveFixture(t)
	_, err := f.msgServer.InitiateSidechainExit(f.ctx, f.exitMsg())
	require.NoError(t, err)
	f.advance(15 * day)
	operatorBefore := f.bal(f.operator)

	// A failing payout must not return an error (that would halt the chain)
	// and must leave the sidechain and its queue entry untouched.
	f.bank.failFromModule = true
	require.NoError(t, f.keeper.ProcessExits(f.ctx))
	require.Equal(t, operatorBefore, f.bal(f.operator))
	require.Equal(t, tier1Bond, f.get(t, testID).Bond.Amount.Int64())
	require.Equal(t, 1, f.exitQueueLen(t))

	// Once the bank recovers, the next pass pays out.
	f.bank.failFromModule = false
	require.NoError(t, f.keeper.ProcessExits(f.ctx))
	require.Equal(t, operatorBefore+tier1Bond, f.bal(f.operator))
	require.Zero(t, f.exitQueueLen(t))
}

func TestProcessExits_HandlesSeveralAndDanglingEntries(t *testing.T) {
	f := newFixture(t)

	// Two sidechains exit at different times.
	for _, id := range []string{"first-chain", "second-chain"} {
		m := f.registerMsg(t)
		m.Id = id
		_, err := f.msgServer.RegisterSidechain(f.ctx, m)
		require.NoError(t, err)
		f.activate(t, id)
	}
	_, err := f.msgServer.InitiateSidechainExit(f.ctx, &types.MsgInitiateSidechainExit{Operator: f.operatorS, Id: "first-chain"})
	require.NoError(t, err)
	f.advance(2 * day)
	_, err = f.msgServer.InitiateSidechainExit(f.ctx, &types.MsgInitiateSidechainExit{Operator: f.operatorS, Id: "second-chain"})
	require.NoError(t, err)

	// A queue entry whose sidechain record is gone is dropped, not fatal.
	require.NoError(t, f.keeper.ExitQueue.Set(f.ctx, collections.Join(uint64(testBlockTime.Unix()), "ghost")))
	require.Equal(t, 3, f.exitQueueLen(t))

	// Day 14 after the first exit: the first and the dangling entry are due.
	f.advance(12 * day)
	before := f.bal(f.operator)
	require.NoError(t, f.keeper.ProcessExits(f.ctx))
	require.Equal(t, before+tier1Bond, f.bal(f.operator))
	require.Equal(t, 1, f.exitQueueLen(t), "only the second sidechain is still queued")
	require.True(t, f.get(t, "first-chain").Bond.IsZero())
	require.Equal(t, tier1Bond, f.get(t, "second-chain").Bond.Amount.Int64())

	f.advance(2 * day)
	require.NoError(t, f.keeper.ProcessExits(f.ctx))
	require.Zero(t, f.exitQueueLen(t))
	require.Zero(t, f.moduleBal())
	f.requireEscrowInvariant(t)
}

// ---------------------------------------------------------------------------
// RecordCheckpoint (called by x/checkpoint) and GetSidechain
// ---------------------------------------------------------------------------

func TestRecordCheckpoint(t *testing.T) {
	t.Run("updates the last-checkpoint fields", func(t *testing.T) {
		f := newActiveFixture(t)
		f.advance(time.Minute)

		require.NoError(t, f.keeper.RecordCheckpoint(f.ctx, testID, 1, 120, []byte("hash-1")))
		sc := f.get(t, testID)
		require.Equal(t, uint64(1), sc.LastCheckpointSequence)
		require.Equal(t, int64(120), sc.LastCheckpointHeight)
		require.Equal(t, []byte("hash-1"), sc.LastCheckpointHash)
		require.NotNil(t, sc.LastCheckpointAt)
		require.True(t, sc.LastCheckpointAt.Equal(testBlockTime.Add(time.Minute)))

		// Gaps in the sequence are x/checkpoint's concern; here it only has to increase.
		require.NoError(t, f.keeper.RecordCheckpoint(f.ctx, testID, 5, 120, []byte("hash-5")))
		require.Equal(t, uint64(5), f.get(t, testID).LastCheckpointSequence)
	})

	t.Run("failures change nothing", func(t *testing.T) {
		f := newActiveFixture(t)
		require.NoError(t, f.keeper.RecordCheckpoint(f.ctx, testID, 3, 200, []byte("hash-3")))

		cases := []struct {
			name   string
			id     string
			seq    uint64
			height int64
			hash   []byte
			want   error
		}{
			{"unknown sidechain", "nope", 4, 201, []byte("h"), types.ErrSidechainNotFound},
			{"same sequence", testID, 3, 201, []byte("h"), types.ErrInvalidRequest},
			{"lower sequence", testID, 2, 201, []byte("h"), types.ErrInvalidRequest},
			{"height going backwards", testID, 4, 199, []byte("h"), types.ErrInvalidRequest},
			{"empty hash", testID, 4, 201, nil, types.ErrInvalidRequest},
		}
		for _, tc := range cases {
			err := f.keeper.RecordCheckpoint(f.ctx, tc.id, tc.seq, tc.height, tc.hash)
			require.ErrorIs(t, err, tc.want, tc.name)
		}

		sc := f.get(t, testID)
		require.Equal(t, uint64(3), sc.LastCheckpointSequence)
		require.Equal(t, int64(200), sc.LastCheckpointHeight)
		require.Equal(t, []byte("hash-3"), sc.LastCheckpointHash)

		// Equal height is fine.
		require.NoError(t, f.keeper.RecordCheckpoint(f.ctx, testID, 4, 200, []byte("hash-4")))
	})
}

func TestGetSidechain_NotFoundSatisfiesCollectionsNotFound(t *testing.T) {
	// x/checkpoint, x/attestor and x/proofs detect a missing sidechain with
	// errors.Is(err, collections.ErrNotFound), so the exported getter must
	// keep that contract.
	f := newFixture(t)

	_, err := f.keeper.GetSidechain(f.ctx, "nope")
	require.Error(t, err)
	require.True(t, errors.Is(err, collections.ErrNotFound))

	has, err := f.keeper.HasSidechain(f.ctx, "nope")
	require.NoError(t, err)
	require.False(t, has)
}

// ---------------------------------------------------------------------------
// queries
// ---------------------------------------------------------------------------

func TestQuery_NotFoundAndInvalidArgument(t *testing.T) {
	f := newFixture(t)

	_, err := f.queryServer.Sidechain(f.ctx, &types.QuerySidechainRequest{Id: "nope"})
	require.Equal(t, codes.NotFound, status.Code(err))
	_, err = f.queryServer.Sidechain(f.ctx, &types.QuerySidechainRequest{})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = f.queryServer.Sidechain(f.ctx, nil)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = f.queryServer.Sidechains(f.ctx, nil)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = f.queryServer.Params(f.ctx, nil)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestQuery_SidechainAndParams(t *testing.T) {
	f := newActiveFixture(t)

	res, err := f.queryServer.Sidechain(f.ctx, &types.QuerySidechainRequest{Id: testID})
	require.NoError(t, err)
	require.Equal(t, testID, res.Sidechain.Id)
	require.Len(t, res.Sidechain.SignerKeys, 3)

	p, err := f.queryServer.Params(f.ctx, &types.QueryParamsRequest{})
	require.NoError(t, err)
	require.Equal(t, uint32(7), p.Params.MaxSignerKeys)
}

func TestQuery_SidechainsFiltersAndPagination(t *testing.T) {
	f := newFixture(t)

	register := func(id string, tier types.AssuranceTier, bond int64) {
		m := f.registerMsg(t)
		m.Id = id
		m.Tier = tier
		m.Bond = sdk.NewInt64Coin(sdk.DefaultBondDenom, bond)
		_, err := f.msgServer.RegisterSidechain(f.ctx, m)
		require.NoError(t, err)
	}
	register("chain-a", types.AssuranceTier_ASSURANCE_TIER_NOTARIZED, tier1Bond)
	register("chain-b", types.AssuranceTier_ASSURANCE_TIER_ATTESTED, tier2Bond)
	register("chain-c", types.AssuranceTier_ASSURANCE_TIER_ATTESTED, tier2Bond)
	f.activate(t, "chain-b")

	ids := func(req *types.QuerySidechainsRequest) []string {
		res, err := f.queryServer.Sidechains(f.ctx, req)
		require.NoError(t, err)
		var out []string
		for _, sc := range res.Sidechains {
			out = append(out, sc.Id)
		}
		return out
	}

	require.Equal(t, []string{"chain-a", "chain-b", "chain-c"}, ids(&types.QuerySidechainsRequest{}))
	require.Equal(t, []string{"chain-b", "chain-c"}, ids(&types.QuerySidechainsRequest{Tier: types.AssuranceTier_ASSURANCE_TIER_ATTESTED}))
	require.Equal(t, []string{"chain-b"}, ids(&types.QuerySidechainsRequest{Status: types.SidechainStatus_SIDECHAIN_STATUS_ACTIVE}))
	require.Equal(t, []string{"chain-a", "chain-c"}, ids(&types.QuerySidechainsRequest{Status: types.SidechainStatus_SIDECHAIN_STATUS_PENDING}))
	require.Equal(t, []string{"chain-c"}, ids(&types.QuerySidechainsRequest{
		Tier:   types.AssuranceTier_ASSURANCE_TIER_ATTESTED,
		Status: types.SidechainStatus_SIDECHAIN_STATUS_PENDING,
	}))
	require.Empty(t, ids(&types.QuerySidechainsRequest{Tier: types.AssuranceTier_ASSURANCE_TIER_PROVEN}))

	// Page through two at a time.
	res, err := f.queryServer.Sidechains(f.ctx, &types.QuerySidechainsRequest{Pagination: &query.PageRequest{Limit: 2}})
	require.NoError(t, err)
	require.Len(t, res.Sidechains, 2)
	require.NotEmpty(t, res.Pagination.NextKey)

	res2, err := f.queryServer.Sidechains(f.ctx, &types.QuerySidechainsRequest{
		Pagination: &query.PageRequest{Limit: 2, Key: res.Pagination.NextKey},
	})
	require.NoError(t, err)
	require.Len(t, res2.Sidechains, 1)
	require.Equal(t, "chain-c", res2.Sidechains[0].Id)
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
	require.Empty(t, got.Sidechains)
}

func TestGenesis_RoundTripKeepsSidechainsUsable(t *testing.T) {
	f := newActiveFixture(t)
	require.NoError(t, f.keeper.RecordCheckpoint(f.ctx, testID, 1, testHeight, []byte("hash-1")))

	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.NoError(t, exported.Validate())
	require.Len(t, exported.Sidechains, 1)

	// Import into a fresh chain whose module account is funded as bank genesis would.
	g := newFixture(t)
	g.bank.modules[types.ModuleName] = f.bank.moduleBalance(types.ModuleName)
	require.NoError(t, g.keeper.InitGenesis(g.ctx, *exported))
	g.requireEscrowInvariant(t)

	re, err := g.keeper.ExportGenesis(g.ctx)
	require.NoError(t, err)
	require.True(t, exported.Params.Equal(re.Params))
	require.Len(t, re.Sidechains, 1)
	require.True(t, exported.Sidechains[0].Equal(re.Sidechains[0]))

	// The imported sidechain keeps working: keys unpacked, checkpoints continue.
	sc := g.get(t, testID)
	_, ok := sc.SignerKeys[0].GetCachedValue().(cryptotypes.PubKey)
	require.True(t, ok)
	require.NoError(t, g.keeper.RecordCheckpoint(g.ctx, testID, 2, testHeight, []byte("hash-2")))
}

func TestGenesis_RoundTripRebuildsExitQueue(t *testing.T) {
	f := newActiveFixture(t)
	_, err := f.msgServer.InitiateSidechainExit(f.ctx, f.exitMsg())
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
	require.Equal(t, operatorBefore+tier1Bond, g.bal(g.operator))
	require.Zero(t, g.exitQueueLen(t))
	g.requireEscrowInvariant(t)
}

func TestGenesis_ReturnedBondIsNotQueuedAgain(t *testing.T) {
	f := newActiveFixture(t)
	_, err := f.msgServer.InitiateSidechainExit(f.ctx, f.exitMsg())
	require.NoError(t, err)
	f.advance(15 * day)
	require.NoError(t, f.keeper.ProcessExits(f.ctx))

	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.NoError(t, exported.Validate())

	g := newFixture(t)
	require.NoError(t, g.keeper.InitGenesis(g.ctx, *exported))
	require.Zero(t, g.exitQueueLen(t), "a zero bond has nothing left to return")
}

func TestGenesisValidate_RejectsInconsistentState(t *testing.T) {
	f := newActiveFixture(t)
	require.NoError(t, f.keeper.RecordCheckpoint(f.ctx, testID, 1, testHeight, []byte("hash-1")))

	clone := func() types.GenesisState {
		out, err := f.keeper.ExportGenesis(f.ctx)
		require.NoError(t, err)
		return *out
	}
	require.NoError(t, func() error { gs := clone(); return gs.Validate() }())

	cases := map[string]func(gs *types.GenesisState){
		"bad id":              func(gs *types.GenesisState) { gs.Sidechains[0].Id = "Bad ID" },
		"duplicate sidechain": func(gs *types.GenesisState) { gs.Sidechains = append(gs.Sidechains, gs.Sidechains[0]) },
		"no operator":         func(gs *types.GenesisState) { gs.Sidechains[0].Operator = "" },
		"status unspecified": func(gs *types.GenesisState) {
			gs.Sidechains[0].Status = types.SidechainStatus_SIDECHAIN_STATUS_UNSPECIFIED
		},
		"invalid tier":                     func(gs *types.GenesisState) { gs.Sidechains[0].Tier = types.AssuranceTier(99) },
		"threshold above key count":        func(gs *types.GenesisState) { gs.Sidechains[0].SignatureThreshold = 9 },
		"no signer keys":                   func(gs *types.GenesisState) { gs.Sidechains[0].SignerKeys = nil },
		"signer_set_version zero":          func(gs *types.GenesisState) { gs.Sidechains[0].SignerSetVersion = 0 },
		"invalid bond":                     func(gs *types.GenesisState) { gs.Sidechains[0].Bond = sdk.Coin{} },
		"zero checkpoint interval":         func(gs *types.GenesisState) { gs.Sidechains[0].CheckpointIntervalSeconds = 0 },
		"checkpoint sequence without hash": func(gs *types.GenesisState) { gs.Sidechains[0].LastCheckpointHash = nil },
		"checkpoint hash without sequence": func(gs *types.GenesisState) { gs.Sidechains[0].LastCheckpointSequence = 0 },
		"EXITED without bond_return_at": func(gs *types.GenesisState) {
			gs.Sidechains[0].Status = types.SidechainStatus_SIDECHAIN_STATUS_EXITED
			gs.Sidechains[0].BondReturnAt = nil
		},
		"invalid params": func(gs *types.GenesisState) { gs.Params.MinBondByTier = nil },
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

	for _, authority := range []string{f.strangerS, f.operatorS} {
		_, err := f.msgServer.ActivateSidechain(f.ctx, &types.MsgActivateSidechain{Authority: authority, Id: testID})
		require.ErrorIs(t, err, types.ErrUnauthorized)
		_, err = f.msgServer.SuspendSidechain(f.ctx, &types.MsgSuspendSidechain{Authority: authority, Id: testID})
		require.ErrorIs(t, err, types.ErrUnauthorized)
		_, err = f.msgServer.ResumeSidechain(f.ctx, &types.MsgResumeSidechain{Authority: authority, Id: testID})
		require.ErrorIs(t, err, types.ErrUnauthorized)
		_, err = f.msgServer.UpdateParams(f.ctx, &types.MsgUpdateParams{Authority: authority, Params: types.DefaultParams()})
		require.ErrorIs(t, err, types.ErrUnauthorized)
	}
}

func TestOperatorOnlyMessagesRejectOtherSigners(t *testing.T) {
	f := newActiveFixture(t)

	_, err := f.msgServer.UpdateSidechainSigners(f.ctx, &types.MsgUpdateSidechainSigners{
		Operator: f.govS, Id: testID, SignerKeys: f.keyAnys(t, 0), SignatureThreshold: 1,
	})
	require.ErrorIs(t, err, types.ErrUnauthorized, "governance is not the operator")

	_, err = f.msgServer.UpdateSidechain(f.ctx, &types.MsgUpdateSidechain{
		Operator: f.govS, Id: testID, Name: "x", CheckpointIntervalSeconds: 1,
	})
	require.ErrorIs(t, err, types.ErrUnauthorized)

	_, err = f.msgServer.InitiateSidechainExit(f.ctx, &types.MsgInitiateSidechainExit{Operator: f.govS, Id: testID})
	require.ErrorIs(t, err, types.ErrUnauthorized)
}
