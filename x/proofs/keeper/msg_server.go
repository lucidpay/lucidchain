package keeper

import (
	"context"

	"github.com/lucidpay/lucidchain/x/proofs/types"
)

type msgServer struct {
	Keeper
}

// NewMsgServerImpl returns an implementation of the MsgServer interface
// for the provided Keeper.
func NewMsgServerImpl(keeper Keeper) types.MsgServer {
	return &msgServer{Keeper: keeper}
}

var _ types.MsgServer = msgServer{}

// SubmitProof implements types.MsgServer.
func (ms msgServer) SubmitProof(ctx context.Context, msg *types.MsgSubmitProof) (*types.MsgSubmitProofResponse, error) {
	return ms.Keeper.SubmitProof(ctx, msg)
}

// RegisterVerifier implements types.MsgServer.
func (ms msgServer) RegisterVerifier(ctx context.Context, msg *types.MsgRegisterVerifier) (*types.MsgRegisterVerifierResponse, error) {
	return ms.Keeper.RegisterVerifier(ctx, msg)
}

// DeprecateVerifier implements types.MsgServer.
func (ms msgServer) DeprecateVerifier(ctx context.Context, msg *types.MsgDeprecateVerifier) (*types.MsgDeprecateVerifierResponse, error) {
	return ms.Keeper.DeprecateVerifier(ctx, msg)
}
