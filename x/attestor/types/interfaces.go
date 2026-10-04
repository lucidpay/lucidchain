package types

import (
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
)

// Anything that carries signer_keys (a repeated Any holding a cosmos.crypto.PubKey)
// must implement UnpackInterfacesMessage, otherwise the Any values are never
// resolved and GetCachedValue() returns nil.
var (
	_ codectypes.UnpackInterfacesMessage = (*Attestor)(nil)
	_ codectypes.UnpackInterfacesMessage = (*MsgRegisterAttestor)(nil)
	_ codectypes.UnpackInterfacesMessage = (*MsgUpdateAttestorSigners)(nil)
	_ codectypes.UnpackInterfacesMessage = (*GenesisState)(nil)
	_ codectypes.UnpackInterfacesMessage = (*QueryAttestorResponse)(nil)
	_ codectypes.UnpackInterfacesMessage = (*QueryAttestorsResponse)(nil)
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
func (a *Attestor) UnpackInterfaces(unpacker codectypes.AnyUnpacker) error {
	return unpackKeys(a.SignerKeys, unpacker)
}

// UnpackInterfaces implements UnpackInterfacesMessage.
func (m *MsgRegisterAttestor) UnpackInterfaces(unpacker codectypes.AnyUnpacker) error {
	return unpackKeys(m.SignerKeys, unpacker)
}

// UnpackInterfaces implements UnpackInterfacesMessage.
func (m *MsgUpdateAttestorSigners) UnpackInterfaces(unpacker codectypes.AnyUnpacker) error {
	return unpackKeys(m.SignerKeys, unpacker)
}

// UnpackInterfaces implements UnpackInterfacesMessage.
func (gs *GenesisState) UnpackInterfaces(unpacker codectypes.AnyUnpacker) error {
	for i := range gs.Attestors {
		if err := gs.Attestors[i].UnpackInterfaces(unpacker); err != nil {
			return err
		}
	}
	return nil
}

// UnpackInterfaces implements UnpackInterfacesMessage.
func (r *QueryAttestorResponse) UnpackInterfaces(unpacker codectypes.AnyUnpacker) error {
	return r.Attestor.UnpackInterfaces(unpacker)
}

// UnpackInterfaces implements UnpackInterfacesMessage.
func (r *QueryAttestorsResponse) UnpackInterfaces(unpacker codectypes.AnyUnpacker) error {
	for i := range r.Attestors {
		if err := r.Attestors[i].UnpackInterfaces(unpacker); err != nil {
			return err
		}
	}
	return nil
}
