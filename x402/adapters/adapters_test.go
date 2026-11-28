package adapters

// Tests for the real adapters, written against cosmos-sdk v0.55.0.
//
// run
// `go test ./x402/adapters/...` and fix any compile error first. Everything
// here is hermetic: an in-process fake gRPC node serves the auth, bank and tx
// services, and transactions are really signed (secp256k1 and ML-DSA-65).

import (
	"bytes"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"cosmossdk.io/core/address"
	sdkmath "cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/client"
	clienttx "github.com/cosmos/cosmos-sdk/client/tx"
	"github.com/cosmos/cosmos-sdk/codec"
	codectestutil "github.com/cosmos/cosmos-sdk/codec/testutil"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/crypto/keys/mldsa65"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	"github.com/cosmos/cosmos-sdk/std"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
	signingtypes "github.com/cosmos/cosmos-sdk/types/tx/signing"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"
	authtx "github.com/cosmos/cosmos-sdk/x/auth/tx"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	"github.com/lucidpay/lucidchain/x402/facilitator"
)

const testChainID = "x402-test-1"

func must(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------------------
// Fake node: auth + bank + tx gRPC services backed by in-memory state.
// ---------------------------------------------------------------------------

type fakeState struct {
	mu       sync.Mutex
	base     map[string]*authtypes.BaseAccount
	module   map[string]*authtypes.ModuleAccount
	balances map[string]sdkmath.Int // "addr|denom"

	simGas uint64
	simErr error

	bcCode     uint32
	bcLog      string
	bcErr      error
	lastMode   sdktx.BroadcastMode
	broadcasts [][]byte

	txs      map[string]*sdk.TxResponse
	getTxErr error
}

func newFakeState() *fakeState {
	return &fakeState{
		base:     map[string]*authtypes.BaseAccount{},
		module:   map[string]*authtypes.ModuleAccount{},
		balances: map[string]sdkmath.Int{},
		txs:      map[string]*sdk.TxResponse{},
	}
}

type fakeAuth struct {
	authtypes.UnimplementedQueryServer
	s *fakeState
}

func (f *fakeAuth) AccountInfo(_ context.Context, req *authtypes.QueryAccountInfoRequest) (*authtypes.QueryAccountInfoResponse, error) {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	acc, ok := f.s.base[req.Address]
	if !ok {
		return nil, status.Error(codes.NotFound, "account not found")
	}
	return &authtypes.QueryAccountInfoResponse{Info: acc}, nil
}

func (f *fakeAuth) Account(_ context.Context, req *authtypes.QueryAccountRequest) (*authtypes.QueryAccountResponse, error) {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	if m, ok := f.s.module[req.Address]; ok {
		a, err := codectypes.NewAnyWithValue(m)
		return &authtypes.QueryAccountResponse{Account: a}, err
	}
	if b, ok := f.s.base[req.Address]; ok {
		a, err := codectypes.NewAnyWithValue(b)
		return &authtypes.QueryAccountResponse{Account: a}, err
	}
	return nil, status.Error(codes.NotFound, "account not found")
}

type fakeBank struct {
	banktypes.UnimplementedQueryServer
	s *fakeState
}

func (f *fakeBank) SpendableBalanceByDenom(_ context.Context, req *banktypes.QuerySpendableBalanceByDenomRequest) (*banktypes.QuerySpendableBalanceByDenomResponse, error) {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	amt, ok := f.s.balances[req.Address+"|"+req.Denom]
	if !ok {
		amt = sdkmath.ZeroInt()
	}
	c := sdk.NewCoin(req.Denom, amt)
	return &banktypes.QuerySpendableBalanceByDenomResponse{Balance: &c}, nil
}

type fakeTx struct {
	sdktx.UnimplementedServiceServer
	s *fakeState
}

func (f *fakeTx) Simulate(_ context.Context, _ *sdktx.SimulateRequest) (*sdktx.SimulateResponse, error) {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	if f.s.simErr != nil {
		return nil, f.s.simErr
	}
	return &sdktx.SimulateResponse{GasInfo: &sdk.GasInfo{GasUsed: f.s.simGas}}, nil
}

func (f *fakeTx) BroadcastTx(_ context.Context, req *sdktx.BroadcastTxRequest) (*sdktx.BroadcastTxResponse, error) {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	f.s.lastMode = req.Mode
	f.s.broadcasts = append(f.s.broadcasts, append([]byte(nil), req.TxBytes...))
	if f.s.bcErr != nil {
		return nil, f.s.bcErr
	}
	return &sdktx.BroadcastTxResponse{TxResponse: &sdk.TxResponse{
		Code:      f.s.bcCode,
		Codespace: "sdk",
		TxHash:    facilitator.TxHash(req.TxBytes),
		RawLog:    f.s.bcLog,
	}}, nil
}

func (f *fakeTx) GetTx(_ context.Context, req *sdktx.GetTxRequest) (*sdktx.GetTxResponse, error) {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	if f.s.getTxErr != nil {
		return nil, f.s.getTxErr
	}
	r, ok := f.s.txs[req.Hash]
	if !ok {
		return nil, status.Error(codes.NotFound, "tx not found: "+req.Hash)
	}
	return &sdktx.GetTxResponse{TxResponse: r}, nil
}

func startFake(t testing.TB) (string, *fakeState) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	must(t, err)
	st := newFakeState()
	srv := grpc.NewServer()
	authtypes.RegisterQueryServer(srv, &fakeAuth{s: st})
	banktypes.RegisterQueryServer(srv, &fakeBank{s: st})
	sdktx.RegisterServiceServer(srv, &fakeTx{s: st})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String(), st
}

