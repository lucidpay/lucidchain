package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"

	"github.com/lucidpay/lucidchain/x/attestor/types"
	sidechaintypes "github.com/lucidpay/lucidchain/x/sidechain/types"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// putSidechain (re)defines a sidechain in the mock. With no keys it gets one
// fresh random checkpoint signer key, unrelated to every fixture key.
func (f *fixture) putSidechain(t *testing.T, id, operator string, keys ...cryptotypes.PubKey) {
	t.Helper()
	if len(keys) == 0 {
		keys = []cryptotypes.PubKey{ed25519.GenPrivKey().PubKey()}
	}
	anys := make([]*codectypes.Any, len(keys))
	for i, k := range keys {
		a, err := codectypes.NewAnyWithValue(k)
		require.NoError(t, err)
		anys[i] = a
	}
	f.sidechains.m[id] = sidechaintypes.Sidechain{
		Id:                 id,
		Operator:           operator,
		SignerKeys:         anys,
		SignatureThreshold: 1,
		SignerSetVersion:   1,
		Tier:               sidechaintypes.AssuranceTier_ASSURANCE_TIER_ATTESTED,
		Status:             sidechaintypes.SidechainStatus_SIDECHAIN_STATUS_ACTIVE,
	}
}

// addrOf returns the bech32 account address derived from a public key.
func (f *fixture) addrOf(t *testing.T, pk cryptotypes.PubKey) string {
	t.Helper()
	s, err := f.addressCodec.BytesToString(pk.Address())
	require.NoError(t, err)
	return s
}

// requireNoAttestation asserts that nothing was stored for (testSidechain, seq 1)
// and that the attestor's reputation did not move.
func (f *fixture) requireNoAttestation(t *testing.T) {
	t.Helper()
	id := types.AttestationID(testSchema, testSidechain, 1, testAttestor)
	has, err := f.keeper.Attestations.Has(f.ctx, id)
	require.NoError(t, err)
	require.False(t, has)
	require.Zero(t, f.getAttestor(t).ReputationScore)
}

// ---------------------------------------------------------------------------
// SubmitAttestation
// ---------------------------------------------------------------------------

func TestSubmitAttestation_RejectsSidechainOwnerConflict(t *testing.T) {
	// Attestor under test: operator f.operator, signer keys f.signers[0..2].
	cases := []struct {
		name  string
		setup func(t *testing.T, f *fixture)
	}{
		{"attestor operator is the sidechain operator", func(t *testing.T, f *fixture) {
			f.putSidechain(t, testSidechain, f.operatorS)
		}},
		{"attestor signer key is also a sidechain checkpoint signer", func(t *testing.T, f *fixture) {
			f.putSidechain(t, testSidechain, f.sidechainOwnerS, f.signers[1].PubKey())
		}},
		{"attestor signer key address is the sidechain operator", func(t *testing.T, f *fixture) {
			// Position 2 is registered but not even used to sign: being in
			// the signer set is enough.
			f.putSidechain(t, testSidechain, f.addrOf(t, f.signers[2].PubKey()))
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newActiveFixture(t)
			tc.setup(t, f)

			p, err := f.keeper.GetParams(f.ctx)
			require.NoError(t, err)
			before := f.ctx.GasMeter().GasConsumed()

			_, err = f.msgServer.SubmitAttestation(f.ctx, f.attestMsg(t, 1, 0, 1))
			require.ErrorIs(t, err, types.ErrConflictOfInterest)

			// The check runs before signature verification: no verification gas.
			require.Less(t, f.ctx.GasMeter().GasConsumed()-before, p.SignatureVerificationGas)
			f.requireNoAttestation(t)
		})
	}
}

