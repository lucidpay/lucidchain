package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/cosmos/cosmos-sdk/types/query"

	"github.com/lucidpay/lucidchain/x/attestor/types"
)

var _ types.QueryServer = queryServer{}

// NewQueryServerImpl returns an implementation of the QueryServer interface.
func NewQueryServerImpl(k Keeper) types.QueryServer {
	return queryServer{k}
}

type queryServer struct {
	k Keeper
}

// toStatusErr maps module errors to gRPC status codes.
func toStatusErr(err error, notFound error) error {
	if errors.Is(err, notFound) {
		return status.Error(codes.NotFound, err.Error())
	}
	return status.Error(codes.Internal, err.Error())
}

// Attestor returns one attestor.
func (q queryServer) Attestor(ctx context.Context, req *types.QueryAttestorRequest) (*types.QueryAttestorResponse, error) {
	if req == nil || req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	a, err := q.k.GetAttestor(ctx, req.Id)
	if err != nil {
		return nil, toStatusErr(err, types.ErrAttestorNotFound)
	}
	return &types.QueryAttestorResponse{Attestor: a}, nil
}

// Attestors lists attestors. Filters: status (UNSPECIFIED = any) and domain
// (attestors authorized for at least one schema in that domain).
func (q queryServer) Attestors(ctx context.Context, req *types.QueryAttestorsRequest) (*types.QueryAttestorsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}

	attestors, pageRes, err := query.CollectionFilteredPaginate(
		ctx,
		q.k.Attestors,
		req.Pagination,
		func(_ string, a types.Attestor) (bool, error) {
			if req.Status != types.AttestorStatus_ATTESTOR_STATUS_UNSPECIFIED && a.Status != req.Status {
				return false, nil
			}
			if req.Domain == "" {
				return true, nil
			}
			for _, sid := range a.AuthorizedSchemaIds {
				s, err := q.k.Schemas.Get(ctx, sid)
				if err != nil {
					if errors.Is(err, collections.ErrNotFound) {
						continue
					}
					return false, err
				}
				if s.Domain == req.Domain {
					return true, nil
				}
			}
			return false, nil
		},
		func(_ string, a types.Attestor) (types.Attestor, error) {
			return a, nil
		},
	)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryAttestorsResponse{Attestors: attestors, Pagination: pageRes}, nil
}

// Schema returns one schema.
func (q queryServer) Schema(ctx context.Context, req *types.QuerySchemaRequest) (*types.QuerySchemaResponse, error) {
	if req == nil || req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	s, err := q.k.GetSchema(ctx, req.Id)
	if err != nil {
		return nil, toStatusErr(err, types.ErrSchemaNotFound)
	}
	return &types.QuerySchemaResponse{Schema: s}, nil
}

// Schemas lists schemas, optionally filtered by domain.
func (q queryServer) Schemas(ctx context.Context, req *types.QuerySchemasRequest) (*types.QuerySchemasResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}

	schemas, pageRes, err := query.CollectionFilteredPaginate(
		ctx,
		q.k.Schemas,
		req.Pagination,
		func(_ string, s types.AttestationSchema) (bool, error) {
			return req.Domain == "" || s.Domain == req.Domain, nil
		},
		func(_ string, s types.AttestationSchema) (types.AttestationSchema, error) {
			return s, nil
		},
	)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QuerySchemasResponse{Schemas: schemas, Pagination: pageRes}, nil
}

// Attestation returns one attestation.
func (q queryServer) Attestation(ctx context.Context, req *types.QueryAttestationRequest) (*types.QueryAttestationResponse, error) {
	if req == nil || req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	a, err := q.k.GetAttestation(ctx, req.Id)
	if err != nil {
		return nil, toStatusErr(err, types.ErrAttestationNotFound)
	}
	return &types.QueryAttestationResponse{Attestation: a}, nil
}

// AttestationsForCheckpoint lists the attestations of one checkpoint, ordered
// by attestation id.
func (q queryServer) AttestationsForCheckpoint(ctx context.Context, req *types.QueryAttestationsForCheckpointRequest) (*types.QueryAttestationsForCheckpointResponse, error) {
	if req == nil || req.SidechainId == "" || req.CheckpointSequence == 0 {
		return nil, status.Error(codes.InvalidArgument, "sidechain_id and a checkpoint_sequence >= 1 are required")
	}

	atts, pageRes, err := query.CollectionPaginate(
		ctx,
		q.k.AttestationsByCheckpoint,
		req.Pagination,
		func(_ collections.Pair[collections.Pair[string, uint64], string], attestationID string) (types.Attestation, error) {
			return q.k.GetAttestation(ctx, attestationID)
		},
		query.WithCollectionPaginationPairPrefix[collections.Pair[string, uint64], string](
			collections.Join(req.SidechainId, req.CheckpointSequence),
		),
	)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryAttestationsForCheckpointResponse{Attestations: atts, Pagination: pageRes}, nil
}

// Dispute returns one dispute.
func (q queryServer) Dispute(ctx context.Context, req *types.QueryDisputeRequest) (*types.QueryDisputeResponse, error) {
	if req == nil || req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	d, err := q.k.GetDispute(ctx, req.Id)
	if err != nil {
		return nil, toStatusErr(err, types.ErrDisputeNotFound)
	}
	return &types.QueryDisputeResponse{Dispute: d}, nil
}