// ---------------------------------------------------------------------------
// Test environment and tx builders.
// ---------------------------------------------------------------------------

type env struct {
	t        testing.TB
	reg      codectypes.InterfaceRegistry
	txCfg    client.TxConfig
	addrCdc  address.Codec
	st       *fakeState
	grpcAddr string
	parser   *SDKTxParser
	ver      *SDKSignatureVerifier
}

func newEnv(t testing.TB) *env {
	t.Helper()
	reg := codectestutil.CodecOptions{}.NewInterfaceRegistry()
	std.RegisterInterfaces(reg)
	authtypes.RegisterInterfaces(reg)
	banktypes.RegisterInterfaces(reg)
	cdc := codec.NewProtoCodec(reg)
	txCfg := authtx.NewTxConfig(cdc, authtx.DefaultSignModes)
	addrCdc := reg.SigningContext().AddressCodec()

	addr, st := startFake(t)
	ver, err := NewSDKSignatureVerifier(txCfg, reg, addrCdc, addr, testChainID)
	must(t, err)
	t.Cleanup(func() { _ = ver.Close() })

	return &env{
		t: t, reg: reg, txCfg: txCfg, addrCdc: addrCdc, st: st, grpcAddr: addr,
		parser: NewSDKTxParser(txCfg, addrCdc), ver: ver,
	}
}

func (e *env) addr(bz []byte) string {
	e.t.Helper()
	s, err := e.addrCdc.BytesToString(bz)
	must(e.t, err)
	return s
}

func genKey(t testing.TB, kind string) cryptotypes.PrivKey {
	t.Helper()
	if kind == "mldsa65" {
		k, err := mldsa65.GenPrivKey()
		must(t, err)
		return &k
	}
	return secp256k1.GenPrivKey()
}

var keyKinds = []string{"secp256k1", "mldsa65"}

func keyAddr(k cryptotypes.PrivKey) sdk.AccAddress { return sdk.AccAddress(k.PubKey().Address()) }

// setAccount registers an on-chain account. pub may be nil (pubkey not yet stored).
func (e *env) setAccount(addr sdk.AccAddress, pub cryptotypes.PubKey, accNum, seq uint64) {
	e.st.mu.Lock()
	defer e.st.mu.Unlock()
	e.st.base[e.addr(addr)] = authtypes.NewBaseAccount(addr, pub, accNum, seq)
}

func (e *env) addAccount(k cryptotypes.PrivKey, accNum, seq uint64, storePub bool) {
	var pub cryptotypes.PubKey
	if storePub {
		pub = k.PubKey()
	}
	e.setAccount(keyAddr(k), pub, accNum, seq)
}

type txOpts struct {
	key        cryptotypes.PrivKey
	from       sdk.AccAddress
	to         sdk.AccAddress
	coins      sdk.Coins
	memo       string
	unordered  bool
	timeout    time.Time
	seq        uint64
	accNum     uint64
	chainID    string
	msgs       []sdk.Msg // if set, replaces the default MsgSend
	extraMsgs  []sdk.Msg // appended after the first message
	feePayer   sdk.AccAddress
	feeGranter sdk.AccAddress
}

