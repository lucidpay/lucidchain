package types

// DONTCOVER

import (
	errorsmod "cosmossdk.io/errors"
)

// x/checkpoint module sentinel errors
var (
	ErrInvalidSigner       = errorsmod.Register(ModuleName, 1100, "expected gov account as only signer for proposal message")
	ErrSidechainNotFound   = errorsmod.Register(ModuleName, 2, "sidechain not found")
	ErrSidechainNotActive  = errorsmod.Register(ModuleName, 3, "sidechain is not active")
	ErrInvalidCheckpoint   = errorsmod.Register(ModuleName, 4, "invalid checkpoint")
	ErrInvalidSequence     = errorsmod.Register(ModuleName, 5, "invalid checkpoint lc_sequence")
	ErrInvalidPreviousHash = errorsmod.Register(ModuleName, 6, "invalid previous checkpoint hash")
	ErrSignerSetMismatch   = errorsmod.Register(ModuleName, 7, "signer set version mismatch")
	ErrInvalidSignature    = errorsmod.Register(ModuleName, 8, "invalid checkpoint signature")
	ErrThresholdNotMet     = errorsmod.Register(ModuleName, 9, "signature threshold not met")
	ErrLimitExceeded       = errorsmod.Register(ModuleName, 10, "module limit exceeded")
	ErrInvalidGenesis      = errorsmod.Register(ModuleName, 11, "invalid genesis state")
)
