package keeper

import (
	"bytes"
	"fmt"

	errorsmod "cosmossdk.io/errors"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"

	"github.com/lucidpay/lucidchain/x/attestor/types"
	sidechaintypes "github.com/lucidpay/lucidchain/x/sidechain/types"
)

type labeledAddr struct {
	label string
	addr  []byte
}

func signerKeyAddrs(keys []*codectypes.Any) ([][]byte, error) {
	out := make([][]byte, 0, len(keys))
	for i, a := range keys {
		pk, ok := a.GetCachedValue().(cryptotypes.PubKey)
		if !ok { // fail closed
			return nil, errorsmod.Wrapf(sdkerrors.ErrInvalidType, "signer key %d not unpacked", i)
		}
		out = append(out, pk.Address().Bytes())
	}
	return out, nil
}

// checkNoSidechainConflict: the sidechain's operator and checkpoint signer keys
// are the "owner side"; the attestor's operator, submitter and signer keys are
// the "attestor side". Any overlap is rejected.
func (k Keeper) checkNoSidechainConflict(att types.Attestor, submitter string, sc sidechaintypes.Sidechain) error {
	scOp, err := k.addressCodec.StringToBytes(sc.Operator)
	if err != nil {
		return err
	}
	owner := []labeledAddr{{"sidechain operator", scOp}}
	scKeys, err := signerKeyAddrs(sc.SignerKeys)
	if err != nil {
		return err
	}
	for i, a := range scKeys {
		owner = append(owner, labeledAddr{fmt.Sprintf("sidechain signer key #%d", i), a})
	}

	attOp, err := k.addressCodec.StringToBytes(att.Operator)
	if err != nil {
		return err
	}
	sub, err := k.addressCodec.StringToBytes(submitter)
	if err != nil {
		return err
	}
	cands := []labeledAddr{{"attestor operator", attOp}, {"submitter", sub}}
	attKeys, err := signerKeyAddrs(att.SignerKeys)
	if err != nil {
		return err
	}
	for i, a := range attKeys {
		cands = append(cands, labeledAddr{fmt.Sprintf("attestor signer key #%d", i), a})
	}

	for _, c := range cands {
		for _, o := range owner {
			if bytes.Equal(c.addr, o.addr) {
				return errorsmod.Wrapf(types.ErrConflictOfInterest,
					"%s matches %s of sidechain %q", c.label, o.label, sc.Id)
			}
		}
	}
	return nil
}

// checkNoSidechainOperatorConflict compares only the two operator addresses.
// Used at genesis, where mutable signer keys must not decide validity.
func (k Keeper) checkNoSidechainOperatorConflict(att types.Attestor, sc sidechaintypes.Sidechain) error {
	a, err := k.addressCodec.StringToBytes(att.Operator)
	if err != nil {
		return err
	}
	s, err := k.addressCodec.StringToBytes(sc.Operator)
	if err != nil {
		return err
	}
	if bytes.Equal(a, s) {
		return errorsmod.Wrapf(types.ErrConflictOfInterest,
			"attestor %q operator is the operator of sidechain %q", att.Id, sc.Id)
	}
	return nil
}