func (e *env) opts(k cryptotypes.PrivKey) txOpts {
	return txOpts{
		key:       k,
		from:      keyAddr(k),
		to:        sdk.AccAddress(bytes.Repeat([]byte{0xAB}, 20)),
		coins:     sdk.NewCoins(sdk.NewInt64Coin("utest", 10000)),
		memo:      "x402:pay-1",
		unordered: true,
		timeout:   time.Now().Add(time.Minute).UTC(),
		chainID:   testChainID,
		accNum:    7,
	}
}

func (e *env) newBuilder(o txOpts) client.TxBuilder {
	e.t.Helper()
	b := e.txCfg.NewTxBuilder()
	msgs := o.msgs
	if len(msgs) == 0 {
		msgs = []sdk.Msg{banktypes.NewMsgSend(o.from, o.to, o.coins)}
	}
	msgs = append(msgs, o.extraMsgs...)
	must(e.t, b.SetMsgs(msgs...))
	b.SetMemo(o.memo)
	b.SetGasLimit(200000)
	if o.unordered {
		b.SetUnordered(true)
		b.SetTimeoutTimestamp(o.timeout)
	}
	if len(o.feePayer) > 0 {
		b.SetFeePayer(o.feePayer)
	}
	if len(o.feeGranter) > 0 {
		b.SetFeeGranter(o.feeGranter)
	}
	return b
}

func (e *env) encode(b client.TxBuilder) []byte {
	e.t.Helper()
	raw, err := e.txCfg.TxEncoder()(b.GetTx())
	must(e.t, err)
	return raw
}

// signedTx builds a single-signer SIGN_MODE_DIRECT tx with a REAL signature from o.key.
func (e *env) signedTx(o txOpts) []byte {
	e.t.Helper()
	b := e.newBuilder(o)
	pub := o.key.PubKey()
	mode := signingtypes.SignMode_SIGN_MODE_DIRECT
	must(e.t, b.SetSignatures(signingtypes.SignatureV2{
		PubKey:   pub,
		Data:     &signingtypes.SingleSignatureData{SignMode: mode},
		Sequence: o.seq,
	}))
	sd := authsigning.SignerData{
		Address:       e.addr(pub.Address()),
		ChainID:       o.chainID,
		AccountNumber: o.accNum,
		Sequence:      o.seq,
		PubKey:        pub,
	}
	sig, err := clienttx.SignWithPrivKey(context.Background(), mode, sd, b, o.key, e.txCfg, o.seq)
	must(e.t, err)
	must(e.t, b.SetSignatures(sig))
	return e.encode(b)
}

// junkSigTx builds a structurally valid tx with one junk signature per pubkey.
// Good for parser tests and for cases that must be rejected before any crypto.
func (e *env) junkSigTx(o txOpts, pubs ...cryptotypes.PubKey) []byte {
	e.t.Helper()
	b := e.newBuilder(o)
	sigs := make([]signingtypes.SignatureV2, 0, len(pubs))
	for _, p := range pubs {
		sigs = append(sigs, signingtypes.SignatureV2{
			PubKey: p,
			Data: &signingtypes.SingleSignatureData{
				SignMode:  signingtypes.SignMode_SIGN_MODE_DIRECT,
				Signature: []byte{1, 2, 3},
			},
			Sequence: o.seq,
		})
	}
	must(e.t, b.SetSignatures(sigs...))
	return e.encode(b)
}

func tamperMemo(t testing.TB, raw []byte) []byte {
	t.Helper()
	var r sdktx.TxRaw
	must(t, r.Unmarshal(raw))
	var body sdktx.TxBody
	must(t, body.Unmarshal(r.BodyBytes))
	body.Memo = "x402:evil"
	bz, err := body.Marshal()
	must(t, err)
	r.BodyBytes = bz
	out, err := r.Marshal()
	must(t, err)
	return out
}

// stripAuthInfo returns a TxRaw with the same body but no AuthInfo (so no fee).
func stripAuthInfo(t testing.TB, raw []byte) []byte {
	t.Helper()
	var r sdktx.TxRaw
	must(t, r.Unmarshal(raw))
	r.AuthInfoBytes = nil
	out, err := r.Marshal()
	must(t, err)
	return out
}

