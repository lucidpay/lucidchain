package types

// DONTCOVER

import (
	errorsmod "cosmossdk.io/errors"
)

// x/proofs module sentinel errors
var (
	ErrVerifierNotFound    = errorsmod.Register(ModuleName, 2, "verifier not found")
	ErrVerifierDeprecated  = errorsmod.Register(ModuleName, 3, "verifier is deprecated")
	ErrVerifierUnavailable = errorsmod.Register(ModuleName, 4, "no verifier implementation compiled into this binary")
	ErrInvalidVerifierKey  = errorsmod.Register(ModuleName, 5, "invalid verification key")
	ErrCheckpointNotFound  = errorsmod.Register(ModuleName, 6, "checkpoint not found")
	ErrProofExists         = errorsmod.Register(ModuleName, 7, "proof already recorded")
	ErrProofNotFound       = errorsmod.Register(ModuleName, 8, "proof record not found")
	ErrInvalidProof        = errorsmod.Register(ModuleName, 9, "proof verification failed")
	ErrBindingMismatch     = errorsmod.Register(ModuleName, 10, "public inputs are not bound to this checkpoint and claim")
	ErrUnauthorized        = errorsmod.Register(ModuleName, 11, "unauthorized")
	ErrInvalidRequest      = errorsmod.Register(ModuleName, 12, "invalid request")
	ErrLimitExceeded       = errorsmod.Register(ModuleName, 13, "module limit exceeded")
	ErrInvalidGenesis      = errorsmod.Register(ModuleName, 14, "invalid genesis state")
	ErrInvalidSigner       = errorsmod.Register(ModuleName, 1100, "expected gov account as only signer for proposal message")
)
