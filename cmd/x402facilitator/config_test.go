package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validConfig() Config {
	return Config{
		Network:         "cosmos:my-testnet-1",
		Asset:           "ucheck",
		MemoPrefix:      "x402:",
		AllowedKeyTypes: []string{"/cosmos.crypto.secp256k1.PubKey"},
		MinRemaining:    "5s",
		MaxWindow:       "60s",
		MinFeeReserve:   "100",
		GRPCAddr:        "localhost:9090",
	}
}

func TestValidConfigResolves(t *testing.T) {
	r, warns, err := validConfig().Resolve("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if r.ListenAddr != "127.0.0.1:8402" || r.PollInterval.String() != "500ms" || r.ConfirmTimeout.String() != "15s" {
		t.Fatalf("defaults not applied: %+v", r)
	}
}

func TestConfigRejections(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
		token  string
		want   string
	}{
		{"bad network", func(c *Config) { c.Network = "eip155:1" }, "", "network"},
		{"bad asset", func(c *Config) { c.Asset = "1" }, "", "asset"},
		{"empty memo prefix", func(c *Config) { c.MemoPrefix = "" }, "", "memoPrefix"},
		{"no key types", func(c *Config) { c.AllowedKeyTypes = nil }, "", "allowedKeyTypes"},
		{"key type not a type url", func(c *Config) { c.AllowedKeyTypes = []string{"secp256k1"} }, "", "type URL"},
		{"missing minRemaining", func(c *Config) { c.MinRemaining = "" }, "", "minRemaining is required"},
		{"window below remaining", func(c *Config) { c.MaxWindow = "3s" }, "", "greater than minRemaining"},
		{"window beyond chain limit", func(c *Config) { c.MaxWindow = "11m" }, "", "unordered-tx limit"},
		{"negative reserve", func(c *Config) { c.MinFeeReserve = "-1" }, "", "minFeeReserve"},
		{"sponsor not implemented", func(c *Config) { c.SponsorAddr = "cosmos1x" }, "", "not implemented"},
		{"missing grpc", func(c *Config) { c.GRPCAddr = "" }, "", "grpcAddr is required"},
		{"half tls", func(c *Config) { c.TLSCertFile = "c.pem" }, "", "set together"},
		{"public listener needs token", func(c *Config) { c.ListenAddr = "0.0.0.0:8402" }, "", "X402_FACILITATOR_TOKEN"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validConfig()
			tc.mutate(&c)
			_, _, err := c.Resolve(tc.token)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestWarnings(t *testing.T) {
	c := validConfig()
	c.MinFeeReserve = "0"
	c.GRPCAddr = "10.0.0.5:9090"
	c.ListenAddr = "0.0.0.0:8402"
	_, warns, err := c.Resolve("secret")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(warns, "|")
	for _, want := range []string{"minFeeReserve is 0", "not loopback", "without TLS"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing warning %q in %q", want, joined)
		}
	}
}

func TestLoadConfigStrict(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.json")
	if err := os.WriteFile(good, []byte(`{"network":"cosmos:t-1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(good); err != nil {
		t.Fatalf("good file: %v", err)
	}
	typo := filepath.Join(dir, "typo.json")
	if err := os.WriteFile(typo, []byte(`{"netwrok":"cosmos:t-1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(typo); err == nil {
		t.Fatal("unknown fields must be rejected")
	}
}

func TestExampleConfigIsValid(t *testing.T) {
	c, err := LoadConfig("x402-facilitator.example.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Resolve(""); err != nil {
		t.Fatalf("example config should validate: %v", err)
	}
}
