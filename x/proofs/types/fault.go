package types

// VerifierFault is the value the keeper panics with when a verifier reports
// an infrastructure failure (an error wrapping ErrVerifierFault). It is not a
// verdict on the proof: continuing would make this node's result differ from
// healthy validators, so the node must stop instead.
type VerifierFault struct {
	Op  string
	Err error
}

func (f VerifierFault) Error() string {
	return "verifier fault during " + f.Op + ": " + f.Err.Error()
}

func (f VerifierFault) Unwrap() error { return f.Err }
