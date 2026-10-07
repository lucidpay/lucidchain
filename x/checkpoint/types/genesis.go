package types

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"slices"
	"strings"

	errorsmod "cosmossdk.io/errors"
)

// DefaultGenesis returns the default genesis state.
func DefaultGenesis() *GenesisState {
	return &GenesisState{
		Params:      DefaultParams(),
		Checkpoints: []Checkpoint{},
	}
}

// Validate performs basic genesis validation. For every sidechain, the
// checkpoints must form a gapless chain 1..n with matching hash links.
func (gs GenesisState) Validate() error {
	if err := gs.Params.Validate(); err != nil {
		return errorsmod.Wrap(ErrInvalidGenesis, err.Error())
	}

	cps := slices.Clone(gs.Checkpoints)
	slices.SortFunc(cps, func(a, b Checkpoint) int {
		if c := strings.Compare(a.SidechainId, b.SidechainId); c != 0 {
			return c
		}
		return cmp.Compare(a.LcSequence, b.LcSequence)
	})

	for i, cp := range cps {
		if cp.SidechainId == "" {
			return errorsmod.Wrap(ErrInvalidGenesis, "checkpoint with empty sidechain_id")
		}
		if cp.LcSequence == 0 {
			return errorsmod.Wrapf(ErrInvalidGenesis, "sidechain %q: lc_sequence must be >= 1", cp.SidechainId)
		}
		if len(cp.CheckpointHash) != sha256.Size {
			return errorsmod.Wrapf(ErrInvalidGenesis,
				"sidechain %q seq %d: checkpoint_hash must be %d bytes", cp.SidechainId, cp.LcSequence, sha256.Size)
		}

		startsChain := i == 0 || cps[i-1].SidechainId != cp.SidechainId
		if startsChain {
			if cp.LcSequence != 1 {
				return errorsmod.Wrapf(ErrInvalidGenesis,
					"sidechain %q: first checkpoint must have lc_sequence 1, got %d", cp.SidechainId, cp.LcSequence)
			}
			if len(cp.PreviousCheckpointHash) != 0 {
				return errorsmod.Wrapf(ErrInvalidGenesis,
					"sidechain %q: lc_sequence 1 must have empty previous_checkpoint_hash", cp.SidechainId)
			}
			continue
		}

		prev := cps[i-1]
		if cp.LcSequence != prev.LcSequence+1 {
			return errorsmod.Wrapf(ErrInvalidGenesis,
				"sidechain %q: gap or duplicate between lc_sequence %d and %d", cp.SidechainId, prev.LcSequence, cp.LcSequence)
		}
		if !bytes.Equal(cp.PreviousCheckpointHash, prev.CheckpointHash) {
			return errorsmod.Wrapf(ErrInvalidGenesis,
				"sidechain %q seq %d: previous_checkpoint_hash does not match checkpoint %d",
				cp.SidechainId, cp.LcSequence, prev.LcSequence)
		}
	}
	return nil
}
