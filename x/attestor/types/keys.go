package types

import "cosmossdk.io/collections"

const (
	// ModuleName defines the module name
	ModuleName = "attestor"

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
	ParamsKey = collections.NewPrefix("p_attestor")

	AttestorKey    = collections.NewPrefix("attestor/value/")
	SchemaKey      = collections.NewPrefix("schema/value/")
	AttestationKey = collections.NewPrefix("attestation/value/")
	DisputeKey     = collections.NewPrefix("dispute/value/")

	// AttestationByCheckpointKey indexes attestations by (sidechain_id, lc_sequence, attestation_id).
	AttestationByCheckpointKey = collections.NewPrefix("attestation/by_checkpoint/")

	// ExitQueueKey orders exited attestors by (bond_return_at unix seconds, attestor_id).
	ExitQueueKey = collections.NewPrefix("attestor/exit_queue/")
)
