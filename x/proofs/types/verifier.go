package types

// ProofVerifier is the contract between x/proofs and a concrete proof system.
// Implementations are compiled into the binary and handed to the keeper in a
// map keyed by proof_system_id; governance can only register ids that have an
// implementation.
//
// CONSENSUS WARNING: all methods run inside transaction execution on every
// validator, so they must be deterministic (same inputs, same result, on every
// platform), must not read the clock, network or filesystem, and must not
// depend on map iteration order or goroutine scheduling for their result. The
// keeper recovers panics, but a verifier that panics on some machines and not
// others would still split consensus.
type ProofVerifier interface {
	// ValidateKey checks verification key material when a verifier is
	// registered or rotated. It should fully parse the key and reject trailing
	// bytes and keys that cannot support the binding convention.
	ValidateKey(verificationKey []byte) error

	// CheckBinding checks that publicInputs commit to the given binding (by
	// convention: public inputs 0 and 1 are the binding's hi and lo limbs).
	// It runs before Verify and must be cheap.
	CheckBinding(publicInputs []byte, binding Binding) error

	// Verify returns nil if and only if proof is valid for verificationKey and
	// publicInputs. The keeper charges the registration's verification_gas
	// before calling it.
	Verify(verificationKey, publicInputs, proof []byte) error
}
