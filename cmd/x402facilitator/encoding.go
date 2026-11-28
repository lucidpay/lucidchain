package main

import (
	"fmt"

	"github.com/cosmos/gogoproto/proto"

	coreaddress "cosmossdk.io/core/address"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/codec/address"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/std"
	authtx "github.com/cosmos/cosmos-sdk/x/auth/tx"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/cosmos/cosmos-sdk/x/tx/signing"
)

// encoding is the minimum the SDK adapters need to decode and verify payment
// transactions. It is built from SDK packages only, so the facilitator never
// links the chain's app package (keeping it a separate process from lucidchaind).
type encoding struct {
	TxConfig  client.TxConfig
	Registry  codectypes.InterfaceRegistry
	AddrCodec coreaddress.Codec
}

// newEncoding builds the registry and TxConfig for a chain with the given
// account bech32 prefix (e.g. "cosmos").
//
// Only bank MsgSend is ever accepted as a payment, so only the SDK's standard
// interfaces (crypto key types including ML-DSA-65, auth accounts, bank) are
// registered.
func newEncoding(prefix string) (*encoding, error) {
	if prefix == "" {
		return nil, fmt.Errorf("bech32 prefix required")
	}
	reg, err := codectypes.NewInterfaceRegistryWithOptions(codectypes.InterfaceRegistryOptions{
		ProtoFiles: proto.HybridResolver,
		SigningOptions: signing.Options{
			AddressCodec:          address.NewBech32Codec(prefix),
			ValidatorAddressCodec: address.NewBech32Codec(prefix + "valoper"),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("interface registry: %w", err)
	}
	std.RegisterInterfaces(reg)
	authtypes.RegisterInterfaces(reg)
	banktypes.RegisterInterfaces(reg)

	txConfig, err := authtx.NewTxConfigWithOptions(codec.NewProtoCodec(reg), authtx.ConfigOptions{
		EnabledSignModes: authtx.DefaultSignModes,
	})
	if err != nil {
		return nil, fmt.Errorf("tx config: %w", err)
	}
	return &encoding{
		TxConfig:  txConfig,
		Registry:  reg,
		AddrCodec: txConfig.SigningContext().AddressCodec(),
	}, nil
}
