package verifiers_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lucidpay/lucidchain/x/proofs/types"
	"github.com/lucidpay/lucidchain/x/proofs/verifiers"
)

type fakeTransport struct {
	resp  verifiers.PQResponse
	err   error
	calls int
	last  verifiers.PQRequest
}

func (f *fakeTransport) Verify(r verifiers.PQRequest) (verifiers.PQResponse, error) {
	f.calls++
	f.last = r
	return f.resp, f.err
}

var (
	pinnedImage = [32]byte{0xAA, 0x01}
	testVKID    = [verifiers.VKIDLen]byte{0x11, 0x22}
	maxProof    = 1024
)

func newPQ(tr *fakeTransport) types.ProofVerifier {
	return verifiers.NewPQ(tr, pinnedImage, [][verifiers.VKIDLen]byte{testVKID}, maxProof)
}

func pqBinding(seq uint64) types.Binding {
	return types.ComputeBinding("lucidchain-1", "sc-1", seq, []byte("state-root"), []byte("checkpoint-hash"), "geofence.v1")
}

func TestPQVerifier(t *testing.T) {
	binding, other := pqBinding(7), pqBinding(8)
	pub, err := verifiers.PQPublicInputs(binding, []uint32{42})
	require.NoError(t, err)
	proof := bytes.Repeat([]byte{0x5a}, 200)
	vk := testVKID[:]

	t.Run("registered in the default-style map", func(t *testing.T) {
		m := verifiers.Default()
		m[verifiers.PQBabyBear] = newPQ(&fakeTransport{})
		require.NoError(t, types.ValidateID("proof_system_id", verifiers.PQBabyBear))
		require.NotNil(t, m[verifiers.PQBabyBear])
	})

	t.Run("verification key", func(t *testing.T) {
		v := newPQ(&fakeTransport{})
		require.NoError(t, v.ValidateKey(vk))
		require.Error(t, v.ValidateKey(nil))
		require.Error(t, v.ValidateKey(vk[:31]), "short")
		require.Error(t, v.ValidateKey(append(bytes.Clone(vk), 0)), "long")
		require.Error(t, v.ValidateKey(make([]byte, 32)), "not in allowlist")
	})

	t.Run("binding", func(t *testing.T) {
		v := newPQ(&fakeTransport{})
		require.NoError(t, v.CheckBinding(pub, binding))
		require.Error(t, v.CheckBinding(pub, other), "different checkpoint")
		require.Error(t, v.CheckBinding(nil, binding))
		require.Error(t, v.CheckBinding(append(bytes.Clone(pub), 0), binding), "trailing byte")
		require.Error(t, v.CheckBinding(pub[:len(pub)-4], binding), "count mismatch")

		bad := bytes.Clone(pub)
		binary.LittleEndian.PutUint32(bad[4:], 0xFFFFFFFF) // >= modulus
		require.Error(t, v.CheckBinding(bad, binding), "non-canonical field element")
	})

	t.Run("valid proof", func(t *testing.T) {
		tr := &fakeTransport{resp: verifiers.PQResponse{Status: verifiers.StatusValid, ImageHash: pinnedImage}}
		require.NoError(t, newPQ(tr).Verify(vk, pub, proof))
		require.Equal(t, 1, tr.calls)
		require.Equal(t, testVKID, tr.last.VKID)
		require.Equal(t, pub, tr.last.PublicInputs)
	})

	t.Run("deterministic rejections are not faults", func(t *testing.T) {
		for _, st := range []verifiers.Status{verifiers.StatusInvalid, verifiers.StatusMalformed} {
			tr := &fakeTransport{resp: verifiers.PQResponse{Status: st, ImageHash: pinnedImage}}
			err := newPQ(tr).Verify(vk, pub, proof)
			require.Error(t, err)
			require.False(t, errors.Is(err, types.ErrVerifierFault), "status %d", st)
		}
	})

	t.Run("infrastructure failures are faults", func(t *testing.T) {
		cases := map[string]*fakeTransport{
			"transport error": {err: errors.New("vsock: connection reset")},
			"wrong image":     {resp: verifiers.PQResponse{Status: verifiers.StatusValid, ImageHash: [32]byte{0xBB}}},
			"unknown status":  {resp: verifiers.PQResponse{Status: 99, ImageHash: pinnedImage}},
		}
		for name, tr := range cases {
			err := newPQ(tr).Verify(vk, pub, proof)
			require.ErrorIs(t, err, types.ErrVerifierFault, name)
		}
	})

	t.Run("rejected before reaching the microVM", func(t *testing.T) {
		tr := &fakeTransport{resp: verifiers.PQResponse{Status: verifiers.StatusValid, ImageHash: pinnedImage}}
		v := newPQ(tr)
		require.Error(t, v.Verify(vk, pub, nil), "empty proof")
		require.Error(t, v.Verify(vk, pub, make([]byte, maxProof+1)), "oversized proof")
		require.Error(t, v.Verify(vk, append(bytes.Clone(pub), 0), proof), "non-canonical public inputs")
		require.Error(t, v.Verify(make([]byte, 32), pub, proof), "unknown vk_id")
		require.Equal(t, 0, tr.calls, "no call may reach the verifier")
	})

	t.Run("public input encoding", func(t *testing.T) {
		_, err := verifiers.PQPublicInputs(binding, []uint32{0xFFFFFFFF})
		require.Error(t, err, "extra value not a field element")
	})
}
