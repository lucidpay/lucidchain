package types

import (
	"net/url"
	"strings"
	"unicode"

	"unicode/utf8"

	errorsmod "cosmossdk.io/errors"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
)

// Size limits that are not governance parameters.
const (
	MaxNameBytes   = 128
	MaxReasonBytes = 256
	MaxURIBytes    = 512
)

// expectedKeyLen pins the raw public key size per allowed type. A key of the
// wrong size would be accepted by type URL alone and then fail every checkpoint
// verification, locking the bond. A type missing from this map is rejected, so
// adding a new key type through governance also needs a code change here.
var expectedKeyLen = map[string]int{
	"/cosmos.crypto.mldsa65.PubKey":   1952,
	"/cosmos.crypto.ed25519.PubKey":   32,
	"/cosmos.crypto.secp256k1.PubKey": 33,
}

var allowedURISchemes = map[string]struct{}{"https": {}, "ipfs": {}}

// ValidateText checks a free-text field against a byte limit.
func ValidateText(field, v string, maxBytes int, required bool) error {
	if required && v == "" {
		return errorsmod.Wrapf(ErrInvalidRequest, "%s is required", field)
	}
	if len(v) > maxBytes { // first, so the loop below is bounded
		return errorsmod.Wrapf(ErrLimitExceeded, "%s is %d bytes, max %d", field, len(v), maxBytes)
	}
	if !utf8.ValidString(v) {
		return errorsmod.Wrapf(ErrInvalidRequest, "%s must be valid UTF-8", field)
	}
	for _, r := range v {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return errorsmod.Wrapf(ErrInvalidRequest, "%s contains control or formatting characters", field)
		}
	}
	return nil
}

// ValidateURI validates an optional URI: valid text, parseable, allowed scheme.
func ValidateURI(field, v string, maxBytes int) error {
	if v == "" {
		return nil
	}
	if err := ValidateText(field, v, maxBytes, false); err != nil {
		return err
	}
	u, err := url.Parse(v)
	if err != nil {
		return errorsmod.Wrapf(ErrInvalidRequest, "%s is not a valid URI: %s", field, err)
	}
	if _, ok := allowedURISchemes[strings.ToLower(u.Scheme)]; !ok || u.Host == "" {
		return errorsmod.Wrapf(ErrInvalidRequest, "%s must be an https:// or ipfs:// URI", field)
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

		want, known := expectedKeyLen[a.TypeUrl]
		if !known {
			return errorsmod.Wrapf(ErrInvalidSigners,
				"signer key %d: no expected key size registered for type %q", i, a.TypeUrl)
		}
		if got := len(pk.Bytes()); got != want {
			return errorsmod.Wrapf(ErrInvalidSigners,
				"signer key %d has %d bytes, %s keys must be %d", i, got, a.TypeUrl, want)
		}

		id := a.TypeUrl + "\x00" + string(pk.Bytes())
		if _, dup := seen[id]; dup {
			return errorsmod.Wrapf(ErrInvalidSigners, "signer key %d is a duplicate", i)
		}
		seen[id] = struct{}{}
	}
	return nil
}
