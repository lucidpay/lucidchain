//go:build linux && afvsock

package verifiers

// Opt-in: build with `-tags afvsock` and `go get github.com/mdlayher/vsock`.
// Use this only when the host reaches the guest over kernel AF_VSOCK (for
// example QEMU with vhost-vsock). Firecracker and Cloud Hypervisor hosts use
// DialFirecracker instead, which needs no extra dependency.
//
// TODO: NEEDS TO BE TESTED

import (
	"context"
	"net"

	"github.com/mdlayher/vsock"
)

// DialVsock returns a Dialer that connects to the guest with the given context
// id (CID) and port over AF_VSOCK.
func DialVsock(cid, port uint32) Dialer {
	return func(ctx context.Context) (net.Conn, error) {
		type result struct {
			conn net.Conn
			err  error
		}
		ch := make(chan result, 1)
		go func() {
			c, err := vsock.Dial(cid, port, nil)
			if err != nil {
				ch <- result{err: err}
				return
			}
			ch <- result{conn: c}
		}()
		select {
		case r := <-ch:
			return r.conn, r.err
		case <-ctx.Done():
			// vsock.Dial is not context-aware: close the late connection.
			go func() {
				if r := <-ch; r.conn != nil {
					_ = r.conn.Close()
				}
			}()
			return nil, ctx.Err()
		}
	}
}