func TestSubmitAttestation_OwnerMayAttestOtherSidechains(t *testing.T) {
	f := newActiveFixture(t)

	// The attestor's operator owns testSidechain, so it may not attest there...
	f.putSidechain(t, testSidechain, f.operatorS)
	_, err := f.msgServer.SubmitAttestation(f.ctx, f.attestMsg(t, 1, 0, 1))
	require.ErrorIs(t, err, types.ErrConflictOfInterest)

	// ...but the very same attestor can attest somebody else's sidechain.
	const other = "sc-2"
	f.putSidechain(t, other, f.sidechainOwnerS)
	hash := f.checkpoints.add(other, 1)

	m := f.attestMsg(t, 1, 0, 1)
	m.SidechainId = other
	f.signAttestation(t, m, testChainID, hash, 0, 1)

	resp, err := f.msgServer.SubmitAttestation(f.ctx, m)
	require.NoError(t, err)
	require.Equal(t, types.AttestationID(testSchema, other, 1, testAttestor), resp.AttestationId)
	require.Equal(t, other, f.getAttestation(t, resp.AttestationId).SidechainId)
}

func TestSubmitAttestation_UnknownSidechain(t *testing.T) {
	f := newActiveFixture(t)
	delete(f.sidechains.m, testSidechain)

	_, err := f.msgServer.SubmitAttestation(f.ctx, f.attestMsg(t, 1, 0, 1))
	require.ErrorIs(t, err, types.ErrSidechainNotFound)
	f.requireNoAttestation(t)
}

func TestSubmitAttestation_ConflictCheckFailsClosed(t *testing.T) {
	f := newActiveFixture(t)

	// A sidechain whose signer key was not unpacked (no cached value) must not
	// let a submission slip past the conflict check.
	f.sidechains.m[testSidechain] = sidechaintypes.Sidechain{
		Id:         testSidechain,
		Operator:   f.sidechainOwnerS,
		Tier:       sidechaintypes.AssuranceTier_ASSURANCE_TIER_ATTESTED, // added
		SignerKeys: []*codectypes.Any{{TypeUrl: sdk.MsgTypeURL(&ed25519.PubKey{})}},
	}

	_, err := f.msgServer.SubmitAttestation(f.ctx, f.attestMsg(t, 1, 0, 1))
	require.ErrorIs(t, err, sdkerrors.ErrInvalidType)
	require.NotErrorIs(t, err, types.ErrConflictOfInterest)
	f.requireNoAttestation(t)
}

// ---------------------------------------------------------------------------
// genesis
// ---------------------------------------------------------------------------

// exportWithAttestation returns an exported genesis containing one attestation
// on testSidechain, plus a fresh target fixture with a funded module account.
func exportWithAttestation(t *testing.T) (types.GenesisState, *fixture) {
	t.Helper()
	f := newActiveFixture(t)
	f.attest(t, 1)
	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.Len(t, exported.Attestations, 1)

	g := newFixture(t)
	g.bank.modules[types.ModuleName] = f.bank.moduleBalance(types.ModuleName)
	return *exported, g
}

func TestGenesis_RejectsAttestationOnOwnSidechain(t *testing.T) {
	exported, g := exportWithAttestation(t)

	// In the imported chain, the attestor's operator also operates the sidechain.
	g.putSidechain(t, testSidechain, g.operatorS)

	err := g.keeper.InitGenesis(g.ctx, exported)
	require.ErrorIs(t, err, types.ErrConflictOfInterest)
}

func TestGenesis_RejectsAttestationOnUnknownSidechain(t *testing.T) {
	exported, g := exportWithAttestation(t)
	// x/sidechain has no state: it was not initialized first, or the genesis is wrong.

	err := g.keeper.InitGenesis(g.ctx, exported)
	require.ErrorContains(t, err, "not found")
}

func TestGenesis_SignerKeyOverlapDoesNotBlockImport(t *testing.T) {
	exported, g := exportWithAttestation(t)

	// Sidechain signer keys are mutable, and public keys are visible on chain.
	// If genesis compared them, a sidechain operator could rotate in an honest
	// attestor's public key and make every later export unimportable. Genesis
	// therefore compares only the immutable operator addresses.
	g.putSidechain(t, testSidechain, g.sidechainOwnerS, g.signers[0].PubKey())

	require.NoError(t, g.keeper.InitGenesis(g.ctx, exported))
}
