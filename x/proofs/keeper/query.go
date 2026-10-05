package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/cosmos/cosmos-sdk/types/query"

	"github.com/lucidpay/lucidchain/x/proofs/types"
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

// toStatusErr maps module errors to gRPC status codes.
func toStatusErr(err error, notFound error) error {
	if errors.Is(err, notFound) {
		return status.Error(codes.NotFound, err.Error())
	}
	return status.Error(codes.Internal, err.Error())
}

// Verifier returns one verifier registration.
func (q queryServer) Verifier(ctx context.Context, req *types.QueryVerifierRequest) (*types.QueryVerifierResponse, error) {
	if req == nil || req.ProofSystemId == "" {
		return nil, status.Error(codes.InvalidArgument, "proof_system_id is required")
	}
	v, err := q.k.GetVerifier(ctx, req.ProofSystemId)
	if err != nil {
		return nil, toStatusErr(err, types.ErrVerifierNotFound)
	}
	return &types.QueryVerifierResponse{Verifier: v}, nil
}

// Verifiers lists verifier registrations.
func (q queryServer) Verifiers(ctx context.Context, req *types.QueryVerifiersRequest) (*types.QueryVerifiersResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}

	vs, pageRes, err := query.CollectionPaginate(
		ctx,
		q.k.Verifiers,
		req.Pagination,
		func(_ string, v types.VerifierRegistration) (types.VerifierRegistration, error) {
			return v, nil
		},
	)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryVerifiersResponse{Verifiers: vs, Pagination: pageRes}, nil
}

// ProofRecord returns one proof record.
func (q queryServer) ProofRecord(ctx context.Context, req *types.QueryProofRecordRequest) (*types.QueryProofRecordResponse, error) {
	if req == nil || req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	p, err := q.k.GetProof(ctx, req.Id)
	if err != nil {
		return nil, toStatusErr(err, types.ErrProofNotFound)
	}
	return &types.QueryProofRecordResponse{Proof: p}, nil
}

// ProofsForCheckpoint lists the proofs of one checkpoint, ordered by proof id.
func (q queryServer) ProofsForCheckpoint(ctx context.Context, req *types.QueryProofsForCheckpointRequest) (*types.QueryProofsForCheckpointResponse, error) {
	if req == nil || req.SidechainId == "" || req.CheckpointSequence == 0 {
		return nil, status.Error(codes.InvalidArgument, "sidechain_id and a checkpoint_sequence >= 1 are required")
	}

	proofs, pageRes, err := query.CollectionPaginate(
		ctx,
		q.k.ProofsByCheckpoint,
		req.Pagination,
		func(_ collections.Pair[collections.Pair[string, uint64], string], proofID string) (types.ProofRecord, error) {
			return q.k.GetProof(ctx, proofID)
		},
		query.WithCollectionPaginationPairPrefix[collections.Pair[string, uint64], string](
			collections.Join(req.SidechainId, req.CheckpointSequence),
		),
	)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryProofsForCheckpointResponse{Proofs: proofs, Pagination: pageRes}, nil
}

// Binding returns the value a prover must put in the first two public inputs
// of a proof of claim_id about a checkpoint on this chain.
func (q queryServer) Binding(ctx context.Context, req *types.QueryBindingRequest) (*types.QueryBindingResponse, error) {
	if req == nil || req.SidechainId == "" || req.CheckpointSequence == 0 {
		return nil, status.Error(codes.InvalidArgument, "sidechain_id and a checkpoint_sequence >= 1 are required")
	}
	if err := types.ValidateID("claim_id", req.ClaimId); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	binding, cp, err := q.k.checkpointBinding(ctx, req.SidechainId, req.CheckpointSequence, req.ClaimId)
	if err != nil {
		return nil, toStatusErr(err, types.ErrCheckpointNotFound)
	}

	return &types.QueryBindingResponse{
		Binding:        binding[:],
		Hi:             binding.Hi(),
		Lo:             binding.Lo(),
		ChainId:        sdkChainID(ctx),
		StateRoot:      cp.StateRoot,
		CheckpointHash: cp.CheckpointHash,
	}, nil
}
