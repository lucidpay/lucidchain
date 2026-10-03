package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/cosmos/cosmos-sdk/types/query"
	"github.com/lucidpay/lucidchain/x/sidechain/types"
)

func (q queryServer) Params(ctx context.Context, req *types.QueryParamsRequest) (*types.QueryParamsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}

	params, err := q.k.Params.Get(ctx)
	if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return nil, status.Error(codes.Internal, "internal error")
	}

	return &types.QueryParamsResponse{Params: params}, nil
}

func (q queryServer) Sidechain(ctx context.Context, req *types.QuerySidechainRequest) (*types.QuerySidechainResponse, error) {
	if req == nil || req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "sidechain id is required")
	}

	sc, err := q.k.Sidechains.Get(ctx, req.Id)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "sidechain %q not found", req.Id)
		}
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &types.QuerySidechainResponse{Sidechain: sc}, nil
}

func (q queryServer) Sidechains(ctx context.Context, req *types.QuerySidechainsRequest) (*types.QuerySidechainsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}

	scs, pageRes, err := query.CollectionFilteredPaginate(
		ctx,
		q.k.Sidechains,
		req.Pagination,
		func(_ string, sc types.Sidechain) (bool, error) {
			// UNSPECIFIED (0) means "no filter"
			if req.Tier != types.AssuranceTier_ASSURANCE_TIER_UNSPECIFIED && sc.Tier != req.Tier {
				return false, nil
			}
			if req.Status != types.SidechainStatus_SIDECHAIN_STATUS_UNSPECIFIED && sc.Status != req.Status {
				return false, nil
			}
			return true, nil
		},
		func(_ string, sc types.Sidechain) (types.Sidechain, error) {
			return sc, nil
		},
	)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &types.QuerySidechainsResponse{Sidechains: scs, Pagination: pageRes}, nil
}
