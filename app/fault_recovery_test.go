package app

import (
	"errors"
	"testing"

	"github.com/lucidpay/lucidchain/x/proofs/types"
)

func TestFaultRecoveryHandler(t *testing.T) {
	var halted bool
	h := FaultRecoveryHandler(func(types.VerifierFault) { halted = true })

	if err := h("some other panic"); err != nil || halted {
		t.Fatalf("non-fault panic must pass through: err=%v halted=%v", err, halted)
	}
	f := types.VerifierFault{Op: "verification", Err: errors.New("boom")}
	if err := h(f); err == nil || !halted {
		t.Fatalf("fault must halt: err=%v halted=%v", err, halted)
	}
}
