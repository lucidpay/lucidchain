package keeper

import (
	"bytes"
	"context"

	errorsmod "cosmossdk.io/errors"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"

	"github.com/lucidpay/lucidchain/x/checkpoint/types"
)

// UpdateParams implements types.MsgServer.
func (ms msgServer) UpdateParams(ctx context.Context, req *types.MsgUpdateParams) (*types.MsgUpdateParamsResponse, error) {
	authority, err := ms.addressCodec.StringToBytes(req.Authority)
	if err != nil {
		return nil, errorsmod.Wrap(sdkerrors.ErrInvalidAddress, "invalid authority address")
	}

	if !bytes.Equal(ms.GetAuthority(), authority) {
		expected, _ := ms.addressCodec.BytesToString(ms.GetAuthority())
		return nil, errorsmod.Wrapf(sdkerrors.ErrUnauthorized,
			"invalid authority; expected %s, got %s", expected, req.Authority)
	}

	if err := req.Params.Validate(); err != nil {
		return nil, err
	}
	if err := ms.SetParams(ctx, req.Params); err != nil {
		return nil, err
	}

	return &types.MsgUpdateParamsResponse{}, nil
}
