package x402facilitator

import (
	"context"
	"encoding/base64"
	"errors"
	"math/big"
	"sync/atomic"
	"testing"
	"time"
)

const (
	secp  = "/cosmos.crypto.secp256k1.PubKey"
	mldsa = "/cosmos.crypto.mldsa65.PubKey"
)

var t0 = time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)

// ---- fakes ----

type fakeParser struct{ tx *ParsedTx }

func (f fakeParser) Parse(raw []byte) (*ParsedTx, error) {
	if f.tx == nil {
		return nil, errors.New("bad tx")
	}
	c := *f.tx
	c.Raw = raw
	return &c, nil
}

type fakeSig struct{ err error }

func (f fakeSig) Verify(context.Context, *ParsedTx) error { return f.err }

type fakeChain struct {
	balance   *big.Int
	moduleAcc bool
	simErr    error
}

func (f fakeChain) Balance(context.Context, string, string) (*big.Int, error) { return f.balance, nil }
func (f fakeChain) IsModuleAccount(context.Context, string) (bool, error)     { return f.moduleAcc, nil }
func (f fakeChain) Simulate(context.Context, []byte) (uint64, error)          { return 100000, f.simErr }

type fakeBroadcaster struct {
	broadcasts atomic.Int32
	code       uint32
}

func (f *fakeBroadcaster) Broadcast(context.Context, []byte) (string, error) {
	f.broadcasts.Add(1)
	return "HASH", nil
}
func (f *fakeBroadcaster) TxResult(context.Context, string) (bool, uint32, string, error) {
	return true, f.code, "", nil
}

// ---- helpers ----

func goodTx() *ParsedTx {
	return &ParsedTx{
		MsgCount: 1, MsgTypeURL: MsgSendTypeURL, SignerCount: 1,
		From: "chain1payer", To: "chain1payee", Denom: "ucheck", Amount: big.NewInt(1000),
		Signer: "chain1payer", SignerKeyType: secp,
		Memo: "x402:pay-1", Unordered: true, Sequence: 0,
		TimeoutTimestamp: t0.Add(30 * time.Second),
	}
}

func goodReq() VerifyRequest {
	return VerifyRequest{
		X402Version: 2,
		PaymentPayload: PaymentPayload{
			X402Version: 2, Scheme: SchemeExact, Network: "cosmos:test-1",
			Payload: CosmosPayload{Transport: TransportInline, KeyType: secp,
				Tx: base64.StdEncoding.EncodeToString([]byte("signed-tx-bytes"))},
		},
		PaymentRequirements: PaymentRequirements{
			Scheme: SchemeExact, Network: "cosmos:test-1", Amount: "1000", Asset: "ucheck",
			PayTo: "chain1payee", MaxTimeoutSeconds: 60,
			Extra: RequirementsExtra{Flow: FlowSend, PaymentID: "pay-1"},
		},
	}
}

func newVerifier(tx *ParsedTx, chain fakeChain, sig fakeSig) *Verifier {
	return &Verifier{
		Policy: Policy{
			Network: "cosmos:test-1", Asset: "ucheck",
			AllowedKeyTypes: map[string]bool{secp: true, mldsa: true},
			MinRemaining:    5 * time.Second, MaxWindow: 120 * time.Second,
			MemoPrefix: "x402:", MinFeeBalance: big.NewInt(50),
		},
		Parser: fakeParser{tx}, Sig: sig, Chain: chain,
		Now: func() time.Time { return t0 },
	}
}

func okChain() fakeChain { return fakeChain{balance: big.NewInt(1_000_000)} }

// ---- verify tests ----

func TestVerifyHappyPath(t *testing.T) {
	v := newVerifier(goodTx(), okChain(), fakeSig{})
	r := v.Verify(context.Background(), goodReq())
	if !r.IsValid || r.Payer != "chain1payer" {
		t.Fatalf("expected valid, got %+v", r)
	}
}

