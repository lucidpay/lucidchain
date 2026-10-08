package keeper

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/cosmos/cosmos-sdk/types/query"
	"github.com/lucidpay/lucidchain/x/sidechain/types"
)

// Sidechain returns one sidechain.
func (q queryServer) Sidechain(ctx context.Context, req *types.QuerySidechainRequest) (*types.QuerySidechainResponse, error) {
	if req == nil || req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	sc, err := q.k.loadSidechain(ctx, req.Id)
	if err != nil {
		if errors.Is(err, types.ErrSidechainNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QuerySidechainResponse{Sidechain: sc}, nil
}

// Sidechains lists sidechains. Filters: tier and status (UNSPECIFIED = any).
// The filter runs while paginating, so its cost grows with the total number of
// sidechains, not the page size.
func (q queryServer) Sidechains(ctx context.Context, req *types.QuerySidechainsRequest) (*types.QuerySidechainsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}

	scs, pageRes, err := query.CollectionFilteredPaginate(
		ctx,
		q.k.Sidechains,
		req.Pagination,
		func(_ string, sc types.Sidechain) (bool, error) {
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

// Params returns the module params.
func (q queryServer) Params(ctx context.Context, req *types.QueryParamsRequest) (*types.QueryParamsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	params, err := q.k.paramsOrDefault(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryParamsResponse{Params: params}, nil
}
