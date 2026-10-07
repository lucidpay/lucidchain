package types

import "cosmossdk.io/collections"

const (
	// ModuleName defines the module name
	ModuleName = "proofs"

	// StoreKey defines the primary module store key
	StoreKey = ModuleName

	// GovModuleName duplicates the gov module's name to avoid a dependency with x/gov.
	// It should be synced with the gov module's name if it is ever changed.
	// See: https://github.com/cosmos/cosmos-sdk/blob/v0.52.0-beta.2/x/gov/types/keys.go#L9
	GovModuleName = "gov"
)

// Collection prefixes. They must be unique within this module, none may be a
// byte-prefix of another, and they must stay stable after launch.
var (
	ParamsKey = collections.NewPrefix("p_proofs")

	VerifierKey = collections.NewPrefix("verifier/value/")
	ProofKey    = collections.NewPrefix("proof/value/")

	// ProofByCheckpointKey indexes proofs by ((sidechain_id, lc_sequence), proof_id).
	ProofByCheckpointKey = collections.NewPrefix("proof/by_checkpoint/")
)
