package checkpoint

import (
	autocliv1 "cosmossdk.io/api/cosmos/autocli/v1"

	"github.com/lucidpay/lucidchain/x/checkpoint/types"
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
					RpcMethod: "Checkpoint",
					Use:       "checkpoint [sidechain-id] [lc-sequence]",
					Short:     "Shows the checkpoint at a given sequence",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{
						{ProtoField: "sidechain_id"},
						{ProtoField: "lc_sequence"},
					},
				},
				{
					RpcMethod: "LatestCheckpoint",
					Use:       "latest-checkpoint [sidechain-id]",
					Short:     "Shows the newest checkpoint of a sidechain",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{
						{ProtoField: "sidechain_id"},
					},
				},
				{
					RpcMethod: "Checkpoints",
					Use:       "checkpoints [sidechain-id]",
					Short:     "Lists the checkpoints of a sidechain",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{
						{ProtoField: "sidechain_id"},
					},
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
				{
					RpcMethod: "SubmitCheckpoint",
					Use:       "submit-checkpoint [sidechain-id] [lc-sequence] [state-root] [previous-checkpoint-hash] [record-count] [data-pointer] [signer-set-version]",
					Short:     "Submit a signed sidechain checkpoint",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{
						{ProtoField: "sidechain_id"},
						{ProtoField: "lc_sequence"},
						{ProtoField: "state_root"},
						{ProtoField: "previous_checkpoint_hash"},
						{ProtoField: "record_count"},
						{ProtoField: "data_pointer"},
						{ProtoField: "signer_set_version"},
					},
				},
			},
		},
	}
}
