package types

import (
	errorsmod "cosmossdk.io/errors"
)

// DefaultGenesis returns the default genesis state.
func DefaultGenesis() *GenesisState {
	return &GenesisState{
		Params:       DefaultParams(),
		Attestors:    []Attestor{},
		Schemas:      []AttestationSchema{},
		Attestations: []Attestation{},
		Disputes:     []Dispute{},
	}
}

func genesisErr(format string, args ...any) error {
	return errorsmod.Wrapf(ErrInvalidGenesis, format, args...)
}

// Validate performs genesis validation, including cross-references between
// attestors, schemas, attestations and disputes. Signer key types cannot be
// resolved without an interface registry, so only counts are checked here.
func (gs GenesisState) Validate() error {
	if err := gs.Params.Validate(); err != nil {
		return genesisErr("%s", err)
	}

	schemaIDs := make(map[string]struct{}, len(gs.Schemas))
	for _, s := range gs.Schemas {
		if err := s.Validate(); err != nil {
			return genesisErr("schema %q: %s", s.Id, err)
		}
		if _, dup := schemaIDs[s.Id]; dup {
			return genesisErr("duplicate schema %q", s.Id)
		}
		schemaIDs[s.Id] = struct{}{}
	}

	attestorIDs := make(map[string]struct{}, len(gs.Attestors))
	for _, a := range gs.Attestors {
		if err := ValidateID("attestor id", a.Id); err != nil {
			return genesisErr("%s", err)
		}
		if _, dup := attestorIDs[a.Id]; dup {
			return genesisErr("duplicate attestor %q", a.Id)
		}
		attestorIDs[a.Id] = struct{}{}

		if a.Operator == "" {
			return genesisErr("attestor %q: operator is required", a.Id)
		}
		if a.Status == AttestorStatus_ATTESTOR_STATUS_UNSPECIFIED {
			return genesisErr("attestor %q: status is required", a.Id)
		}
		if a.BondAmount.IsNil() || a.BondAmount.IsNegative() {
			return genesisErr("attestor %q: bond_amount must be non-negative", a.Id)
		}
		if len(a.SignerKeys) == 0 || a.SignatureThreshold == 0 || int(a.SignatureThreshold) > len(a.SignerKeys) {
			return genesisErr("attestor %q: need at least one signer key and 1 <= threshold <= keys", a.Id)
		}
		if a.SignerSetVersion == 0 {
			return genesisErr("attestor %q: signer_set_version must be >= 1", a.Id)
		}
		if err := ValidateSchemaIDList("authorized_schema_ids", a.AuthorizedSchemaIds); err != nil {
			return genesisErr("attestor %q: %s", a.Id, err)
		}
		for _, sid := range a.AuthorizedSchemaIds {
			if _, ok := schemaIDs[sid]; !ok {
				return genesisErr("attestor %q: unknown schema %q", a.Id, sid)
			}
		}
		if a.Status == AttestorStatus_ATTESTOR_STATUS_EXITED && a.BondReturnAt == nil {
			return genesisErr("attestor %q: EXITED attestors need bond_return_at", a.Id)
		}
	}

	attestations := make(map[string]Attestation, len(gs.Attestations))
	for _, at := range gs.Attestations {
		if want := AttestationID(at.SchemaId, at.SidechainId, at.CheckpointSequence, at.AttestorId); at.Id != want {
			return genesisErr("attestation id %q does not match its fields (want %q)", at.Id, want)
		}
		if _, dup := attestations[at.Id]; dup {
			return genesisErr("duplicate attestation %q", at.Id)
		}
		if _, ok := attestorIDs[at.AttestorId]; !ok {
			return genesisErr("attestation %q: unknown attestor %q", at.Id, at.AttestorId)
		}
		if _, ok := schemaIDs[at.SchemaId]; !ok {
			return genesisErr("attestation %q: unknown schema %q", at.Id, at.SchemaId)
		}
		if at.Status == AttestationStatus_ATTESTATION_STATUS_UNSPECIFIED {
			return genesisErr("attestation %q: status is required", at.Id)
		}
		attestations[at.Id] = at
	}

	disputeIDs := make(map[string]struct{}, len(gs.Disputes))
	openByAttestor := make(map[string]uint64)
	for _, d := range gs.Disputes {
		if want := DisputeID(d.AttestationId); d.Id != want {
			return genesisErr("dispute id %q does not match its attestation (want %q)", d.Id, want)
		}
		if _, dup := disputeIDs[d.Id]; dup {
			return genesisErr("duplicate dispute %q", d.Id)
		}
		disputeIDs[d.Id] = struct{}{}

		at, ok := attestations[d.AttestationId]
		if !ok {
			return genesisErr("dispute %q: unknown attestation %q", d.Id, d.AttestationId)
		}
		if d.Challenger == "" {
			return genesisErr("dispute %q: challenger is required", d.Id)
		}
		if d.BondAmount.IsNil() || d.BondAmount.IsNegative() {
			return genesisErr("dispute %q: bond_amount must be non-negative", d.Id)
		}
		if !d.Resolved {
			if at.Status != AttestationStatus_ATTESTATION_STATUS_DISPUTED {
				return genesisErr("dispute %q is open but attestation %q is %s", d.Id, at.Id, at.Status)
			}
			openByAttestor[at.AttestorId]++
		}
	}

	for _, a := range gs.Attestors {
		if a.OpenDisputes != openByAttestor[a.Id] {
			return genesisErr("attestor %q: open_disputes is %d but %d disputes are open",
				a.Id, a.OpenDisputes, openByAttestor[a.Id])
		}
	}
	return nil
}
