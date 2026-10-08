package sidechain

import (
	autocliv1 "cosmossdk.io/api/cosmos/autocli/v1"

	"github.com/lucidpay/lucidchain/x/sidechain/types"
)

// AutoCLIOptions implements the autocli.HasAutoCLIConfig interface.
func (am AppModule) AutoCLIOptions() *autocliv1.ModuleOptions {
	return &autocliv1.ModuleOptions{
		Query: &autocliv1.ServiceCommandDescriptor{
			Service: types.Query_serviceDesc.ServiceName,
			RpcCommandOptions: []*autocliv1.RpcCommandOptions{
				{
					RpcMethod: "Params",
					Use:       "params",
					Short:     "Shows the parameters of the module",
				},
				{
					RpcMethod:      "Sidechain",
					Use:            "sidechain [id]",
					Short:          "Show a sidechain",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "id"}}},
				{
					RpcMethod: "Sidechains",
					Use:       "sidechains",
					Short:     "List sidechains (filter with --tier, --status)"},
			},
		},
		Tx: &autocliv1.ServiceCommandDescriptor{
			Service:              types.Msg_serviceDesc.ServiceName,
			EnhanceCustomCommand: true, // only required if you want to use the custom command
			RpcCommandOptions: []*autocliv1.RpcCommandOptions{
				{
					RpcMethod: "UpdateParams",
					Skip:      true, // skipped because authority gated
				},
				{
					RpcMethod:      "RegisterSidechain",
					Use:            "register-sidechain [id] [name] [tier] [bond]",
					Short:          "Register a sidechain and escrow its bond",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "id"}, {ProtoField: "name"}, {ProtoField: "tier"}, {ProtoField: "bond"}}},
				{
					RpcMethod:      "UpdateSidechainSigners",
					Use:            "update-signers [id]",
					Short:          "Update the signers of a sidechain",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "id"}}},
				{
					RpcMethod:      "UpdateSidechain",
					Use:            "update-sidechain [id]",
					Short:          "Update a sidechain",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "id"}}},
				{
					RpcMethod:      "InitiateSidechainExit",
					Use:            "exit [id]",
					Short:          "Initiate the exit process for a sidechain",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "id"}}},
				// Governance-only messages are skipped; they go through proposals.
				{RpcMethod: "ActivateSidechain", Skip: true},
				{RpcMethod: "SuspendSidechain", Skip: true},
				{RpcMethod: "ResumeSidechain", Skip: true},
			},
		},
	}
}
