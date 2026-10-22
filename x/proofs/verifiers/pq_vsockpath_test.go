package verifiers

import (
	"context"
	"encoding/hex"
	"net"
	"os"
	"os/exec"
	"testing"
	"time"
)

// startGuest runs the Rust vsock request loop over TCP (stand-in for vsock).
func startGuest(t *testing.T, image [32]byte) string {
	bin := os.Getenv("TCP_SERVE")
	if bin == "" {
		t.Skip("TCP_SERVE not set")
	}
	addr := "127.0.0.1:" + os.Getenv("TCP_PORT")
	cmd := exec.Command(bin, addr, hex.EncodeToString(image[:]))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	for i := 0; i < 50; i++ {
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			return addr
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("guest did not start")
	return ""
}

func TestVsockPathAgainstRustGuest(t *testing.T) {
	image := [32]byte{0xAA, 0x01}
	addr := startGuest(t, image)
	dial := func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", addr)
	}
	tr, err := NewVsockTransport(dial, 2, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	var id [VKIDLen]byte
	copy(id[:], fx(t, "vkid.bin"))
	resp, err := tr.Verify(PQRequest{VKID: id, PublicInputs: fx(t, "pub_256.bin"), Proof: fx(t, "proof_256.bin")})
	if err != nil {
		t.Fatalf("vsock-path round trip failed: %v", err)
	}
	if resp.Status != StatusValid || resp.ImageHash != image {
		t.Fatalf("got %+v", resp)
	}
}

func TestVsockPathBehaviour(t *testing.T) {
	image := [32]byte{0xAA, 0x01}
	addr := startGuest(t, image)
	dial := func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", addr)
	}
	vt, err := NewVsockTransport(dial, 2, 20*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer vt.Close()
	wt, _, _ := newWasm(t, 2, 60*time.Second, 1024)

	var id [VKIDLen]byte
	copy(id[:], fx(t, "vkid.bin"))
	pub, proof := fx(t, "pub_256.bin"), fx(t, "proof_256.bin")

	// 1. pooled connection reused across many requests, and both transports agree
	for k := 0; k < 30; k++ {
		m := append([]byte(nil), proof...)
		pos := int((uint64(k) * 2654435761) % uint64(len(m)))
		m[pos] ^= 1 << (k % 8)
		req := PQRequest{VKID: id, PublicInputs: pub, Proof: m}
		a, errA := vt.Verify(req)
		b, errB := wt.Verify(req)
		if errA != nil || errB != nil {
			t.Fatalf("k=%d vsock=%v wasm=%v", k, errA, errB)
		}
		if a.Status != b.Status {
			t.Fatalf("k=%d verdict mismatch vsock=%d wasm=%d", k, a.Status, b.Status)
		}
	}
	// 2. a valid proof on the (now pooled) connection, and the worst-case size
	for _, n := range []string{"256", "16384"} {
		resp, err := vt.Verify(PQRequest{VKID: id, PublicInputs: fx(t, "pub_"+n+".bin"), Proof: fx(t, "proof_"+n+".bin")})
		if err != nil || resp.Status != StatusValid {
			t.Fatalf("n=%s: %+v %v", n, resp, err)
		}
	}
	// 3. unknown vk_id: the guest closes without answering -> transport error (fault)
	bad := id
	bad[0] ^= 0xFF
	if _, err := vt.Verify(PQRequest{VKID: bad, PublicInputs: pub, Proof: proof}); err == nil {
		t.Fatal("unknown vk_id must surface as a transport error")
	} else {
		t.Logf("unknown vk_id -> %v", err)
	}
}
