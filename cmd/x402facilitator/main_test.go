package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lucidpay/lucidchain/x402/adapters"
	"github.com/lucidpay/lucidchain/x402/facilitator"
)

func testResolved(t *testing.T) Resolved {
	t.Helper()
	r, _, err := validConfig().Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// testHandler wires the REAL adapters. They dial lazily, so no node needs to be
// running: requests that reach the node simply fail (and must fail closed).
func testHandler(t *testing.T, token string) http.Handler {
	t.Helper()
	res := testResolved(t)
	set, err := newAdapters(res, "cosmos", "my-testnet-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(set.Close)
	return buildHandler(res, token, set)
}

func do(h http.Handler, method, path, body, auth string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func payment() string {
	req := facilitator.VerifyRequest{
		X402Version: 2,
		PaymentPayload: facilitator.PaymentPayload{
			X402Version: 2, Scheme: "exact", Network: "cosmos:my-testnet-1",
			Payload: facilitator.CosmosPayload{
				Transport: "inline", KeyType: "/cosmos.crypto.secp256k1.PubKey",
				Tx: base64.StdEncoding.EncodeToString([]byte("any bytes")),
			},
		},
		PaymentRequirements: facilitator.PaymentRequirements{
			Scheme: "exact", Network: "cosmos:my-testnet-1", Amount: "1000", Asset: "ucheck",
			PayTo: "cosmos1payee", MaxTimeoutSeconds: 60,
		},
	}
	b, _ := json.Marshal(req)
	return string(b)
}

// A payload that is not a decodable transaction must never be approved or settled.
// (Full valid-payment coverage lives in x402/adapters and x402/facilitator tests.)
func TestMalformedPaymentIsRejected(t *testing.T) {
	h := testHandler(t, "")

	var v facilitator.VerifyResponse
	rec := do(h, http.MethodPost, "/verify", payment(), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("verify status %d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v.IsValid {
		t.Fatal("a malformed payment was approved in /verify")
	}
	if v.InvalidReason != facilitator.ReasonMalformedPayload {
		t.Fatalf("want %q, got %q", facilitator.ReasonMalformedPayload, v.InvalidReason)
	}

	var s facilitator.SettleResponse
	rec = do(h, http.MethodPost, "/settle", payment(), "")
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	if s.Success {
		t.Fatal("a malformed payment was settled")
	}
	if s.ErrorReason != facilitator.ReasonMalformedPayload {
		t.Fatalf("want %q, got %q", facilitator.ReasonMalformedPayload, s.ErrorReason)
	}
}

func TestSupportedAndHealth(t *testing.T) {
	h := testHandler(t, "")
	rec := do(h, http.MethodGet, "/supported", "", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "cosmos:my-testnet-1") {
		t.Fatalf("supported: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(h, http.MethodGet, "/healthz", "", ""); rec.Code != 200 {
		t.Fatalf("healthz: %d", rec.Code)
	}
}

func TestTokenAuth(t *testing.T) {
	h := testHandler(t, "s3cret")
	if rec := do(h, http.MethodPost, "/verify", payment(), ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: want 401, got %d", rec.Code)
	}
	if rec := do(h, http.MethodPost, "/verify", payment(), "Bearer wrong"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: want 401, got %d", rec.Code)
	}
	if rec := do(h, http.MethodPost, "/verify", payment(), "Bearer s3cret"); rec.Code != http.StatusOK {
		t.Fatalf("right token: want 200, got %d", rec.Code)
	}
	// /supported and /healthz stay public so resource servers can discover support.
	if rec := do(h, http.MethodGet, "/supported", "", ""); rec.Code != 200 {
		t.Fatalf("supported should be public, got %d", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/healthz", "", ""); rec.Code != 200 {
		t.Fatalf("healthz should be public, got %d", rec.Code)
	}
}

func TestRunRefusesToStartUntilImplemented(t *testing.T) {
	if adapters.Implemented {
		t.Skip("adapters.Implemented=true: run would start a real server, so this refusal test no longer applies")
	}
	var out bytes.Buffer
	err := run(context.Background(),
		[]string{"-config", "x402-facilitator.example.json", "-bech32-prefix", "cosmos"}, &out)
	if err == nil || !strings.Contains(err.Error(), "Implemented") {
		t.Fatalf("expected refusal to start while adapters.Implemented=false, got %v", err)
	}
}

func TestRunRequiresBech32Prefix(t *testing.T) {
	var out bytes.Buffer
	err := run(context.Background(), []string{"-config", "x402-facilitator.example.json"}, &out)
	if err == nil || !strings.Contains(err.Error(), "-bech32-prefix") {
		t.Fatalf("expected -bech32-prefix error, got %v", err)
	}
}

func TestRunCheckConfig(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"-config", "x402-facilitator.example.json", "-check-config"}, &out); err != nil {
		t.Fatalf("check-config should pass on the example: %v", err)
	}
	if !strings.Contains(out.String(), "config OK") {
		t.Fatalf("missing confirmation in output: %s", out.String())
	}
}

func TestRunRequiresConfigFlag(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), nil, &out); err == nil {
		t.Fatal("expected error without -config")
	}
}

func TestSplitList(t *testing.T) {
	got := splitList(" a, b ,,c ")
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("got %q", got)
	}
	if len(splitList("")) != 0 {
		t.Fatal("empty input must give an empty list")
	}
}
