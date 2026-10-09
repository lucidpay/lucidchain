package attestor

import (
	autocliv1 "cosmossdk.io/api/cosmos/autocli/v1"

	"github.com/lucidpay/lucidchain/x/attestor/types"
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
					RpcMethod:      "Attestor",
					Use:            "attestor [id]",
					Short:          "Show one attestor",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "id"}},
				},
				{RpcMethod: "Attestors", Use: "attestors", Short: "List attestors (--domain, --status)"},
				{
					RpcMethod:      "Schema",
					Use:            "schema [id]",
					Short:          "Show one schema",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "id"}},
				},
				{RpcMethod: "Schemas", Use: "schemas", Short: "List schemas (--domain)"},
				{
					RpcMethod:      "Attestation",
					Use:            "attestation [id]",
					Short:          "Show one attestation",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "id"}},
				},
				{
					RpcMethod: "AttestationsForCheckpoint",
					Use:       "checkpoint-attestations [sidechain-id] [checkpoint-sequence]",
					Short:     "List attestations for a checkpoint",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{
						{ProtoField: "sidechain_id"},
						{ProtoField: "checkpoint_sequence"},
					},
				},
				{
					RpcMethod:      "Dispute",
					Use:            "dispute [id]",
					Short:          "Show one dispute",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "id"}},
				},
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
				{RpcMethod: "SubmitAttestation", Skip: true}, // hand-written in client/cli
				{RpcMethod: "RaiseDispute",
					Use:   "raise-dispute [attestation-id] [evidence-uri] [bond-amount]",
					Short: "Challenge an active attestation within its dispute window",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{
						{ProtoField: "attestation_id"},
						{ProtoField: "evidence_uri"},
						{ProtoField: "bond_amount"},
					},
				},
			},
		},
	}
}
