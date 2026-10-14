package types

// ProofVerifier is a proof-system implementation compiled into the binary.
//
// Consensus rules for every method: the result (including which error class is
// returned) must be a pure, deterministic function of the arguments. No clock,
// randomness, network, process-local cache state or map ordering may influence
// it. Implementations should not panic; the keeper recovers panics anyway and
// maps them to a deterministic failure.
//
// Errors wrapping ErrVerifierFault mean the verifier infrastructure failed (not
// that the proof is bad) and are handled by the keeper as faults; every other
// error is a deterministic rejection of the input.
type ProofVerifier interface {
	// ValidateKey fully parses verification key material at registration or
	// rotation. It rejects trailing bytes and keys that cannot support the
	// binding convention.
	ValidateKey(verificationKey []byte) error

	// PublicInputCount returns the number of public inputs a witness for this
	// key has, as a prover counts them (the Groth16 constant-one wire is not
	// included). It parses the key fully, so call it at registration, not per
	// transaction.
	PublicInputCount(verificationKey []byte) (int, error)

	// BindingWidth returns how many leading public inputs hold the checkpoint
	// binding. Layout indexes must be at or above it.
	BindingWidth() int

	// CheckBinding checks that the leading public inputs commit to binding.
	// Cheap; runs before Verify.
	CheckBinding(publicInputs []byte, binding Binding) error

	// CheckInputs checks that each public input at expected[i].Index equals
	// expected[i].Value, a big-endian unsigned integer (any length, leading zeros
	// ignored, empty is zero). Cheap; runs before Verify.
	CheckInputs(publicInputs []byte, expected []ExpectedInput) error

	// Verify returns nil if and only if proof is valid for verificationKey and
	// publicInputs. It must not rely on CheckBinding or CheckInputs having run.
	// The keeper charges verification_gas before calling it.
	Verify(verificationKey, publicInputs, proof []byte) error
}

// ExpectedInput pins one public input to a chain-derived value.
type ExpectedInput struct {
	Index uint32 // 0-based position in the public witness
	Value []byte // big-endian unsigned integer
}
