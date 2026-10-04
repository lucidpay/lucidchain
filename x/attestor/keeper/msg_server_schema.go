package keeper

import (
	"context"

	errorsmod "cosmossdk.io/errors"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/lucidpay/lucidchain/x/attestor/types"
)

// CreateAttestationSchema publishes a new schema. Authority only. New schemas
// are always created active, whatever the message says.
func (ms msgServer) CreateAttestationSchema(ctx context.Context, msg *types.MsgCreateAttestationSchema) (*types.MsgCreateAttestationSchemaResponse, error) {
	if err := ms.checkAuthority(ms.authority, msg.Authority); err != nil {
		return nil, err
	}
	if msg.Schema == nil {
		return nil, errorsmod.Wrap(types.ErrInvalidRequest, "schema is required")
	}

	schema := *msg.Schema
	if err := schema.Validate(); err != nil {
		return nil, err
	}

	exists, err := ms.Schemas.Has(ctx, schema.Id)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, errorsmod.Wrapf(types.ErrSchemaExists, "%q", schema.Id)
	}

	schema.Active = true
	if err := ms.Schemas.Set(ctx, schema.Id, schema); err != nil {
		return nil, err
	}

	emit(sdk.UnwrapSDKContext(ctx), "attestation_schema_created", attr("schema_id", schema.Id))
	return &types.MsgCreateAttestationSchemaResponse{}, nil
}

// RetireAttestationSchema marks a schema inactive. Authority only. History is
// kept: existing attestations stay valid and disputable, but no new
// attestations or attestor registrations can use the schema.
func (ms msgServer) RetireAttestationSchema(ctx context.Context, msg *types.MsgRetireAttestationSchema) (*types.MsgRetireAttestationSchemaResponse, error) {
	if err := ms.checkAuthority(ms.authority, msg.Authority); err != nil {
		return nil, err
	}

	schema, err := ms.GetSchema(ctx, msg.SchemaId)
	if err != nil {
		return nil, err
	}
	if !schema.Active {
		return nil, errorsmod.Wrapf(types.ErrSchemaInactive, "%q is already retired", schema.Id)
	}

	schema.Active = false
	if err := ms.Schemas.Set(ctx, schema.Id, schema); err != nil {
		return nil, err
	}

	emit(sdk.UnwrapSDKContext(ctx), "attestation_schema_retired", attr("schema_id", schema.Id))
	return &types.MsgRetireAttestationSchemaResponse{}, nil
}
