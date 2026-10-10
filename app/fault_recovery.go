package app

import (
	"os"

	"cosmossdk.io/log/v2"

	"github.com/cosmos/cosmos-sdk/baseapp"

	"github.com/lucidpay/lucidchain/x/proofs/types"
)

// HaltFunc stops the node. In production it never returns.
type HaltFunc func(f types.VerifierFault)

// HardHalt logs and terminates the process. It does not rely on a panic
// propagating, because optimistic execution runs blocks in a goroutine and
// recovered panics are common in the stack. The block being executed was not
// committed, so a restart re-executes it.
func HardHalt(logger log.Logger) HaltFunc {
	return func(f types.VerifierFault) {
		logger.Error("FATAL: verifier fault, halting node to avoid divergent state",
			"op", f.Op, "err", f.Err)
		os.Exit(1)
	}
}

// FaultRecoveryHandler halts the node on a verifier fault and leaves every
// other panic to the default handlers.
func FaultRecoveryHandler(halt HaltFunc) baseapp.RecoveryHandler {
	return func(recoveryObj interface{}) error {
		f, ok := recoveryObj.(types.VerifierFault)
		if !ok {
			return nil // not ours: pass to the next handler
		}
		halt(f)
		return f // only reached if halt returns (tests)
	}
}
