package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"regexp"
	"strings"
	"time"
)

// Config is the on-disk (JSON) configuration. Secrets do not belong here: the
// API token comes from the X402_FACILITATOR_TOKEN environment variable and
// signing keys (later, for sponsored fees) belong in a keyring backend or KMS.
//
// Durations use Go syntax ("30s", "500ms"). There are deliberately no defaults
// for chain-specific values (network, asset, key types, expiry window) so
// nothing is guessed.
type Config struct {
	ListenAddr string `json:"listenAddr"` // default 127.0.0.1:8402

	Network    string `json:"network"`    // CAIP-2, e.g. cosmos:my-testnet-1
	Asset      string `json:"asset"`      // base denom
	MemoPrefix string `json:"memoPrefix"` // e.g. "x402:"

	// Pubkey type URLs accepted for payments. Verify the exact strings against
	// your build's registered key types. Remove the classic type to phase it out.
	AllowedKeyTypes []string `json:"allowedKeyTypes"`

	MinRemaining string `json:"minRemaining"` // least time left before tx expiry
	MaxWindow    string `json:"maxWindow"`    // most time left; must be <= 10m (SDK default limit)

	// Integer base-denom units kept in reserve for fees when the payer pays them.
	// Set from gas profiling.
	MinFeeReserve string `json:"minFeeReserve"`

	// Sponsored fee mode is not implemented yet; this must stay empty.
	SponsorAddr string `json:"sponsorAddr"`

	GRPCAddr string `json:"grpcAddr"` // node gRPC, keep on localhost

	PollInterval   string `json:"pollInterval"`   // default 500ms
	ConfirmTimeout string `json:"confirmTimeout"` // default 15s

	TLSCertFile string `json:"tlsCertFile"`
	TLSKeyFile  string `json:"tlsKeyFile"`
}

// Resolved is the validated, parsed form of Config.
type Resolved struct {
	ListenAddr      string
	Network         string
	Asset           string
	MemoPrefix      string
	AllowedKeyTypes map[string]bool
	MinRemaining    time.Duration
	MaxWindow       time.Duration
	MinFeeReserve   *big.Int
	GRPCAddr        string
	PollInterval    time.Duration
	ConfirmTimeout  time.Duration
	TLSCertFile     string
	TLSKeyFile      string
}

// maxUnorderedWindow is the SDK's default maximum unordered-tx timeout. If you
// change it on the chain, change it here too.
const maxUnorderedWindow = 10 * time.Minute

