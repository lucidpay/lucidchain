// Command x402facilitator runs the x402 facilitator HTTP service for the chain.
//
// It is a separate process from lucidchaind: it talks to a node over gRPC and
// never links into the chain binary. Run it next to a dedicated node.
//
//	x402facilitator -config x402-facilitator.json
//	x402facilitator -config x402-facilitator.json -check-config
//
// The adapters (x402/adapters) are stubs until you implement them; the binary
// refuses to start with stubs unless -allow-stubs is given, and even then every
// payment is rejected (the stubs fail closed).
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

const tokenEnv = "X402_FACILITATOR_TOKEN"

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
	allowStubs := fs.Bool("allow-stubs", false, "start even though adapters are stubs (every payment is rejected)")
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

	if !adapters.Implemented {
		if !*allowStubs {
			return errors.New("adapters are stubs (adapters.Implemented=false): implement them, or use -allow-stubs for wiring tests only")
		}
		log.Warn("running with STUB adapters: every verify and settle request will be rejected")
	}
	log.Warn("using in-memory payment store: settlement claims are lost on restart; use a durable store in production")

	srv := &http.Server{
		Addr:              res.ListenAddr,
		Handler:           buildHandler(res, token),
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

// buildHandler wires the adapters into the facilitator and adds /healthz and
// bearer-token auth in front of /verify and /settle.
func buildHandler(res Resolved, token string) http.Handler {
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
		Parser: adapters.NewSDKTxParser(),
		Sig:    adapters.NewSDKSignatureVerifier(),
		Chain:  adapters.NewGRPCChain(res.GRPCAddr),
	}
	settler := &facilitator.Settler{
		Verifier:       verifier,
		Broadcaster:    adapters.NewNodeBroadcaster(res.GRPCAddr),
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
