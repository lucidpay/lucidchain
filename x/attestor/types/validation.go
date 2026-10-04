package types

import (
	errorsmod "cosmossdk.io/errors"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
)

// Size limits that are not governance parameters.
const (
	MaxNameBytes         = 128
	MaxTitleBytes        = 128
	MaxTextBytes         = 2048
	MaxListEntries       = 16
	MaxListEntryBytes    = 256
	MaxAuthorizedSchemas = 32
)

// Validate performs stateless validation of a schema.
func (s AttestationSchema) Validate() error {
	if err := ValidateID("schema id", s.Id); err != nil {
		return err
	}
	if err := ValidateID("domain", s.Domain); err != nil {
		return err
	}
	if s.Title == "" || len(s.Title) > MaxTitleBytes {
		return errorsmod.Wrapf(ErrInvalidRequest, "title is required and at most %d bytes", MaxTitleBytes)
	}
	if s.ClaimDescription == "" || len(s.ClaimDescription) > MaxTextBytes {
		return errorsmod.Wrapf(ErrInvalidRequest, "claim_description is required and at most %d bytes", MaxTextBytes)
	}
	if s.Exclusions == "" || len(s.Exclusions) > MaxTextBytes {
		return errorsmod.Wrapf(ErrInvalidRequest, "exclusions is required and at most %d bytes", MaxTextBytes)
	}
	if len(s.RequiredDataSources) > MaxListEntries {
		return errorsmod.Wrapf(ErrLimitExceeded, "at most %d required_data_sources", MaxListEntries)
	}
	for _, src := range s.RequiredDataSources {
		if src == "" || len(src) > MaxListEntryBytes {
			return errorsmod.Wrapf(ErrInvalidRequest,
				"each required_data_sources entry must be non-empty and at most %d bytes", MaxListEntryBytes)
		}
	}
	if s.ValidityPeriodSeconds == 0 || s.ValidityPeriodSeconds > MaxPeriodSeconds {
		return errorsmod.Wrapf(ErrInvalidRequest, "validity_period_seconds must be in [1, %d]", MaxPeriodSeconds)
	}
	if s.DisputeWindowSeconds == 0 || s.DisputeWindowSeconds > MaxPeriodSeconds {
		return errorsmod.Wrapf(ErrInvalidRequest, "dispute_window_seconds must be in [1, %d]", MaxPeriodSeconds)
	}
	return nil
}

// ValidateSchemaIDList checks a non-empty, duplicate-free list of schema ids.
func ValidateSchemaIDList(field string, ids []string) error {
	if len(ids) == 0 {
		return errorsmod.Wrapf(ErrInvalidRequest, "%s must not be empty", field)
	}
	if len(ids) > MaxAuthorizedSchemas {
		return errorsmod.Wrapf(ErrLimitExceeded, "%s has %d entries, max %d", field, len(ids), MaxAuthorizedSchemas)
	}
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if err := ValidateID(field+" entry", id); err != nil {
			return err
		}
		if _, dup := seen[id]; dup {
			return errorsmod.Wrapf(ErrInvalidRequest, "%s contains duplicate %q", field, id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

// ValidateSignerSet checks an attestor's signer keys and threshold against the
// params. The keys must already be unpacked (tx decoding does this for
// messages that implement UnpackInterfaces).
func ValidateSignerSet(keys []*codectypes.Any, threshold uint32, p Params) error {
	n := len(keys)
	if n == 0 {
		return errorsmod.Wrap(ErrInvalidSigners, "at least one signer key is required")
	}
	if uint64(n) > uint64(p.MaxSignerKeys) {
		return errorsmod.Wrapf(ErrInvalidSigners, "%d signer keys, max %d", n, p.MaxSignerKeys)
	}
	if threshold == 0 || uint64(threshold) > uint64(n) {
		return errorsmod.Wrapf(ErrInvalidSigners,
			"signature_threshold %d must be in [1, %d]", threshold, n)
	}

	allowed := make(map[string]struct{}, len(p.AllowedPubkeyTypeUrls))
	for _, u := range p.AllowedPubkeyTypeUrls {
		allowed[u] = struct{}{}
	}

	seen := make(map[string]struct{}, n)
	for i, a := range keys {
		if a == nil {
			return errorsmod.Wrapf(ErrInvalidSigners, "signer key %d is nil", i)
		}
		if _, ok := allowed[a.TypeUrl]; !ok {
			return errorsmod.Wrapf(ErrInvalidSigners,
				"signer key %d has type %q which is not in allowed_pubkey_type_urls", i, a.TypeUrl)
		}
		pk, ok := a.GetCachedValue().(cryptotypes.PubKey)
		if !ok {
			return errorsmod.Wrapf(ErrInvalidSigners, "signer key %d is not an unpacked public key", i)
		}
		id := a.TypeUrl + "\x00" + string(pk.Bytes())
		if _, dup := seen[id]; dup {
			return errorsmod.Wrapf(ErrInvalidSigners, "signer key %d is a duplicate", i)
		}
		seen[id] = struct{}{}
	}
	return nil
}
