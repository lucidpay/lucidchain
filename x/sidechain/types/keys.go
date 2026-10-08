package types

import "cosmossdk.io/collections"

const (
	// ModuleName defines the module name
	ModuleName = "sidechain"

	// StoreKey defines the primary module store key
	StoreKey = ModuleName

	// GovModuleName duplicates the gov module's name to avoid a dependency with x/gov.
	// It should be synced with the gov module's name if it is ever changed.
	// See: https://github.com/cosmos/cosmos-sdk/blob/v0.52.0-beta.2/x/gov/types/keys.go#L9
	GovModuleName = "gov"
)

// ParamsKey is the prefix to retrieve all Params
var (
	// ParamsKey is the prefix to retrieve all Params
	ParamsKey = collections.NewPrefix("p_sidechain")

	// SidechainKey is the prefix for sidechain registration records, keyed by id.
	SidechainKey = collections.NewPrefix("sidechain/value/")

	// ExitQueueKey orders exited sidechains by (bond_return_at unix seconds, id).
	ExitQueueKey = collections.NewPrefix("sidechain/exit_queue/")
)
