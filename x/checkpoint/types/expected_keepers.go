package types

import (
	"context"

	"cosmossdk.io/core/address"

	sdk "github.com/cosmos/cosmos-sdk/types"
	sidechaintypes "github.com/lucidpay/lucidchain/x/sidechain/types"
)

// AuthKeeper defines the expected interface for the Auth module.
type AuthKeeper interface {
	AddressCodec() address.Codec
	GetAccount(context.Context, sdk.AccAddress) sdk.AccountI // only used for simulation
	// Methods imported from account should be defined here
}

// BankKeeper defines the expected interface for the Bank module.
type BankKeeper interface {
	SpendableCoins(context.Context, sdk.AccAddress) sdk.Coins
	// Methods imported from bank should be defined here
	SendCoinsFromAccountToModule(ctx context.Context, senderAddr sdk.AccAddress, recipientModule string, amt sdk.Coins) error
}

// ParamSubspace defines the expected Subspace interface for parameters.
type ParamSubspace interface {
	Get(context.Context, []byte, interface{})
	Set(context.Context, []byte, interface{})
}

// SidechainKeeper is the subset of x/sidechain this module depends on.
type SidechainKeeper interface {
	// GetSidechain returns the registration record, with signer_keys unpacked.
	GetSidechain(ctx context.Context, id string) (sidechaintypes.Sidechain, error)

	// RecordCheckpoint updates the sidechain's last-checkpoint fields.
	RecordCheckpoint(ctx context.Context, id string, sequence uint64, height int64, hash []byte) error
}
