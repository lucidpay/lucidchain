package verifiers

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"io"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/lucidpay/lucidchain/x/proofs/types"
)

func encodeVals(vals []uint32) []byte {
	buf := make([]byte, 4+4*len(vals))
	binary.LittleEndian.PutUint32(buf, uint32(len(vals)))
	for i, v := range vals {
		binary.LittleEndian.PutUint32(buf[4+4*i:], v)
	}
	return buf
}

// Anything the parser accepts must be the one canonical encoding.
func FuzzParsePQPublicInputs(f *testing.F) {
	f.Add([]byte{})
	f.Add(encodeVals(make([]uint32, bindingLimbs)))
	f.Add(encodeVals(make([]uint32, maxPQPublicValues)))
	f.Fuzz(func(t *testing.T, bz []byte) {
		vals, err := parsePQPublicInputs(bz)
		if err != nil {
			return
		}
		require.GreaterOrEqual(t, len(vals), bindingLimbs)
		require.LessOrEqual(t, len(vals), maxPQPublicValues)
		for _, v := range vals {
			require.Less(t, v, babyBearModulus)
		}
		require.Equal(t, bz, encodeVals(vals), "accepted input must be the unique canonical encoding")
	})
}

// The limb split must reassemble to hi||lo, and the adapter must accept its
// own public-input encoding.
func FuzzBindingLimbs(f *testing.F) {
	f.Add("lucidchain-1", "sc-1", uint64(7), []byte("root"), []byte("hash"), "geofence.v1")
	f.Fuzz(func(t *testing.T, chain, sc string, seq uint64, root, hash []byte, claim string) {
		b := types.ComputeBinding(chain, sc, seq, root, hash, claim)
		limbs, err := bindingToLimbs(b)
		require.NoError(t, err)
		require.Len(t, limbs, bindingLimbs)

		hi, lo := b.Limbs()
		want := new(big.Int).Lsh(hi, 128)
		want.Or(want, lo)
		got := new(big.Int)
		for i := len(limbs) - 1; i >= 0; i-- {
			require.Less(t, limbs[i], uint32(1<<limbBits))
			got.Lsh(got, limbBits)
			got.Or(got, big.NewInt(int64(limbs[i])))
		}
		require.Zero(t, want.Cmp(got))

		pub, err := PQPublicInputs(b, nil)
		require.NoError(t, err)
		require.NoError(t, pqVerifier{}.CheckBinding(pub, b))
	})
}

// Different tuples must never share a binding (catches ambiguous field
// concatenation such as ("ab","c") vs ("a","bc")).
func FuzzBindingInjective(f *testing.F) {
	f.Add("ab", "c", "x", uint64(1), "a", "bc", "x", uint64(1))
	f.Fuzz(func(t *testing.T, c1, s1, k1 string, q1 uint64, c2, s2, k2 string, q2 uint64) {
		if c1 == c2 && s1 == s2 && k1 == k2 && q1 == q2 {
			return
		}
		root, hash := []byte("r"), []byte("h")
		a := types.ComputeBinding(c1, s1, q1, root, hash, k1)
		b := types.ComputeBinding(c2, s2, q2, root, hash, k2)
		require.NotEqual(t, a[:], b[:], "binding collision between distinct tuples")
	})
}

// The transport must return either a complete response whose digest matches
// the request, or an error, and never panic or hang.
func FuzzRoundTripResponse(f *testing.F) {
	frame := encodeRequest(PQRequest{PublicInputs: []byte{1, 2, 3}, Proof: []byte{4, 5}})
	sum := sha256.Sum256(frame)
	valid := make([]byte, wireResponseLen)
	copy(valid[1+32:], sum[:wireDigestLen])
	f.Add(valid)
	f.Add(append(bytes.Clone(valid), make([]byte, 8)...))
	f.Add(make([]byte, wireResponseLen)) // wrong digest
	f.Add([]byte{})
	f.Add(make([]byte, wireResponseLen-1))
	f.Fuzz(func(t *testing.T, reply []byte) {
		client, server := net.Pipe()
		defer client.Close()
		go func() {
			defer server.Close()
			_, _ = io.CopyN(io.Discard, server, int64(len(frame)))
			_, _ = server.Write(reply)
		}()
		resp, err := roundTrip(client, frame, time.Now().Add(2*time.Second))
		if len(reply) < wireResponseLen {
			require.Error(t, err)
			return
		}
		if !bytes.Equal(reply[1+32:wireResponseLen], sum[:wireDigestLen]) {
			require.ErrorContains(t, err, "stream desync")
			return
		}
		require.NoError(t, err)
		require.Equal(t, Status(reply[0]), resp.Status)
		require.Equal(t, reply[1:1+32], resp.ImageHash[:])
	})
}

// The Firecracker handshake reader must stay bounded and must not consume
// past the newline.
func FuzzReadLine(f *testing.F) {
	f.Add([]byte("OK 1073741824\n"))
	f.Add(bytes.Repeat([]byte("x"), 100))
	f.Fuzz(func(t *testing.T, data []byte) {
		r := bytes.NewReader(data)
		line, err := readLine(r, 64)
		idx := bytes.IndexByte(data, '\n')
		if idx < 0 || idx >= 64 {
			require.Error(t, err)
			return
		}
		require.NoError(t, err)
		require.Equal(t, string(data[:idx]), line)
		require.Equal(t, len(data)-idx-1, r.Len(), "must not consume past the newline")
	})
}