var (
	networkRE = regexp.MustCompile(`^cosmos:[-a-zA-Z0-9]{1,32}$`)
	denomRE   = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9/:._-]{2,127}$`)
)

// LoadConfig reads and strictly decodes the JSON config (unknown fields are errors).
func LoadConfig(path string) (Config, error) {
	var c Config
	f, err := os.Open(path)
	if err != nil {
		return c, err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return c, fmt.Errorf("parse %s: %w", path, err)
	}
	return c, nil
}

// Resolve validates the config (plus the API token) and returns the parsed
// values and any non-fatal warnings.
func (c Config) Resolve(token string) (Resolved, []string, error) {
	var r Resolved
	var warns []string
	var errs []error
	bad := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	r.ListenAddr = c.ListenAddr
	if r.ListenAddr == "" {
		r.ListenAddr = "127.0.0.1:8402"
	}
	if _, _, err := net.SplitHostPort(r.ListenAddr); err != nil {
		bad("listenAddr %q: %v", r.ListenAddr, err)
	}

	if !networkRE.MatchString(c.Network) {
		bad("network %q must look like cosmos:<chain-id>", c.Network)
	}
	r.Network = c.Network

	if !denomRE.MatchString(c.Asset) {
		bad("asset %q is not a valid denom", c.Asset)
	}
	r.Asset = c.Asset

	if c.MemoPrefix == "" || len(c.MemoPrefix) > 32 {
		bad("memoPrefix must be 1-32 characters")
	}
	r.MemoPrefix = c.MemoPrefix

	r.AllowedKeyTypes = map[string]bool{}
	if len(c.AllowedKeyTypes) == 0 {
		bad("allowedKeyTypes must list at least one pubkey type URL")
	}
	for _, k := range c.AllowedKeyTypes {
		if !strings.HasPrefix(k, "/") {
			bad("allowedKeyTypes entry %q must be a type URL starting with /", k)
		}
		r.AllowedKeyTypes[k] = true
	}

	var err error
	if r.MinRemaining, err = parsePositiveDuration("minRemaining", c.MinRemaining, 0); err != nil {
		errs = append(errs, err)
	}
	if r.MaxWindow, err = parsePositiveDuration("maxWindow", c.MaxWindow, 0); err != nil {
		errs = append(errs, err)
	}
	if r.MinRemaining > 0 && r.MaxWindow > 0 {
		if r.MaxWindow <= r.MinRemaining {
			bad("maxWindow (%s) must be greater than minRemaining (%s)", r.MaxWindow, r.MinRemaining)
		}
		if r.MaxWindow > maxUnorderedWindow {
			bad("maxWindow (%s) exceeds the chain's unordered-tx limit (%s)", r.MaxWindow, maxUnorderedWindow)
		}
	}

	r.MinFeeReserve = new(big.Int)
	if _, ok := r.MinFeeReserve.SetString(c.MinFeeReserve, 10); !ok || r.MinFeeReserve.Sign() < 0 {
		bad("minFeeReserve %q must be a non-negative integer", c.MinFeeReserve)
	} else if r.MinFeeReserve.Sign() == 0 {
		warns = append(warns, "minFeeReserve is 0: payers may pass verify but fail to pay fees; set it from gas profiling")
	}

	if c.SponsorAddr != "" {
		bad("sponsorAddr is set but sponsored fee mode is not implemented yet")
	}

	if c.GRPCAddr == "" {
		bad("grpcAddr is required")
	} else if host, _, err := net.SplitHostPort(c.GRPCAddr); err != nil {
		bad("grpcAddr %q: %v", c.GRPCAddr, err)
	} else if !isLoopbackHost(host) {
		warns = append(warns, "grpcAddr is not loopback: run the facilitator next to a dedicated node and keep gRPC on localhost")
	}
	r.GRPCAddr = c.GRPCAddr

	if r.PollInterval, err = parsePositiveDuration("pollInterval", c.PollInterval, 500*time.Millisecond); err != nil {
		errs = append(errs, err)
	}
	if r.ConfirmTimeout, err = parsePositiveDuration("confirmTimeout", c.ConfirmTimeout, 15*time.Second); err != nil {
		errs = append(errs, err)
	}
	if r.PollInterval > 0 && r.ConfirmTimeout > 0 && r.ConfirmTimeout < r.PollInterval {
		bad("confirmTimeout must be at least pollInterval")
	}

	if (c.TLSCertFile == "") != (c.TLSKeyFile == "") {
		bad("tlsCertFile and tlsKeyFile must be set together")
	}
	r.TLSCertFile, r.TLSKeyFile = c.TLSCertFile, c.TLSKeyFile

	// Exposure rules: a non-loopback listener needs an API token.
	if host, _, err := net.SplitHostPort(r.ListenAddr); err == nil && !isLoopbackHost(host) {
		if token == "" {
			bad("listenAddr %q is not loopback: set X402_FACILITATOR_TOKEN", r.ListenAddr)
		}
		if r.TLSCertFile == "" {
			warns = append(warns, "non-loopback listener without TLS: terminate TLS in front of it")
		}
	}

	if len(errs) > 0 {
		return Resolved{}, warns, errors.Join(errs...)
	}
	return r, warns, nil
}

func parsePositiveDuration(name, s string, def time.Duration) (time.Duration, error) {
	if s == "" {
		if def > 0 {
			return def, nil
		}
		return 0, fmt.Errorf("%s is required", name)
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("%s %q: %v", name, s, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s must be positive", name)
	}
	return d, nil
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
