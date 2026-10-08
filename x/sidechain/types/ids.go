package types

import (
	"regexp"

	errorsmod "cosmossdk.io/errors"
)

// MaxIDBytes bounds sidechain identifiers.
const MaxIDBytes = 64

// idPattern restricts sidechain ids to a safe charset. It excludes "\x00" and
// "/", which would be ambiguous inside collections pair keys (x/checkpoint keys
// its checkpoints by (sidechain_id, sequence)) and in REST paths.
var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// ValidateID validates a sidechain id.
func ValidateID(id string) error {
	switch {
	case id == "":
		return errorsmod.Wrap(ErrInvalidRequest, "id is required")
	case len(id) > MaxIDBytes:
		return errorsmod.Wrapf(ErrLimitExceeded, "id is %d bytes, max %d", len(id), MaxIDBytes)
	case !idPattern.MatchString(id):
		return errorsmod.Wrapf(ErrInvalidRequest, "id %q must match [a-z0-9][a-z0-9._-]*", id)
	}
	return nil
}
