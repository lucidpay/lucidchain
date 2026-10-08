package types

import (
	errorsmod "cosmossdk.io/errors"
)

// DefaultGenesis returns the default genesis state.
func DefaultGenesis() *GenesisState {
	return &GenesisState{
		Params:     DefaultParams(),
		Sidechains: []Sidechain{},
	}
}

func genesisErr(format string, args ...any) error {
	return errorsmod.Wrapf(ErrInvalidGenesis, format, args...)
}

// Validate performs genesis validation. Signer key types cannot be resolved
// without an interface registry, so only counts are checked for the keys.
func (gs GenesisState) Validate() error {
	if err := gs.Params.Validate(); err != nil {
		return genesisErr("%s", err)
	}

	seen := make(map[string]struct{}, len(gs.Sidechains))
	for _, sc := range gs.Sidechains {
		if err := ValidateID(sc.Id); err != nil {
			return genesisErr("%s", err)
		}
		if _, dup := seen[sc.Id]; dup {
			return genesisErr("duplicate sidechain %q", sc.Id)
		}
		seen[sc.Id] = struct{}{}

		if sc.Operator == "" {
			return genesisErr("sidechain %q: operator is required", sc.Id)
		}
		if sc.Status == SidechainStatus_SIDECHAIN_STATUS_UNSPECIFIED {
			return genesisErr("sidechain %q: status is required", sc.Id)
		}
		if !ValidTier(sc.Tier) {
			return genesisErr("sidechain %q: invalid tier", sc.Id)
		}
		if len(sc.SignerKeys) == 0 || sc.SignatureThreshold == 0 || int(sc.SignatureThreshold) > len(sc.SignerKeys) {
			return genesisErr("sidechain %q: need at least one signer key and 1 <= threshold <= keys", sc.Id)
		}
		if sc.SignerSetVersion == 0 {
			return genesisErr("sidechain %q: signer_set_version must be >= 1", sc.Id)
		}
		if err := sc.Bond.Validate(); err != nil {
			return genesisErr("sidechain %q: bond: %s", sc.Id, err)
		}
		if err := ValidateCheckpointInterval(sc.CheckpointIntervalSeconds); err != nil {
			return genesisErr("sidechain %q: %s", sc.Id, err)
		}
		if (sc.LastCheckpointSequence == 0) != (len(sc.LastCheckpointHash) == 0) {
			return genesisErr("sidechain %q: last_checkpoint_hash must be set exactly when last_checkpoint_sequence > 0", sc.Id)
		}
		if sc.Status == SidechainStatus_SIDECHAIN_STATUS_EXITED && sc.BondReturnAt == nil {
			return genesisErr("sidechain %q: EXITED sidechains need bond_return_at", sc.Id)
		}
	}
	return nil
}
