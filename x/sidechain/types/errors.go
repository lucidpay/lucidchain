package types

// DONTCOVER

import (
	errorsmod "cosmossdk.io/errors"
)

// x/sidechain module sentinel errors
var (
	ErrInvalidSigner         = errorsmod.Register(ModuleName, 1100, "expected gov account as only signer for proposal message")
	ErrSidechainNotFound     = errorsmod.Register(ModuleName, 2, "sidechain not found")
	ErrSidechainExists       = errorsmod.Register(ModuleName, 3, "sidechain already exists")
	ErrInvalidSidechainState = errorsmod.Register(ModuleName, 4, "operation not allowed in the sidechain's current status")
	ErrInvalidSigners        = errorsmod.Register(ModuleName, 5, "invalid signer set")
	ErrInsufficientBond      = errorsmod.Register(ModuleName, 6, "insufficient bond")
	ErrTierNotAvailable      = errorsmod.Register(ModuleName, 7, "no minimum bond is configured for this assurance tier")
	ErrUnauthorized          = errorsmod.Register(ModuleName, 8, "unauthorized")
	ErrInvalidRequest        = errorsmod.Register(ModuleName, 9, "invalid request")
	ErrLimitExceeded         = errorsmod.Register(ModuleName, 10, "module limit exceeded")
	ErrInvalidGenesis        = errorsmod.Register(ModuleName, 11, "invalid genesis state")
)
