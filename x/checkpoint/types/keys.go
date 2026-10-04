package types

import "cosmossdk.io/collections"

const (
	// ModuleName defines the module name
	ModuleName = "checkpoint"

	// StoreKey defines the primary module store key
	StoreKey = ModuleName

	// GovModuleName duplicates the gov module's name to avoid a dependency with x/gov.
	// It should be synced with the gov module's name if it is ever changed.
	// See: https://github.com/cosmos/cosmos-sdk/blob/v0.52.0-beta.2/x/gov/types/keys.go#L9
	GovModuleName = "gov"
)

// Collection prefixes. They must be unique within this module and stable
// after launch (changing one orphans existing state).
var (
	// ParamsKey is the prefix for module params.
	ParamsKey = collections.NewPrefix("p_checkpoint")

	// CheckpointKey is the prefix for checkpoints, keyed by (sidechain_id, sequence).
	CheckpointKey = collections.NewPrefix("checkpoint/value/")

	// LatestSequenceKey is the prefix for the latest accepted sequence per sidechain.
	LatestSequenceKey = collections.NewPrefix("checkpoint/latest/")
)
