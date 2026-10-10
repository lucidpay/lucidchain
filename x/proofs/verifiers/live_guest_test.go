package verifiers

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

//	Run: PQ_GUEST_SOCKET=/path/to/guest.sock go test ./x/proofs/verifiers \
//	       -run xxx -fuzz=FuzzLiveGuest -fuzztime=10m -parallel=8
//
// maxConns is 1 per worker so 8 workers stay under the guest's 16 connections.
func FuzzLiveGuest(f *testing.F) {
	sock := os.Getenv("PQ_GUEST_SOCKET")
	if sock == "" {
		f.Skip("set PQ_GUEST_SOCKET to a unix socket serving the guest wire protocol")
	}
	dial := func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", sock)
	}
	tr, err := NewVsockTransport(dial, 1, 30*time.Second)
	if err != nil {
		f.Fatal(err)
	}
	defer tr.Close()

	f.Add(make([]byte, VKIDLen), encodeVals(make([]uint32, bindingLimbs)), []byte{0x5a})

	var firstImage *[32]byte
	f.Fuzz(func(t *testing.T, vkid, pub, proof []byte) {
		if len(pub) > wireMaxPublicInputBytes || len(proof) > PQWireMaxProofBytes {
			t.Skip()
		}
		var req PQRequest
		copy(req.VKID[:], vkid)
		req.PublicInputs, req.Proof = pub, proof

		r1, err := tr.Verify(req)
		require.NoError(t, err, "guest must answer every well-framed request")
		require.LessOrEqual(t, uint8(r1.Status), uint8(StatusMalformed), "unknown status")
		r2, err := tr.Verify(req)
		require.NoError(t, err)
		require.Equal(t, r1, r2, "verdict must be deterministic")

		if firstImage == nil {
			h := r1.ImageHash
			firstImage = &h
		}
		require.Equal(t, *firstImage, r1.ImageHash, "image hash changed mid-run")
	})
}
