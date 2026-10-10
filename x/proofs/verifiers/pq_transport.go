package verifiers

// Transport to the isolated Rust verifier, speaking wire protocol v1 (see
// rust_reference/WIRE_SPEC.md; every constant here must match it).
//
// The transport never produces a verdict of its own. Any problem (dial
// failure, timeout, short read, closed connection, oversized request) is
// returned as an error, and pqVerifier turns that into types.ErrVerifierFault,
// which halts the node. Only a complete, well-formed response is passed up.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	wireVersion     byte = 2
	wireDigestLen        = 16
	wireResponseLen      = 1 + 32 + wireDigestLen

	//wireVersion byte = 1

	// PQWireMaxProofBytes is the largest proof the guest accepts. Pass a value
	// no larger than this as maxProofBytes to NewPQ.
	PQWireMaxProofBytes = 512 * 1024

	wireMaxPublicInputBytes = 4 + 4*maxPQPublicValues
	wireHeaderLen           = 1 + VKIDLen + 4 + 4
	//wireResponseLen         = 1 + 32
)

// Dialer opens a fresh connection to the verifier service in the guest.
type Dialer func(ctx context.Context) (net.Conn, error)

// VsockTransport is a PQTransport over a small pool of persistent connections.
type VsockTransport struct {
	dial    Dialer
	timeout time.Duration
	slots   chan struct{} // bounds in-flight requests and open connections

	mu     sync.Mutex
	idle   []net.Conn
	closed bool
}

var _ PQTransport = (*VsockTransport)(nil)

// NewVsockTransport returns a transport that keeps at most maxConns
// connections open (it must not exceed the guest's MAX_CONNECTIONS, 16) and
// gives each request at most requestTimeout in total, including waiting for a
// free connection, dialing, writing and reading. A timeout is an error, never
// a verdict.
func NewVsockTransport(dial Dialer, maxConns int, requestTimeout time.Duration) (*VsockTransport, error) {
	if dial == nil {
		return nil, errors.New("dialer is required")
	}
	if maxConns < 1 {
		return nil, errors.New("maxConns must be at least 1")
	}
	if requestTimeout <= 0 {
		return nil, errors.New("requestTimeout must be positive")
	}
	return &VsockTransport{dial: dial, timeout: requestTimeout, slots: make(chan struct{}, maxConns)}, nil
}

// Verify implements PQTransport.
func (t *VsockTransport) Verify(req PQRequest) (PQResponse, error) {
	if len(req.PublicInputs) > wireMaxPublicInputBytes {
		return PQResponse{}, fmt.Errorf("public inputs are %d bytes, wire limit is %d", len(req.PublicInputs), wireMaxPublicInputBytes)
	}
	if len(req.Proof) > PQWireMaxProofBytes {
		return PQResponse{}, fmt.Errorf("proof is %d bytes, wire limit is %d", len(req.Proof), PQWireMaxProofBytes)
	}
	frame := encodeRequest(req)

	deadline := time.Now().Add(t.timeout)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	select {
	case t.slots <- struct{}{}:
		defer func() { <-t.slots }()
	case <-ctx.Done():
		return PQResponse{}, fmt.Errorf("waiting for a verifier connection: %w", ctx.Err())
	}

	conn, pooled, err := t.get(ctx)
	if err != nil {
		return PQResponse{}, err
	}
	resp, err := roundTrip(conn, frame, deadline)
	if err != nil {
		_ = conn.Close()
		// A pooled connection can go stale when the guest restarts. Verification
		// is a pure function, so repeating the request once on a fresh
		// connection is safe. Never retry a fresh connection, and never retry
		// a timeout (the deadline is already spent).
		if !pooled || errors.Is(err, os.ErrDeadlineExceeded) {
			return PQResponse{}, err
		}
		if conn, err = t.dialOpen(ctx); err != nil {
			return PQResponse{}, err
		}
		if resp, err = roundTrip(conn, frame, deadline); err != nil {
			_ = conn.Close()
			return PQResponse{}, err
		}
	}
	t.put(conn)
	return resp, nil
}

// Close closes all idle connections and makes later calls fail. Requests
// already in flight finish and then close their connection.
func (t *VsockTransport) Close() error {
	t.mu.Lock()
	t.closed = true
	idle := t.idle
	t.idle = nil
	t.mu.Unlock()
	for _, c := range idle {
		_ = c.Close()
	}
	return nil
}