func TestVerifyRejections(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ParsedTx, *VerifyRequest, *fakeChain, *fakeSig)
		reason string
	}{
		{"wrong recipient", func(tx *ParsedTx, _ *VerifyRequest, _ *fakeChain, _ *fakeSig) { tx.To = "chain1evil" }, ReasonRecipientMismatch},
		{"underpay", func(tx *ParsedTx, _ *VerifyRequest, _ *fakeChain, _ *fakeSig) { tx.Amount = big.NewInt(999) }, ReasonAmountMismatch},
		{"overpay is also not exact", func(tx *ParsedTx, _ *VerifyRequest, _ *fakeChain, _ *fakeSig) { tx.Amount = big.NewInt(1001) }, ReasonAmountMismatch},
		{"wrong denom", func(tx *ParsedTx, _ *VerifyRequest, _ *fakeChain, _ *fakeSig) { tx.Denom = "stake" }, ReasonAmountMismatch},
		{"extra message", func(tx *ParsedTx, _ *VerifyRequest, _ *fakeChain, _ *fakeSig) { tx.MsgCount = 2 }, ReasonUnexpectedMessages},
		{"not a send", func(tx *ParsedTx, _ *VerifyRequest, _ *fakeChain, _ *fakeSig) {
			tx.MsgTypeURL = "/cosmos.authz.v1beta1.MsgGrant"
		}, ReasonUnexpectedMessages},
		{"ordered tx", func(tx *ParsedTx, _ *VerifyRequest, _ *fakeChain, _ *fakeSig) { tx.Unordered = false }, ReasonNotUnordered},
		{"non-zero sequence", func(tx *ParsedTx, _ *VerifyRequest, _ *fakeChain, _ *fakeSig) { tx.Sequence = 3 }, ReasonBadSequence},
		{"expires too soon", func(tx *ParsedTx, _ *VerifyRequest, _ *fakeChain, _ *fakeSig) {
			tx.TimeoutTimestamp = t0.Add(2 * time.Second)
		}, ReasonExpiryTooSoon},
		{"already expired", func(tx *ParsedTx, _ *VerifyRequest, _ *fakeChain, _ *fakeSig) {
			tx.TimeoutTimestamp = t0.Add(-time.Second)
		}, ReasonExpiryTooSoon},
		{"expires beyond requirement window", func(tx *ParsedTx, _ *VerifyRequest, _ *fakeChain, _ *fakeSig) {
			tx.TimeoutTimestamp = t0.Add(90 * time.Second)
		}, ReasonExpiryTooFar},
		{"memo wrong id", func(tx *ParsedTx, _ *VerifyRequest, _ *fakeChain, _ *fakeSig) { tx.Memo = "x402:other" }, ReasonMemoMismatch},
		{"memo no prefix", func(tx *ParsedTx, _ *VerifyRequest, _ *fakeChain, _ *fakeSig) { tx.Memo = "pay-1" }, ReasonMemoMismatch},
		{"fee granter not allowed", func(tx *ParsedTx, _ *VerifyRequest, _ *fakeChain, _ *fakeSig) { tx.FeeGranter = "chain1x" }, ReasonFeeFieldsNotAllowed},
		{"payTo is module account", func(_ *ParsedTx, _ *VerifyRequest, c *fakeChain, _ *fakeSig) { c.moduleAcc = true }, ReasonPayToModuleAccount},
		{"bad signature", func(_ *ParsedTx, _ *VerifyRequest, _ *fakeChain, s *fakeSig) { s.err = errors.New("bad sig") }, ReasonBadSignature},
		{"insufficient funds incl. fee", func(_ *ParsedTx, _ *VerifyRequest, c *fakeChain, _ *fakeSig) { c.balance = big.NewInt(1020) }, ReasonInsufficientFunds},
		{"simulation fails", func(_ *ParsedTx, _ *VerifyRequest, c *fakeChain, _ *fakeSig) { c.simErr = errors.New("boom") }, ReasonSimulationFailed},
		{"wrong network", func(_ *ParsedTx, r *VerifyRequest, _ *fakeChain, _ *fakeSig) {
			r.PaymentRequirements.Network = "cosmos:other"
		}, ReasonUnsupportedNetwork},
		{"wrong asset", func(_ *ParsedTx, r *VerifyRequest, _ *fakeChain, _ *fakeSig) { r.PaymentRequirements.Asset = "stake" }, ReasonUnsupportedAsset},
		{"reference transport unsupported", func(_ *ParsedTx, r *VerifyRequest, _ *fakeChain, _ *fakeSig) {
			r.PaymentPayload.Payload.Transport = TransportReference
		}, ReasonUnsupportedTransport},
		{"payload key type lies", func(_ *ParsedTx, r *VerifyRequest, _ *fakeChain, _ *fakeSig) {
			r.PaymentPayload.Payload.KeyType = mldsa
		}, ReasonKeyTypeNotAllowed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx, req, chain, sig := goodTx(), goodReq(), okChain(), fakeSig{}
			tc.mutate(tx, &req, &chain, &sig)
			got := newVerifier(tx, chain, sig).Verify(context.Background(), req)
			if got.IsValid || got.InvalidReason != tc.reason {
				t.Fatalf("want reason %q, got %+v", tc.reason, got)
			}
		})
	}
}

