package verifiers

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"errors"
	"fmt"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

//go:embed wasm/embed/pq_verifier.wasm
var pqVerifierWasm []byte

// The guest imports nothing (no WASI, no host functions) and exports exactly
// memory, alloc and verify_frame. It has no _start or _initialize.
//
// Design rules, each one closing a consensus hazard:
//   - Fresh instance per call. Wasm memory only grows, so a reused instance
//     makes "does this request hit the page limit" depend on history, and a
//     restarted node could disagree with a long-running one at the margin.
//   - Queueing is NOT part of the timeout. Only execution is timed, so a
//     busy validator cannot fault on valid traffic.
//   - Every error (trap, timeout, OOM, bad exit status) is a fault, never a
//     verdict. The caller maps it to types.ErrVerifierFault.
//   - The embedded module must match the pinned hash, checked here, so no
//     construction path can skip it.

// WasmImageHash is sha256 of the embedded guest.
func WasmImageHash() [32]byte { return sha256.Sum256(pqVerifierWasm) }

// WasmTransport is a PQTransport that runs the verifier in-process under wazero.
type WasmTransport struct {
	rt       wazero.Runtime
	compiled wazero.CompiledModule
	timeout  time.Duration // execution only, per call
	slots    chan struct{} // bounds concurrent instances (and so memory)
	closed   atomic.Bool
}

var _ PQTransport = (*WasmTransport)(nil)

// NewWasmTransport compiles the embedded guest. memoryPages caps guest memory
// (64 KiB per page); timeout bounds one execution, not the wait for a slot.
func NewWasmTransport(ctx context.Context, maxInstances int, timeout time.Duration, memoryPages uint32) (*WasmTransport, error) {
	if maxInstances < 1 || timeout <= 0 || memoryPages < 1 {
		return nil, errors.New("maxInstances, timeout and memoryPages must be positive")
	}
	if err := CheckWasmPin(); err != nil {
		return nil, err
	}
	// The interpreter is deterministic but orders of magnitude slower; a node on
	// it would fault on real proofs. Refuse instead of limping.
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return nil, fmt.Errorf("wazero compiler engine is not supported on %s", runtime.GOARCH)
	}

	cfg := wazero.NewRuntimeConfigCompiler().
		WithCoreFeatures(api.CoreFeaturesV2). // explicit: anything newer (e.g. relaxed SIMD) fails to compile
		WithCloseOnContextDone(true).
		WithMemoryLimitPages(memoryPages)
	rt := wazero.NewRuntimeWithConfig(ctx, cfg)

	compiled, err := rt.CompileModule(ctx, pqVerifierWasm)
	if err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("compile verifier: %w", err)
	}
	if err := checkShape(compiled); err != nil {
		_ = rt.Close(ctx)
		return nil, err
	}
	return &WasmTransport{
		rt: rt, compiled: compiled, timeout: timeout,
		slots: make(chan struct{}, maxInstances),
	}, nil
}

func checkShape(m wazero.CompiledModule) error {
	if n := len(m.ImportedFunctions()); n != 0 {
		return fmt.Errorf("verifier module imports %d functions, want 0", n)
	}
	if n := len(m.ImportedMemories()); n != 0 {
		return fmt.Errorf("verifier module imports %d memories, want 0", n)
	}
	fns := m.ExportedFunctions()
	for _, name := range []string{"alloc", "verify_frame"} {
		if _, ok := fns[name]; !ok {
			return fmt.Errorf("verifier module is missing export %q", name)
		}
	}
	if _, ok := m.ExportedMemories()["memory"]; !ok {
		return errors.New("verifier module does not export memory")
	}
	return nil
}

// Verify implements PQTransport. Every error is an infrastructure fault.
func (t *WasmTransport) Verify(req PQRequest) (PQResponse, error) {
	if len(req.PublicInputs) > wireMaxPublicInputBytes {
		return PQResponse{}, fmt.Errorf("public inputs are %d bytes, wire limit is %d", len(req.PublicInputs), wireMaxPublicInputBytes)
	}
	if len(req.Proof) > PQWireMaxProofBytes {
		return PQResponse{}, fmt.Errorf("proof is %d bytes, wire limit is %d", len(req.Proof), PQWireMaxProofBytes)
	}
	if t.closed.Load() {
		return PQResponse{}, errors.New("transport is closed")
	}

	// Wait for a slot with no deadline: slots are released after a bounded
	// execution, and time spent queued must not count against the request.
	t.slots <- struct{}{}
	defer func() { <-t.slots }()

	ctx, cancel := context.WithTimeout(context.Background(), t.timeout)
	defer cancel()

	mod, err := t.rt.InstantiateModule(ctx, t.compiled,
		wazero.NewModuleConfig().WithName("").WithStartFunctions())
	if err != nil {
		return PQResponse{}, fmt.Errorf("instantiate verifier: %w", err)
	}
	defer func() { _ = mod.Close(context.Background()) }() // discarded after every call

	status, err := callGuest(ctx, mod, req)
	if err != nil {
		return PQResponse{}, err
	}
	return PQResponse{Status: status}, nil
}

func callGuest(ctx context.Context, mod api.Module, req PQRequest) (Status, error) {
	total := wireHeaderLen + len(req.PublicInputs) + len(req.Proof)

	res, err := mod.ExportedFunction("alloc").Call(ctx, uint64(total))
	if err != nil {
		return 0, fmt.Errorf("guest alloc: %w", err)
	}
	ptr := uint32(res[0])
	if ptr == 0 {
		return 0, errors.New("guest alloc refused the request size")
	}

	// Write header, public inputs and proof straight into guest memory: no
	// intermediate frame buffer.
	mem := mod.Memory()
	hdr := frameHeader(req)
	pubAt := ptr + wireHeaderLen
	proofAt := pubAt + uint32(len(req.PublicInputs))
	if !mem.Write(ptr, hdr[:]) || !mem.Write(pubAt, req.PublicInputs) || !mem.Write(proofAt, req.Proof) {
		return 0, errors.New("guest memory write out of range")
	}

	out, err := mod.ExportedFunction("verify_frame").Call(ctx, uint64(ptr), uint64(total))
	if err != nil {
		return 0, fmt.Errorf("guest verify: %w", err)
	}
	if out[0] > uint64(StatusMalformed) {
		return 0, fmt.Errorf("guest refused the request (status %d)", out[0])
	}
	return Status(out[0]), nil
}

// Close makes later calls fail and releases the runtime. Calls in flight error
// out as faults, which is acceptable at shutdown.
func (t *WasmTransport) Close() error {
	t.closed.Store(true)
	return t.rt.Close(context.Background())
}
