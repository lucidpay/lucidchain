package keeper

import (
	"context"

	"github.com/lucidpay/lucidchain/x/checkpoint/types"
)

type msgServer struct {
	Keeper
}

// NewMsgServerImpl returns an implementation of the MsgServer interface
// for the provided Keeper.
func NewMsgServerImpl(keeper Keeper) types.MsgServer {
	return &msgServer{Keeper: keeper}
}

// SubmitCheckpoint implements types.MsgServer.
func (ms msgServer) SubmitCheckpoint(ctx context.Context, msg *types.MsgSubmitCheckpoint) (*types.MsgSubmitCheckpointResponse, error) {
	return ms.Keeper.SubmitCheckpoint(ctx, msg)
}

var _ types.MsgServer = msgServer{}
