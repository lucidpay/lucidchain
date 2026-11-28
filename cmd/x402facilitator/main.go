// Command x402facilitator runs the x402 facilitator HTTP service for the chain.
//
// It is a separate process from lucidchaind: it talks to a node over gRPC and
// never links into the chain binary. Run it next to a dedicated node.
//
//	x402facilitator -config x402-facilitator.json -bech32-prefix cosmos
//	x402facilitator -config x402-facilitator.json -check-config
//
// The chain-id is taken from the configured network ("cosmos:<chain-id>") and
// is what every payer signature is verified against.
//
// The binary refuses to start while adapters.Implemented is false (unless
// -allow-stubs is given); set it to true only after the adapter tests pass and
// you have tried real classic and PQ payments against your chain.
package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/lucidpay/lucidchain/x402/adapters"
	"github.com/lucidpay/lucidchain/x402/facilitator"
)

const (
	tokenEnv      = "X402_FACILITATOR_TOKEN"
	networkPrefix = "cosmos:" // CAIP-2 namespace
)

// standardModuleAccounts are well-known SDK module account names. Add
// chain-specific ones (ICA, key-rotation pool, ...) with -module-accounts. The
// adapter also checks the on-chain account type, so this list is a second layer.
var standardModuleAccounts = []string{
	"fee_collector", "distribution", "mint", "gov", "transfer",
	"bonded_tokens_pool", "not_bonded_tokens_pool",
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "x402facilitator:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, logOut io.Writer) error {
	fs := flag.NewFlagSet("x402facilitator", flag.ContinueOnError)
	fs.SetOutput(logOut)
	configPath := fs.String("config", "", "path to the JSON config file (required)")
	checkOnly := fs.Bool("check-config", false, "validate the config and exit")
	allowStubs := fs.Bool("allow-stubs", false, "start even though adapters.Implemented is false (wiring tests only)")
	bech32Prefix := fs.String("bech32-prefix", "", "account address bech32 prefix of the chain, e.g. cosmos (required to start)")
	extraModules := fs.String("module-accounts", "", "comma-separated extra module account names to refuse as payTo")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return errors.New("-config is required")
	}
	log := slog.New(slog.NewTextHandler(logOut, nil))

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		return err
	}
	token := os.Getenv(tokenEnv)
	res, warnings, err := cfg.Resolve(token)
	for _, w := range warnings {
		log.Warn(w)
	}
	if err != nil {
		return fmt.Errorf("invalid config:\n%w", err)
	}
	if *checkOnly {
		log.Info("config OK", "network", res.Network, "asset", res.Asset, "listen", res.ListenAddr)
		return nil
	}

	if *bech32Prefix == "" {
		return errors.New("-bech32-prefix is required")
	}
	chainID, ok := strings.CutPrefix(res.Network, networkPrefix)
	if !ok || chainID == "" {
		return fmt.Errorf("network %q must be CAIP-2 %q<chain-id>", res.Network, networkPrefix)
	}

	if !adapters.Implemented {
		if !*allowStubs {
			return errors.New("adapters.Implemented=false: run the adapter tests, try real payments on your chain, then set it to true (or use -allow-stubs for wiring tests only)")
		}
		log.Warn("adapters.Implemented=false: adapters are not yet validated against real transactions")
	}
	log.Warn("using in-memory payment store: settlement claims are lost on restart; use a durable store in production")

	modules := append(append([]string(nil), standardModuleAccounts...), splitList(*extraModules)...)
	set, err := newAdapters(res, *bech32Prefix, chainID, modules)
	if err != nil {
		return err
	}
	defer set.Close()

	srv := &http.Server{
		Addr:              res.ListenAddr,
		Handler:           buildHandler(res, token, set),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		// Settle waits for block confirmation, so the write timeout must cover it.
		WriteTimeout: res.ConfirmTimeout + 10*time.Second,
		IdleTimeout:  60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", res.ListenAddr, "network", res.Network)
		if res.TLSCertFile != "" {
			errCh <- srv.ListenAndServeTLS(res.TLSCertFile, res.TLSKeyFile)
		} else {
			errCh <- srv.ListenAndServe()
		}
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), res.ConfirmTimeout+10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// adapterSet holds the SDK/node adapters and their gRPC connections.
type adapterSet struct {
	parser      *adapters.SDKTxParser
	verifier    *adapters.SDKSignatureVerifier
	chain       *adapters.GRPCChain
	broadcaster *adapters.NodeBroadcaster
}

func newAdapters(res Resolved, bech32Prefix, chainID string, moduleNames []string) (*adapterSet, error) {
	enc, err := newEncoding(bech32Prefix)
	if err != nil {
		return nil, err
	}
	s := &adapterSet{parser: adapters.NewSDKTxParser(enc.TxConfig, enc.AddrCodec)}
	if s.verifier, err = adapters.NewSDKSignatureVerifier(enc.TxConfig, enc.Registry, enc.AddrCodec, res.GRPCAddr, chainID); err != nil {
		return nil, fmt.Errorf("signature verifier: %w", err)
	}
	if s.chain, err = adapters.NewGRPCChain(res.GRPCAddr, enc.Registry, enc.AddrCodec, moduleNames); err != nil {
		s.Close()
		return nil, fmt.Errorf("chain reader: %w", err)
	}
	if s.broadcaster, err = adapters.NewNodeBroadcaster(res.GRPCAddr); err != nil {
		s.Close()
		return nil, fmt.Errorf("broadcaster: %w", err)
	}
	return s, nil
}

// Close releases every gRPC connection that was opened.
func (s *adapterSet) Close() {
	if s.verifier != nil {
		_ = s.verifier.Close()
	}
	if s.chain != nil {
		_ = s.chain.Close()
	}
	if s.broadcaster != nil {
		_ = s.broadcaster.Close()
	}
}

// buildHandler wires the adapters into the facilitator and adds /healthz and
// bearer-token auth in front of /verify and /settle.
func buildHandler(res Resolved, token string, set *adapterSet) http.Handler {
	verifier := &facilitator.Verifier{
		Policy: facilitator.Policy{
			Network:         res.Network,
			Asset:           res.Asset,
			AllowedKeyTypes: res.AllowedKeyTypes,
			MinRemaining:    res.MinRemaining,
			MaxWindow:       res.MaxWindow,
			MemoPrefix:      res.MemoPrefix,
			MinFeeBalance:   res.MinFeeReserve,
			// SponsorAddr stays empty: sponsored fee mode is not implemented.
		},
		Parser: set.parser,
		Sig:    set.verifier,
		Chain:  set.chain,
	}
	settler := &facilitator.Settler{
		Verifier:       verifier,
		Broadcaster:    set.broadcaster,
		Store:          facilitator.NewMemoryStore(), // TODO: durable store (SQL/Redis)
		PollInterval:   res.PollInterval,
		ConfirmTimeout: res.ConfirmTimeout,
	}
	api := (&facilitator.Server{Verifier: verifier, Settler: settler}).Handler()

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/", requireToken(token, api))
	return mux
}

// requireToken protects every route except GET /supported when a token is set.
func requireToken(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/supported" {
			h := r.Header.Get("Authorization")
			got, ok := strings.CutPrefix(h, "Bearer ")
			if !ok || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
				w.Header().Set("WWW-Authenticate", "Bearer")
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
