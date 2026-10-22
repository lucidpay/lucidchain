package verifiers

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

//go:embed wasm/embed/pq_verifier.wasm
var pqVerifierWasm []byte

// The guest imports nothing (no WASI, no host functions) and exports
// memory, alloc, dealloc and verify_frame. It has no _initialize or _start.

type wasmInstance struct {
	mod                 api.Module
	alloc, free, verify api.Function
}

// WasmTransport is a PQTransport that runs the verifier in-process under wazero.
type WasmTransport struct {
	rt        wazero.Runtime
	compiled  wazero.CompiledModule
	imageHash [32]byte
	timeout   time.Duration
	slots     chan struct{}      // bounds concurrent calls
	idle      chan *wasmInstance // warm instances

	mu     sync.Mutex
	closed bool
}

var _ PQTransport = (*WasmTransport)(nil)

// WasmImageHash is sha256 of the embedded guest.
func WasmImageHash() [32]byte { return sha256.Sum256(pqVerifierWasm) }

// NewWasmTransport compiles the embedded guest. memoryPages caps guest memory
// (64 KiB per page).
func NewWasmTransport(ctx context.Context, maxInstances int, timeout time.Duration, memoryPages uint32) (*WasmTransport, error) {
	if maxInstances < 1 || timeout <= 0 || memoryPages < 1 {
		return nil, errors.New("maxInstances, timeout and memoryPages must be positive")
	}
	cfg := wazero.NewRuntimeConfig().
		WithCloseOnContextDone(true).
		WithMemoryLimitPages(memoryPages)
	rt := wazero.NewRuntimeWithConfig(ctx, cfg)
	compiled, err := rt.CompileModule(ctx, pqVerifierWasm)
	if err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("compile verifier: %w", err)
	}
	if n := len(compiled.ImportedFunctions()); n != 0 {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("verifier module unexpectedly imports %d functions", n)
	}
	return &WasmTransport{
		rt: rt, compiled: compiled, imageHash: WasmImageHash(), timeout: timeout,
		slots: make(chan struct{}, maxInstances),
		idle:  make(chan *wasmInstance, maxInstances),
	}, nil
}

func (t *WasmTransport) newInstance() (*wasmInstance, error) {
	// Background ctx: instantiation must not be tied to one request's deadline.
	mod, err := t.rt.InstantiateModule(context.Background(), t.compiled,
		wazero.NewModuleConfig().WithName("").WithStartFunctions())
	if err != nil {
		return nil, fmt.Errorf("instantiate verifier: %w", err)
	}
	in := &wasmInstance{mod: mod,
		alloc:  mod.ExportedFunction("alloc"),
		free:   mod.ExportedFunction("dealloc"),
		verify: mod.ExportedFunction("verify_frame")}
	if in.alloc == nil || in.free == nil || in.verify == nil {
		_ = mod.Close(context.Background())
		return nil, errors.New("verifier module is missing required exports")
	}
	return in, nil
}

// Verify implements PQTransport. Every error is an infrastructure fault.
func (t *WasmTransport) Verify(req PQRequest) (PQResponse, error) {
	if len(req.PublicInputs) > wireMaxPublicInputBytes {
		return PQResponse{}, fmt.Errorf("public inputs are %d bytes, wire limit is %d", len(req.PublicInputs), wireMaxPublicInputBytes)
	}
	if len(req.Proof) > PQWireMaxProofBytes {
		return PQResponse{}, fmt.Errorf("proof is %d bytes, wire limit is %d", len(req.Proof), PQWireMaxProofBytes)
	}
	frame := encodeRequest(req)

	ctx, cancel := context.WithTimeout(context.Background(), t.timeout)
	defer cancel()

	select {
	case t.slots <- struct{}{}:
		defer func() { <-t.slots }()
	case <-ctx.Done():
		return PQResponse{}, fmt.Errorf("waiting for a verifier instance: %w", ctx.Err())
	}

	t.mu.Lock()
	closed := t.closed
	t.mu.Unlock()
	if closed {
		return PQResponse{}, errors.New("transport is closed")
	}

	var in *wasmInstance
	select {
	case in = <-t.idle:
	default:
		var err error
		if in, err = t.newInstance(); err != nil {
			return PQResponse{}, err
		}
	}

	status, err := in.call(ctx, frame)
	if err != nil {
		_ = in.mod.Close(context.Background()) // never reuse after a trap or timeout
		return PQResponse{}, err
	}
	select {
	case t.idle <- in:
	default:
		_ = in.mod.Close(context.Background())
	}
	return PQResponse{Status: status, ImageHash: t.imageHash}, nil
}

func (in *wasmInstance) call(ctx context.Context, frame []byte) (Status, error) {
	res, err := in.alloc.Call(ctx, uint64(len(frame)))
	if err != nil {
		return 0, fmt.Errorf("guest alloc: %w", err)
	}
	ptr := uint32(res[0])
	if !in.mod.Memory().Write(ptr, frame) {
		return 0, errors.New("guest memory write out of range")
	}
	out, err := in.verify.Call(ctx, uint64(ptr), uint64(len(frame)))
	if err != nil {
		return 0, fmt.Errorf("guest verify: %w", err)
	}
	if _, err := in.free.Call(ctx, uint64(ptr), uint64(len(frame))); err != nil {
		return 0, fmt.Errorf("guest dealloc: %w", err)
	}
	if out[0] > uint64(StatusMalformed) {
		return 0, fmt.Errorf("guest refused the request (status %d)", out[0])
	}
	return Status(out[0]), nil
}

// MemoryBytes reports the current size of a warm instance's memory (diagnostic).
func (t *WasmTransport) MemoryBytes() uint32 {
	select {
	case in := <-t.idle:
		n := in.mod.Memory().Size()
		t.idle <- in
		return n
	default:
		return 0
	}
}

func (t *WasmTransport) Close() error {
	t.mu.Lock()
	t.closed = true
	t.mu.Unlock()
	return t.rt.Close(context.Background())
}
