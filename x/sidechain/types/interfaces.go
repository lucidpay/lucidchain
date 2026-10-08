package types

import (
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
)

// Anything that carries signer_keys (a repeated Any holding a cosmos.crypto.PubKey)
// must implement UnpackInterfacesMessage, otherwise the Any values are never
// resolved and GetCachedValue() returns nil. x/checkpoint reads the cached keys
// to verify checkpoint signatures, so this is not optional.
var (
	_ codectypes.UnpackInterfacesMessage = (*Sidechain)(nil)
	_ codectypes.UnpackInterfacesMessage = (*MsgRegisterSidechain)(nil)
	_ codectypes.UnpackInterfacesMessage = (*MsgUpdateSidechainSigners)(nil)
	_ codectypes.UnpackInterfacesMessage = (*GenesisState)(nil)
	_ codectypes.UnpackInterfacesMessage = (*QuerySidechainResponse)(nil)
	_ codectypes.UnpackInterfacesMessage = (*QuerySidechainsResponse)(nil)
)

func unpackKeys(keys []*codectypes.Any, unpacker codectypes.AnyUnpacker) error {
	for _, a := range keys {
		var pk cryptotypes.PubKey
		if err := unpacker.UnpackAny(a, &pk); err != nil {
			return err
		}
	}
	return nil
}

// UnpackInterfaces implements UnpackInterfacesMessage.
func (s *Sidechain) UnpackInterfaces(unpacker codectypes.AnyUnpacker) error {
	return unpackKeys(s.SignerKeys, unpacker)
}

// UnpackInterfaces implements UnpackInterfacesMessage.
func (m *MsgRegisterSidechain) UnpackInterfaces(unpacker codectypes.AnyUnpacker) error {
	return unpackKeys(m.SignerKeys, unpacker)
}

// UnpackInterfaces implements UnpackInterfacesMessage.
func (m *MsgUpdateSidechainSigners) UnpackInterfaces(unpacker codectypes.AnyUnpacker) error {
	return unpackKeys(m.SignerKeys, unpacker)
}

// UnpackInterfaces implements UnpackInterfacesMessage.
func (gs *GenesisState) UnpackInterfaces(unpacker codectypes.AnyUnpacker) error {
	for i := range gs.Sidechains {
		if err := gs.Sidechains[i].UnpackInterfaces(unpacker); err != nil {
			return err
		}
	}
	return nil
}

// UnpackInterfaces implements UnpackInterfacesMessage.
func (r *QuerySidechainResponse) UnpackInterfaces(unpacker codectypes.AnyUnpacker) error {
	return r.Sidechain.UnpackInterfaces(unpacker)
}

// UnpackInterfaces implements UnpackInterfacesMessage.
func (r *QuerySidechainsResponse) UnpackInterfaces(unpacker codectypes.AnyUnpacker) error {
	for i := range r.Sidechains {
		if err := r.Sidechains[i].UnpackInterfaces(unpacker); err != nil {
			return err
		}
	}
	return nil
}
