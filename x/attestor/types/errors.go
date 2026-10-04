package types

// DONTCOVER

import (
	errorsmod "cosmossdk.io/errors"
)

// x/attestor module sentinel errors
var (
	ErrAttestorNotFound        = errorsmod.Register(ModuleName, 2, "attestor not found")
	ErrAttestorExists          = errorsmod.Register(ModuleName, 3, "attestor already exists")
	ErrAttestorNotActive       = errorsmod.Register(ModuleName, 4, "attestor is not active")
	ErrInvalidAttestorState    = errorsmod.Register(ModuleName, 5, "operation not allowed in the attestor's current status")
	ErrSchemaNotFound          = errorsmod.Register(ModuleName, 6, "attestation schema not found")
	ErrSchemaExists            = errorsmod.Register(ModuleName, 7, "attestation schema already exists")
	ErrSchemaInactive          = errorsmod.Register(ModuleName, 8, "attestation schema is not active")
	ErrSchemaNotAuthorized     = errorsmod.Register(ModuleName, 9, "attestor is not authorized for this schema")
	ErrAttestationNotFound     = errorsmod.Register(ModuleName, 10, "attestation not found")
	ErrAttestationExists       = errorsmod.Register(ModuleName, 11, "attestation already exists")
	ErrInvalidAttestationState = errorsmod.Register(ModuleName, 12, "operation not allowed in the attestation's current status")
	ErrCheckpointNotFound      = errorsmod.Register(ModuleName, 13, "checkpoint not found")
	ErrDisputeNotFound         = errorsmod.Register(ModuleName, 14, "dispute not found")
	ErrDisputeExists           = errorsmod.Register(ModuleName, 15, "dispute already exists")
	ErrDisputeResolved         = errorsmod.Register(ModuleName, 16, "dispute already resolved")
	ErrDisputeWindowClosed     = errorsmod.Register(ModuleName, 17, "dispute window has closed")
	ErrInvalidSignature        = errorsmod.Register(ModuleName, 18, "invalid attestation signature")
	ErrThresholdNotMet         = errorsmod.Register(ModuleName, 19, "signature threshold not met")
	ErrSignerSetMismatch       = errorsmod.Register(ModuleName, 20, "signer set version mismatch")
	ErrInvalidSigners          = errorsmod.Register(ModuleName, 21, "invalid signer set")
	ErrInsufficientBond        = errorsmod.Register(ModuleName, 22, "insufficient bond")
	ErrUnauthorized            = errorsmod.Register(ModuleName, 23, "unauthorized")
	ErrInvalidRequest          = errorsmod.Register(ModuleName, 24, "invalid request")
	ErrLimitExceeded           = errorsmod.Register(ModuleName, 25, "module limit exceeded")
	ErrInvalidGenesis          = errorsmod.Register(ModuleName, 26, "invalid genesis state")
	ErrInvalidSigner           = errorsmod.Register(ModuleName, 1100, "expected gov account as only signer for proposal message")
)
