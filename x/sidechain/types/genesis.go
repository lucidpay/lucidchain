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

// Validate performs stateless genesis validation. Signer key types cannot be
// resolved without an interface registry, and operator addresses need an
// address codec, so those are checked in InitGenesis / ValidateGenesis.
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

		if err := validateSidechainGenesis(sc); err != nil {
			return err
		}
	}
	return nil
}

func validateSidechainGenesis(sc Sidechain) error {
	if sc.Operator == "" {
		return genesisErr("sidechain %q: operator is required", sc.Id)
	}
	if err := ValidateText("name", sc.Name, MaxNameBytes, true); err != nil {
		return genesisErr("sidechain %q: %s", sc.Id, err)
	}
	if err := ValidateURI("metadata_uri", sc.MetadataUri, MaxURIBytes); err != nil {
		return genesisErr("sidechain %q: %s", sc.Id, err)
	}
	if !ValidTier(sc.Tier) {
		return genesisErr("sidechain %q: invalid tier", sc.Id)
	}
	if sc.Status == SidechainStatus_SIDECHAIN_STATUS_UNSPECIFIED {
		return genesisErr("sidechain %q: status is required", sc.Id)
	}
	if _, ok := SidechainStatus_name[int32(sc.Status)]; !ok {
		return genesisErr("sidechain %q: unknown status %d", sc.Id, int32(sc.Status))
	}

	// signer set: structural checks only
	n := len(sc.SignerKeys)
	if n == 0 || uint32(n) > MaxSignerKeysCap {
		return genesisErr("sidechain %q: signer key count %d must be in [1, %d]", sc.Id, n, MaxSignerKeysCap)
	}
	if sc.SignatureThreshold == 0 || int(sc.SignatureThreshold) > n {
		return genesisErr("sidechain %q: need 1 <= threshold <= keys", sc.Id)
	}
	if sc.SignerSetVersion == 0 {
		return genesisErr("sidechain %q: signer_set_version must be >= 1", sc.Id)
	}
	keys := make(map[string]struct{}, n)
	for i, a := range sc.SignerKeys {
		if a == nil || a.TypeUrl == "" || len(a.Value) == 0 {
			return genesisErr("sidechain %q: signer key %d is empty", sc.Id, i)
		}
		raw := a.TypeUrl + "\x00" + string(a.Value)
		if _, dup := keys[raw]; dup {
			return genesisErr("sidechain %q: signer key %d is a duplicate", sc.Id, i)
		}
		keys[raw] = struct{}{}
	}

	if err := sc.Bond.Validate(); err != nil {
		return genesisErr("sidechain %q: bond: %s", sc.Id, err)
	}
	if err := ValidateCheckpointInterval(sc.CheckpointIntervalSeconds); err != nil {
		return genesisErr("sidechain %q: %s", sc.Id, err)
	}
	if sc.RegisteredAt.IsZero() {
		return genesisErr("sidechain %q: registered_at is required", sc.Id)
	}

	// activation
	switch sc.Status {
	case SidechainStatus_SIDECHAIN_STATUS_ACTIVE, SidechainStatus_SIDECHAIN_STATUS_SUSPENDED:
		if sc.ActivatedAt == nil {
			return genesisErr("sidechain %q: %s sidechains need activated_at", sc.Id, sc.Status)
		}
	case SidechainStatus_SIDECHAIN_STATUS_PENDING:
		if sc.ActivatedAt != nil {
			return genesisErr("sidechain %q: PENDING sidechains cannot have activated_at", sc.Id)
		}
	}

	// exit
	if sc.Status == SidechainStatus_SIDECHAIN_STATUS_EXITED {
		if sc.ExitRequestedAt == nil || sc.BondReturnAt == nil {
			return genesisErr("sidechain %q: EXITED sidechains need exit_requested_at and bond_return_at", sc.Id)
		}
		if sc.BondReturnAt.Before(*sc.ExitRequestedAt) {
			return genesisErr("sidechain %q: bond_return_at is before exit_requested_at", sc.Id)
		}
	} else if sc.ExitRequestedAt != nil || sc.BondReturnAt != nil {
		return genesisErr("sidechain %q: only EXITED sidechains can have exit timestamps", sc.Id)
	}

	// slashing
	if (sc.SlashedAt != nil) != (sc.SlashReason != "") {
		return genesisErr("sidechain %q: slashed_at and slash_reason must be set together", sc.Id)
	}
	if sc.Status == SidechainStatus_SIDECHAIN_STATUS_SLASHED && sc.SlashedAt == nil {
		return genesisErr("sidechain %q: SLASHED sidechains need slashed_at and slash_reason", sc.Id)
	}
	if sc.SlashReason != "" {
		if err := ValidateText("slash_reason", sc.SlashReason, MaxReasonBytes, false); err != nil {
			return genesisErr("sidechain %q: %s", sc.Id, err)
		}
	}

	// checkpoints
	hasCheckpoint := sc.LastCheckpointSequence > 0
	if hasCheckpoint != (len(sc.LastCheckpointHash) > 0) {
		return genesisErr("sidechain %q: last_checkpoint_hash must be set exactly when last_checkpoint_sequence > 0", sc.Id)
	}
	if hasCheckpoint != (sc.LastCheckpointAt != nil) {
		return genesisErr("sidechain %q: last_checkpoint_at must be set exactly when last_checkpoint_sequence > 0", sc.Id)
	}
	if sc.LastCheckpointHeight < 0 || (!hasCheckpoint && sc.LastCheckpointHeight != 0) {
		return genesisErr("sidechain %q: invalid last_checkpoint_height %d", sc.Id, sc.LastCheckpointHeight)
	}
	if hasCheckpoint && sc.Status == SidechainStatus_SIDECHAIN_STATUS_PENDING {
		return genesisErr("sidechain %q: PENDING sidechains cannot have checkpoints", sc.Id)
	}
	return nil
}