func tamperSignature(t testing.TB, raw []byte) []byte {
	t.Helper()
	var r sdktx.TxRaw
	must(t, r.Unmarshal(raw))
	if len(r.Signatures) == 0 || len(r.Signatures[0]) == 0 {
		t.Fatal("no signature to tamper with")
	}
	r.Signatures[0][0] ^= 0xFF
	out, err := r.Marshal()
	must(t, err)
	return out
}

func (e *env) verify(raw []byte, mutate func(*facilitator.ParsedTx)) error {
	e.t.Helper()
	p, err := e.parser.Parse(raw)
	if err != nil {
		return err
	}
	if mutate != nil {
		mutate(p)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return e.ver.Verify(ctx, p)
}

// ---------------------------------------------------------------------------
// TxParser
// ---------------------------------------------------------------------------

func TestParseValid(t *testing.T) {
	for _, kind := range keyKinds {
		t.Run(kind, func(t *testing.T) {
			e := newEnv(t)
			k := genKey(t, kind)
			o := e.opts(k)
			raw := e.signedTx(o)

			p, err := e.parser.Parse(raw)
			must(t, err)

			if !bytes.Equal(p.Raw, raw) {
				t.Fatal("Raw must be the original bytes")
			}
			if p.MsgCount != 1 || p.MsgTypeURL != facilitator.MsgSendTypeURL {
				t.Fatalf("msg: count=%d url=%q", p.MsgCount, p.MsgTypeURL)
			}
			if p.From != e.addr(o.from) || p.To != e.addr(o.to) {
				t.Fatalf("from/to: %q %q", p.From, p.To)
			}
			if p.Denom != "utest" || p.Amount == nil || p.Amount.Int64() != 10000 {
				t.Fatalf("coin: %q %v", p.Denom, p.Amount)
			}
			if p.Signer != p.From || p.SignerCount != 1 {
				t.Fatalf("signer: %q count=%d", p.Signer, p.SignerCount)
			}
			wantType := "/cosmos.crypto." + kind + ".PubKey"
			if p.SignerKeyType != wantType {
				t.Fatalf("key type: got %q want %q", p.SignerKeyType, wantType)
			}
			if p.Memo != "x402:pay-1" || !p.Unordered || p.Sequence != 0 {
				t.Fatalf("memo/unordered/seq: %q %v %d", p.Memo, p.Unordered, p.Sequence)
			}
			if !p.TimeoutTimestamp.Equal(o.timeout) {
				t.Fatalf("timeout: got %v want %v", p.TimeoutTimestamp, o.timeout)
			}
			// Regression: FeePayer must be EMPTY when unset (the SDK's FeePayer()
			// would have returned the signer).
			if p.FeePayer != "" || p.FeeGranter != "" {
				t.Fatalf("fee fields must be empty when unset: %q %q", p.FeePayer, p.FeeGranter)
			}
		})
	}
}

func TestParseFeeFields(t *testing.T) {
	e := newEnv(t)
	a, b := genKey(t, "secp256k1"), genKey(t, "secp256k1")

	t.Run("explicit payer equal to signer is reported", func(t *testing.T) {
		o := e.opts(a)
		o.feePayer = keyAddr(a)
		p, err := e.parser.Parse(e.junkSigTx(o, a.PubKey()))
		must(t, err)
		if p.FeePayer != e.addr(keyAddr(a)) || p.SignerCount != 1 {
			t.Fatalf("payer=%q signers=%d", p.FeePayer, p.SignerCount)
		}
	})

	t.Run("other fee payer becomes a second signer", func(t *testing.T) {
		o := e.opts(a)
		o.feePayer = keyAddr(b)
		o.feeGranter = keyAddr(b)
		p, err := e.parser.Parse(e.junkSigTx(o, a.PubKey(), b.PubKey()))
		must(t, err)
		if p.SignerCount != 2 || p.Signer != e.addr(keyAddr(a)) {
			t.Fatalf("signers=%d first=%q", p.SignerCount, p.Signer)
		}
		if p.FeePayer != e.addr(keyAddr(b)) || p.FeeGranter != e.addr(keyAddr(b)) {
			t.Fatalf("payer=%q granter=%q", p.FeePayer, p.FeeGranter)
		}
	})
}

func TestParseShapes(t *testing.T) {
	e := newEnv(t)
	a, b := genKey(t, "secp256k1"), genKey(t, "secp256k1")

	t.Run("multiple messages: first message described, MsgCount reports 2", func(t *testing.T) {
		o := e.opts(a)
		o.extraMsgs = []sdk.Msg{banktypes.NewMsgSend(keyAddr(b), o.to, o.coins)}
		p, err := e.parser.Parse(e.junkSigTx(o, a.PubKey(), b.PubKey()))
		must(t, err)
		if p.MsgCount != 2 || p.MsgTypeURL != facilitator.MsgSendTypeURL || p.From != e.addr(keyAddr(a)) {
			t.Fatalf("count=%d url=%q from=%q", p.MsgCount, p.MsgTypeURL, p.From)
		}
		if p.SignerCount != 2 {
			t.Fatalf("signer count=%d", p.SignerCount)
		}
	})

	t.Run("multi-coin send leaves Amount nil", func(t *testing.T) {
		o := e.opts(a)
		o.coins = sdk.NewCoins(sdk.NewInt64Coin("utest", 1), sdk.NewInt64Coin("uother", 2))
		p, err := e.parser.Parse(e.junkSigTx(o, a.PubKey()))
		must(t, err)
		if p.Amount != nil || p.Denom != "" {
			t.Fatalf("multi-coin must be unsupported: %q %v", p.Denom, p.Amount)
		}
	})

	t.Run("non-MsgSend first message", func(t *testing.T) {
		o := e.opts(a)
		o.msgs = []sdk.Msg{&banktypes.MsgUpdateParams{Authority: e.addr(keyAddr(a)), Params: banktypes.DefaultParams()}}
		p, err := e.parser.Parse(e.junkSigTx(o, a.PubKey()))
		must(t, err)
		if p.MsgTypeURL != "/cosmos.bank.v1beta1.MsgUpdateParams" || p.Amount != nil || p.From != "" {
			t.Fatalf("url=%q amount=%v from=%q", p.MsgTypeURL, p.Amount, p.From)
		}
	})

	t.Run("ordered tx reports Unordered=false", func(t *testing.T) {
		o := e.opts(a)
		o.unordered = false
		p, err := e.parser.Parse(e.junkSigTx(o, a.PubKey()))
		must(t, err)
		if p.Unordered {
			t.Fatal("expected ordered")
		}
	})
}

func TestParseMalformed(t *testing.T) {
	e := newEnv(t)
	k := genKey(t, "secp256k1")
	valid := e.signedTx(e.opts(k))

	for name, raw := range map[string][]byte{
		"nil":                   nil,
		"empty":                 {},
		"garbage":               []byte("not a tx"),
		"truncated":             valid[:len(valid)/2],
		"no auth info (no fee)": stripAuthInfo(t, valid), // regression: used to panic in GetSigners
	} {
		t.Run(name, func(t *testing.T) {
			// Must return an error and must NOT panic.
			if p, err := e.parser.Parse(raw); err == nil {
				t.Fatalf("expected error, got %+v", p)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// SignatureVerifier (security critical)
// ---------------------------------------------------------------------------

func TestVerifyAccepts(t *testing.T) {
	for _, kind := range keyKinds {
		t.Run(kind, func(t *testing.T) {
			for _, storePub := range []bool{false, true} {
				name := "pubkey_in_tx_only"
				if storePub {
					name = "pubkey_stored_on_chain"
				}
				t.Run(name, func(t *testing.T) {
					e := newEnv(t)
					k := genKey(t, kind)
					e.addAccount(k, 7, 0, storePub)
					if err := e.verify(e.signedTx(e.opts(k)), nil); err != nil {
						t.Fatalf("valid tx rejected: %v", err)
					}
				})
			}
		})
	}

	t.Run("ordered tx with matching sequence", func(t *testing.T) {
		e := newEnv(t)
		k := genKey(t, "secp256k1")
		e.addAccount(k, 7, 5, false)
		o := e.opts(k)
		o.unordered, o.seq = false, 5
		if err := e.verify(e.signedTx(o), nil); err != nil {
			t.Fatalf("valid ordered tx rejected: %v", err)
		}
	})
}

func TestVerifyRejects(t *testing.T) {
	type tc struct {
		name  string
		build func(e *env, key, other cryptotypes.PrivKey) ([]byte, func(*facilitator.ParsedTx))
	}
	cases := []tc{
		{"wrong chain-id signed", func(e *env, k, _ cryptotypes.PrivKey) ([]byte, func(*facilitator.ParsedTx)) {
			e.addAccount(k, 7, 0, false)
			o := e.opts(k)
			o.chainID = "some-other-chain-1"
			return e.signedTx(o), nil
		}},
		{"wrong account number signed", func(e *env, k, _ cryptotypes.PrivKey) ([]byte, func(*facilitator.ParsedTx)) {
			e.addAccount(k, 7, 0, false)
			o := e.opts(k)
			o.accNum = 8
			return e.signedTx(o), nil
		}},
		{"tampered body (memo) after signing", func(e *env, k, _ cryptotypes.PrivKey) ([]byte, func(*facilitator.ParsedTx)) {
			e.addAccount(k, 7, 0, false)
			return tamperMemo(e.t, e.signedTx(e.opts(k))), nil
		}},
		{"tampered signature bytes", func(e *env, k, _ cryptotypes.PrivKey) ([]byte, func(*facilitator.ParsedTx)) {
			e.addAccount(k, 7, 0, false)
			return tamperSignature(e.t, e.signedTx(e.opts(k))), nil
		}},
		{"unknown account", func(e *env, k, _ cryptotypes.PrivKey) ([]byte, func(*facilitator.ParsedTx)) {
			return e.signedTx(e.opts(k)), nil
		}},
		{"ordered tx, wrong sequence", func(e *env, k, _ cryptotypes.PrivKey) ([]byte, func(*facilitator.ParsedTx)) {
			e.addAccount(k, 7, 0, false)
			o := e.opts(k)
			o.unordered, o.seq = false, 3
			return e.signedTx(o), nil
		}},
		{"unordered tx with non-zero sequence", func(e *env, k, _ cryptotypes.PrivKey) ([]byte, func(*facilitator.ParsedTx)) {
			e.addAccount(k, 7, 0, false)
			o := e.opts(k)
			o.seq = 1
			return e.signedTx(o), nil
		}},
		{"stored pubkey differs from tx pubkey", func(e *env, k, other cryptotypes.PrivKey) ([]byte, func(*facilitator.ParsedTx)) {
			e.setAccount(keyAddr(k), other.PubKey(), 7, 0)
			return e.signedTx(e.opts(k)), nil
		}},
		{"signed by a key that does not own from_address", func(e *env, k, other cryptotypes.PrivKey) ([]byte, func(*facilitator.ParsedTx)) {
			e.addAccount(k, 7, 0, false)
			o := e.opts(other)  // signs with `other`...
			o.from = keyAddr(k) // ...but the message spends k's funds
			return e.signedTx(o), nil
		}},
		{"two signers", func(e *env, k, other cryptotypes.PrivKey) ([]byte, func(*facilitator.ParsedTx)) {
			e.addAccount(k, 7, 0, false)
			e.addAccount(other, 8, 0, false)
			o := e.opts(k)
			o.extraMsgs = []sdk.Msg{banktypes.NewMsgSend(keyAddr(other), o.to, o.coins)}
			return e.junkSigTx(o, k.PubKey(), other.PubKey()), nil
		}},
		{"parsed signer does not match raw tx", func(e *env, k, other cryptotypes.PrivKey) ([]byte, func(*facilitator.ParsedTx)) {
			e.addAccount(k, 7, 0, false)
			e.addAccount(other, 8, 0, false)
			return e.signedTx(e.opts(k)), func(p *facilitator.ParsedTx) { p.Signer = e.addr(keyAddr(other)) }
		}},
	}

	for _, kind := range keyKinds {
		t.Run(kind, func(t *testing.T) {
			for _, c := range cases {
				t.Run(c.name, func(t *testing.T) {
					e := newEnv(t)
					raw, mutate := c.build(e, genKey(t, kind), genKey(t, kind))
					if err := e.verify(raw, mutate); err == nil {
						t.Fatal("SECURITY: invalid tx was accepted (Verify returned nil)")
					}
				})
			}
		})
	}
}

func TestVerifyRejectsEmptyInput(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for name, p := range map[string]*facilitator.ParsedTx{
		"nil":     nil,
		"empty":   {},
		"garbage": {Raw: []byte("junk")},
		"no fee":  {Raw: stripAuthInfo(t, e.signedTx(e.opts(genKey(t, "secp256k1"))))}, // regression: panic
	} {
		t.Run(name, func(t *testing.T) {
			if err := e.ver.Verify(ctx, p); err == nil {
				t.Fatal("SECURITY: Verify returned nil for unusable input")
			}
		})
	}
}

func TestVerifyFailsClosedWhenNodeDown(t *testing.T) {
	e := newEnv(t)
	k := genKey(t, "secp256k1")
	e.addAccount(k, 7, 0, false)
	raw := e.signedTx(e.opts(k))

	// Point a second verifier at a dead address.
	dead, err := NewSDKSignatureVerifier(e.txCfg, e.reg, e.addrCdc, "127.0.0.1:1", testChainID)
	must(t, err)
	defer dead.Close()
	p, err := e.parser.Parse(raw)
	must(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := dead.Verify(ctx, p); err == nil {
		t.Fatal("SECURITY: Verify returned nil with the node unreachable")
	}
}

func TestNewVerifierRequiresChainID(t *testing.T) {
	e := newEnv(t)
	if _, err := NewSDKSignatureVerifier(e.txCfg, e.reg, e.addrCdc, e.grpcAddr, ""); err == nil {
		t.Fatal("empty chain-id must be rejected")
	}
}

// ---------------------------------------------------------------------------
// ChainReader
// ---------------------------------------------------------------------------

func newChain(t testing.TB, e *env, moduleNames ...string) *GRPCChain {
	t.Helper()
	c, err := NewGRPCChain(e.grpcAddr, e.reg, e.addrCdc, moduleNames)
	must(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestChainBalance(t *testing.T) {
	e := newEnv(t)
	c := newChain(t, e)
	ctx := context.Background()
	k := genKey(t, "secp256k1")
	addr := e.addr(keyAddr(k))

	e.st.mu.Lock()
	e.st.balances[addr+"|utest"] = sdkmath.NewInt(12345)
	e.st.mu.Unlock()

	got, err := c.Balance(ctx, addr, "utest")
	must(t, err)
	if got.Int64() != 12345 {
		t.Fatalf("balance %v", got)
	}
	zero, err := c.Balance(ctx, addr, "unknowndenom")
	must(t, err)
	if zero.Sign() != 0 {
		t.Fatalf("expected zero, got %v", zero)
	}
}

func TestChainIsModuleAccount(t *testing.T) {
	e := newEnv(t)
	c := newChain(t, e, "gov")
	ctx := context.Background()

	t.Run("derived from module names, no on-chain account needed", func(t *testing.T) {
		ok, err := c.IsModuleAccount(ctx, e.addr(authtypes.NewModuleAddress("gov")))
		if err != nil || !ok {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
	})

	t.Run("on-chain module account not in the name list", func(t *testing.T) {
		ma := authtypes.NewEmptyModuleAccount("ibc_transfer")
		e.st.mu.Lock()
		e.st.module[ma.Address] = ma
		e.st.mu.Unlock()
		ok, err := c.IsModuleAccount(ctx, ma.Address)
		if err != nil || !ok {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
	})

	t.Run("ordinary account is not a module account", func(t *testing.T) {
		k := genKey(t, "secp256k1")
		e.addAccount(k, 9, 0, true)
		ok, err := c.IsModuleAccount(ctx, e.addr(keyAddr(k)))
		if err != nil || ok {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
	})

	t.Run("unknown address is not a module account", func(t *testing.T) {
		k := genKey(t, "secp256k1")
		ok, err := c.IsModuleAccount(ctx, e.addr(keyAddr(k)))
		if err != nil || ok {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
	})

	t.Run("malformed address is an error (fail closed)", func(t *testing.T) {
		ok, err := c.IsModuleAccount(ctx, "not-a-bech32-address")
		if err == nil || ok {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
	})
}

func TestChainSimulate(t *testing.T) {
	e := newEnv(t)
	c := newChain(t, e)
	ctx := context.Background()

	e.st.mu.Lock()
	e.st.simGas = 77777
	e.st.mu.Unlock()
	gas, err := c.Simulate(ctx, []byte("tx"))
	must(t, err)
	if gas != 77777 {
		t.Fatalf("gas %d", gas)
	}

	e.st.mu.Lock()
	e.st.simErr = status.Error(codes.InvalidArgument, "insufficient funds")
	e.st.mu.Unlock()
	if _, err := c.Simulate(ctx, []byte("tx")); err == nil {
		t.Fatal("simulation failure must be an error")
	}
}

// ---------------------------------------------------------------------------
// Broadcaster
// ---------------------------------------------------------------------------

func newBroadcaster(t testing.TB, e *env) *NodeBroadcaster {
	t.Helper()
	b, err := NewNodeBroadcaster(e.grpcAddr)
	must(t, err)
	t.Cleanup(func() { _ = b.Close() })
	return b
}

func TestBroadcast(t *testing.T) {
	e := newEnv(t)
	b := newBroadcaster(t, e)
	ctx := context.Background()
	raw := []byte("signed-tx-bytes")

	t.Run("success returns the tx hash and uses SYNC mode", func(t *testing.T) {
		hash, err := b.Broadcast(ctx, raw)
		must(t, err)
		if hash != facilitator.TxHash(raw) {
			t.Fatalf("hash %q", hash)
		}
		e.st.mu.Lock()
		defer e.st.mu.Unlock()
		if e.st.lastMode != sdktx.BroadcastMode_BROADCAST_MODE_SYNC {
			t.Fatalf("mode %v", e.st.lastMode)
		}
		if !bytes.Equal(e.st.broadcasts[len(e.st.broadcasts)-1], raw) {
			t.Fatal("bytes must be sent unchanged")
		}
	})

	t.Run("non-zero CheckTx code is an error carrying the log", func(t *testing.T) {
		e.st.mu.Lock()
		e.st.bcCode, e.st.bcLog = 5, "insufficient funds"
		e.st.mu.Unlock()
		hash, err := b.Broadcast(ctx, raw)
		if err == nil || hash != "" || !strings.Contains(err.Error(), "insufficient funds") {
			t.Fatalf("hash=%q err=%v", hash, err)
		}
	})

	t.Run("transport error is an error", func(t *testing.T) {
		e.st.mu.Lock()
		e.st.bcCode, e.st.bcLog = 0, ""
		e.st.bcErr = status.Error(codes.Unavailable, "node down")
		e.st.mu.Unlock()
		if _, err := b.Broadcast(ctx, raw); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestTxResult(t *testing.T) {
	e := newEnv(t)
	b := newBroadcaster(t, e)
	ctx := context.Background()

	t.Run("not found is found=false, not an error", func(t *testing.T) {
		found, code, log, err := b.TxResult(ctx, "ABCDEF")
		if err != nil || found || code != 0 || log != "" {
			t.Fatalf("found=%v code=%d log=%q err=%v", found, code, log, err)
		}
	})

	t.Run("included successfully", func(t *testing.T) {
		e.st.mu.Lock()
		e.st.txs["AA"] = &sdk.TxResponse{Code: 0, RawLog: "ok"}
		e.st.mu.Unlock()
		found, code, log, err := b.TxResult(ctx, "AA")
		if err != nil || !found || code != 0 || log != "ok" {
			t.Fatalf("found=%v code=%d log=%q err=%v", found, code, log, err)
		}
	})

	t.Run("included with failure code", func(t *testing.T) {
		e.st.mu.Lock()
		e.st.txs["BB"] = &sdk.TxResponse{Code: 11, RawLog: "out of gas"}
		e.st.mu.Unlock()
		found, code, log, err := b.TxResult(ctx, "BB")
		if err != nil || !found || code != 11 || log != "out of gas" {
			t.Fatalf("found=%v code=%d log=%q err=%v", found, code, log, err)
		}
	})

	t.Run("other errors are errors", func(t *testing.T) {
		e.st.mu.Lock()
		e.st.getTxErr = status.Error(codes.Internal, "boom")
		e.st.mu.Unlock()
		found, _, _, err := b.TxResult(ctx, "CC")
		if err == nil || found {
			t.Fatalf("found=%v err=%v", found, err)
		}
	})
}
