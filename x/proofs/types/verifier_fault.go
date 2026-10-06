package types

import "errors"

// ErrVerifierFault marks a failure of the verification infrastructure rather
// than a verdict on the proof: the verifier process or microVM crashed, timed
// out, was unreachable, answered with something unintelligible, or reported an
// image hash other than the pinned one.
//
// CONSENSUS RULE: a ProofVerifier.Verify error that satisfies
// errors.Is(err, ErrVerifierFault) must NEVER be recorded as "proof invalid".
// The keeper must halt (panic out of FinalizeBlock) so the node retries, because
// recording it would let one validator's flaky or tampered verifier diverge
// from the rest. Every other Verify error is a deterministic rejection.
var ErrVerifierFault = errors.New("verifier fault")
