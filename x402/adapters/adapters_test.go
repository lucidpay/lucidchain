package adapters

import (
	"context"
	"errors"
	"testing"
)

// The signature verifier stub must never return nil: a nil would mean "valid".
func TestStubsFailClosed(t *testing.T) {
	ctx := context.Background()
	if err := NewSDKSignatureVerifier().Verify(ctx, nil); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("signature verifier stub must fail, got %v", err)
	}
	if _, err := NewSDKTxParser().Parse([]byte("x")); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("parser stub must fail, got %v", err)
	}
	c := NewGRPCChain("localhost:9090")
	if _, err := c.Balance(ctx, "a", "d"); !errors.Is(err, ErrNotImplemented) {
		t.Fatal("balance stub must fail")
	}
	if ok, err := c.IsModuleAccount(ctx, "a"); ok || !errors.Is(err, ErrNotImplemented) {
		t.Fatal("module-account stub must fail closed")
	}
	if _, err := c.Simulate(ctx, []byte("x")); !errors.Is(err, ErrNotImplemented) {
		t.Fatal("simulate stub must fail")
	}
	b := NewNodeBroadcaster("localhost:9090")
	if _, err := b.Broadcast(ctx, []byte("x")); !errors.Is(err, ErrNotImplemented) {
		t.Fatal("broadcast stub must fail")
	}
	if found, _, _, err := b.TxResult(ctx, "h"); found || !errors.Is(err, ErrNotImplemented) {
		t.Fatal("tx result stub must fail")
	}
}

// Guard against flipping the flag while stubs remain.
func TestImplementedFlagIsHonest(t *testing.T) {
	if Implemented {
		t.Skip("Implemented=true: replace the fail-closed stub tests with real adapter tests")
	}
}
