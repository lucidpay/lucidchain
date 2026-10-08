package types

import (
	errorsmod "cosmossdk.io/errors"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
)

// Size limits that are not governance parameters.
const (
	MaxNameBytes = 128
	MaxURIBytes  = 512
)

// ValidateText checks a free-text field against a byte limit.
func ValidateText(field, v string, maxBytes int, required bool) error {
	if required && v == "" {
		return errorsmod.Wrapf(ErrInvalidRequest, "%s is required", field)
	}
	if len(v) > maxBytes {
		return errorsmod.Wrapf(ErrLimitExceeded, "%s is %d bytes, max %d", field, len(v), maxBytes)
	}
	return nil
}

// ValidateCheckpointInterval checks a checkpoint interval in seconds.
func ValidateCheckpointInterval(seconds uint64) error {
	if seconds == 0 || seconds > MaxPeriodSeconds {
		return errorsmod.Wrapf(ErrInvalidRequest, "checkpoint_interval_seconds must be in [1, %d]", MaxPeriodSeconds)
	}
	return nil
}

// ValidateSignerSet checks a sidechain's signer keys and threshold against the
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