func (t *VsockTransport) get(ctx context.Context) (conn net.Conn, pooled bool, err error) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil, false, errors.New("transport is closed")
	}
	if n := len(t.idle); n > 0 {
		conn = t.idle[n-1]
		t.idle = t.idle[:n-1]
		t.mu.Unlock()
		return conn, true, nil
	}
	t.mu.Unlock()
	conn, err = t.dialOpen(ctx)
	return conn, false, err
}

func (t *VsockTransport) dialOpen(ctx context.Context) (net.Conn, error) {
	conn, err := t.dial(ctx)
	if err != nil {
		return nil, fmt.Errorf("dial verifier: %w", err)
	}
	return conn, nil
}

func (t *VsockTransport) put(conn net.Conn) {
	_ = conn.SetDeadline(time.Time{})
	t.mu.Lock()
	if t.closed || len(t.idle) >= cap(t.slots) {
		t.mu.Unlock()
		_ = conn.Close()
		return
	}
	t.idle = append(t.idle, conn)
	t.mu.Unlock()
}

func roundTrip(conn net.Conn, frame []byte, deadline time.Time) (PQResponse, error) {
	if err := conn.SetDeadline(deadline); err != nil {
		return PQResponse{}, fmt.Errorf("set deadline: %w", err)
	}
	if _, err := conn.Write(frame); err != nil {
		return PQResponse{}, fmt.Errorf("write request: %w", err)
	}
	var buf [wireResponseLen]byte
	if _, err := io.ReadFull(conn, buf[:]); err != nil {
		return PQResponse{}, fmt.Errorf("read response: %w", err)
	}

	sum := sha256.Sum256(frame)
	if !bytes.Equal(buf[33:], sum[:wireDigestLen]) {
		return PQResponse{}, errors.New("response does not match request (stream desync)")
	}

	var resp PQResponse
	resp.Status = Status(buf[0])
	copy(resp.ImageHash[:], buf[1:])
	return resp, nil
}

func encodeRequest(req PQRequest) []byte {
	buf := make([]byte, wireHeaderLen+len(req.PublicInputs)+len(req.Proof))
	buf[0] = wireVersion
	copy(buf[1:1+VKIDLen], req.VKID[:])
	binary.LittleEndian.PutUint32(buf[1+VKIDLen:], uint32(len(req.PublicInputs)))
	binary.LittleEndian.PutUint32(buf[1+VKIDLen+4:], uint32(len(req.Proof)))
	n := copy(buf[wireHeaderLen:], req.PublicInputs)
	copy(buf[wireHeaderLen+n:], req.Proof)
	return buf
}

// DialFirecracker returns a Dialer for guests whose vsock device is exposed on
// the host as a Unix socket (Firecracker, and Cloud Hypervisor in its vsock
// proxy mode). After connecting to udsPath, the host sends "CONNECT <port>\n"
// and the proxy answers "OK <n>\n" once the guest listener accepts.
//
// Kernel AF_VSOCK hosts (for example QEMU with vhost-vsock) use DialVsock
// instead; see pq_vsock_linux.go.
func DialFirecracker(udsPath string, port uint32) Dialer {
	return func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		conn, err := d.DialContext(ctx, "unix", udsPath)
		if err != nil {
			return nil, err
		}
		deadline, ok := ctx.Deadline()
		if !ok {
			deadline = time.Now().Add(5 * time.Second)
		}
		_ = conn.SetDeadline(deadline)

		if _, err := fmt.Fprintf(conn, "CONNECT %d\n", port); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("vsock handshake: %w", err)
		}
		line, err := readLine(conn, 64)
		if err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("vsock handshake: %w", err)
		}
		if !strings.HasPrefix(line, "OK ") {
			_ = conn.Close()
			return nil, fmt.Errorf("vsock handshake refused: %q", line)
		}
		_ = conn.SetDeadline(time.Time{})
		return conn, nil
	}
}

// readLine reads up to max bytes one at a time, so nothing beyond the newline
// is consumed from the stream.
func readLine(r io.Reader, max int) (string, error) {
	var sb strings.Builder
	var b [1]byte
	for sb.Len() < max {
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return "", err
		}
		if b[0] == '\n' {
			return sb.String(), nil
		}
		sb.WriteByte(b[0])
	}
	return "", errors.New("handshake line too long")
}
