package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/cosmos/cosmos-sdk/types/query"

	"github.com/lucidpay/lucidchain/x/checkpoint/types"
)

var _ types.QueryServer = queryServer{}

// NewQueryServerImpl returns an implementation of the QueryServer interface
// for the provided Keeper.
func NewQueryServerImpl(k Keeper) types.QueryServer {
	return queryServer{k}
}

type queryServer struct {
	k Keeper
}

// Checkpoint returns the checkpoint at (sidechain_id, sequence).
func (q queryServer) Checkpoint(ctx context.Context, req *types.QueryCheckpointRequest) (*types.QueryCheckpointResponse, error) {
	if req == nil || req.SidechainId == "" || req.Sequence == 0 {
		return nil, status.Error(codes.InvalidArgument, "sidechain_id and a sequence >= 1 are required")
	}

	cp, err := q.k.GetCheckpoint(ctx, req.SidechainId, req.Sequence)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound,
				"checkpoint %d of sidechain %q not found", req.Sequence, req.SidechainId)
		}
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &types.QueryCheckpointResponse{Checkpoint: cp}, nil
}

// LatestCheckpoint returns the newest checkpoint of a sidechain.
func (q queryServer) LatestCheckpoint(ctx context.Context, req *types.QueryLatestCheckpointRequest) (*types.QueryCheckpointResponse, error) {
	if req == nil || req.SidechainId == "" {
		return nil, status.Error(codes.InvalidArgument, "sidechain_id is required")
	}

	cp, err := q.k.GetLatestCheckpoint(ctx, req.SidechainId)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound,
				"sidechain %q has no checkpoints", req.SidechainId)
		}
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &types.QueryCheckpointResponse{Checkpoint: cp}, nil
}

// Checkpoints lists the checkpoints of one sidechain, ordered by sequence.
// Set pagination.reverse to list newest first.
func (q queryServer) Checkpoints(ctx context.Context, req *types.QueryCheckpointsRequest) (*types.QueryCheckpointsResponse, error) {
	if req == nil || req.SidechainId == "" {
		return nil, status.Error(codes.InvalidArgument, "sidechain_id is required")
	}

	cps, pageRes, err := query.CollectionPaginate(
		ctx,
		q.k.Checkpoints,
		req.Pagination,
		func(_ collections.Pair[string, uint64], cp types.Checkpoint) (types.Checkpoint, error) {
			return cp, nil
		},
		query.WithCollectionPaginationPairPrefix[string, uint64](req.SidechainId),
	)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &types.QueryCheckpointsResponse{Checkpoints: cps, Pagination: pageRes}, nil
}