// Classic keys are phased out by removing them from the allowed list.
func TestClassicPhaseOut(t *testing.T) {
	v := newVerifier(goodTx(), okChain(), fakeSig{})
	v.Policy.AllowedKeyTypes = map[string]bool{mldsa: true}
	got := v.Verify(context.Background(), goodReq())
	if got.IsValid || got.InvalidReason != ReasonKeyTypeNotAllowed {
		t.Fatalf("classic key should be rejected after phase-out, got %+v", got)
	}
}

func TestPQKeyAccepted(t *testing.T) {
	tx, req := goodTx(), goodReq()
	tx.SignerKeyType = mldsa
	req.PaymentPayload.Payload.KeyType = mldsa
	if r := newVerifier(tx, okChain(), fakeSig{}).Verify(context.Background(), req); !r.IsValid {
		t.Fatalf("expected PQ key to verify, got %+v", r)
	}
}

func TestSponsoredFeeFields(t *testing.T) {
	tx := goodTx()
	tx.FeePayer = "chain1sponsor"
	v := newVerifier(tx, okChain(), fakeSig{})
	v.Policy.SponsorAddr = "chain1sponsor"
	if r := v.Verify(context.Background(), goodReq()); !r.IsValid {
		t.Fatalf("sponsor as fee payer should be allowed, got %+v", r)
	}
	tx.FeePayer = "chain1someoneelse"
	if r := v.Verify(context.Background(), goodReq()); r.IsValid || r.InvalidReason != ReasonFeeFieldsNotAllowed {
		t.Fatalf("foreign fee payer should be rejected, got %+v", r)
	}
}

// ---- settle tests ----

func newSettler(bc *fakeBroadcaster) *Settler {
	return &Settler{
		Verifier:       newVerifier(goodTx(), okChain(), fakeSig{}),
		Broadcaster:    bc,
		Store:          NewMemoryStore(),
		PollInterval:   time.Millisecond,
		ConfirmTimeout: time.Second,
	}
}

func TestSettleIsIdempotent(t *testing.T) {
	bc := &fakeBroadcaster{}
	s := newSettler(bc)
	first := s.Settle(context.Background(), goodReq())
	if !first.Success || first.Transaction == "" {
		t.Fatalf("expected success, got %+v", first)
	}
	second := s.Settle(context.Background(), goodReq())
	if second != first {
		t.Fatalf("retry must return the first outcome: %+v vs %+v", second, first)
	}
	if n := bc.broadcasts.Load(); n != 1 {
		t.Fatalf("must broadcast exactly once, got %d", n)
	}
}

func TestSettleOnChainFailure(t *testing.T) {
	bc := &fakeBroadcaster{code: 5}
	got := newSettler(bc).Settle(context.Background(), goodReq())
	if got.Success || got.ErrorReason != ReasonTxFailed {
		t.Fatalf("expected tx_failed, got %+v", got)
	}
}

func TestSettleInvalidPaymentReleasesClaim(t *testing.T) {
	bc := &fakeBroadcaster{}
	s := newSettler(bc)
	bad := goodReq()
	bad.PaymentRequirements.PayTo = "chain1evil"
	if got := s.Settle(context.Background(), bad); got.Success || got.ErrorReason != ReasonRecipientMismatch {
		t.Fatalf("expected recipient_mismatch, got %+v", got)
	}
	if bc.broadcasts.Load() != 0 {
		t.Fatal("must not broadcast an invalid payment")
	}
	// The claim was released, so a corrected request can still settle.
	if got := s.Settle(context.Background(), goodReq()); !got.Success {
		t.Fatalf("valid retry should succeed, got %+v", got)
	}
}
