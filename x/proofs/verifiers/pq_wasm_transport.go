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

// The guest imports exactly four wasi_snapshot_preview1 functions, which the
// host answers with capability-free stubs (see pq_wasm_shims.go), and exports
// exactly memory, alloc and verify_frame. It has no _start or _initialize, and
// WithStartFunctions() ensures none would be run anyway.

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

	if err := registerGuestShims(ctx, rt); err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("register guest shims: %w", err)
	}

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
	imps := m.ImportedFunctions()
	if len(imps) != len(guestImports) {
		return fmt.Errorf("verifier module imports %d functions, want %d", len(imps), len(guestImports))
	}
	seen := make(map[string]bool, len(imps))
	for _, f := range imps {
		mod, name, _ := f.Import()
		want, ok := guestImports[name]
		if mod != shimModule || !ok || seen[name] ||
			!sameTypes(f.ParamTypes(), want.params) || !sameTypes(f.ResultTypes(), want.results) {
			return fmt.Errorf("verifier module has unexpected import %s.%s", mod, name)
		}
		seen[name] = true
	}
	if n := len(m.ImportedMemories()); n != 0 {
		return fmt.Errorf("verifier module imports %d memories, want 0", n)
	}

	// Exactly these exports: a stray _start or _initialize is a spec violation.
	fns := m.ExportedFunctions()
	if len(fns) != 2 {
		return fmt.Errorf("verifier module exports %d functions, want 2", len(fns))
	}
	for name, want := range map[string]importSig{
		"alloc":        {[]api.ValueType{i32}, []api.ValueType{i32}},
		"verify_frame": {[]api.ValueType{i32, i32}, []api.ValueType{i32}},
	} {
		f, ok := fns[name]
		if !ok {
			return fmt.Errorf("verifier module is missing export %q", name)
		}
		if !sameTypes(f.ParamTypes(), want.params) || !sameTypes(f.ResultTypes(), want.results) {
			return fmt.Errorf("verifier export %q has the wrong signature", name)
		}
	}
	mems := m.ExportedMemories()
	if _, ok := mems["memory"]; !ok || len(mems) != 1 {
		return errors.New("verifier module must export exactly one memory, named \"memory\"")
	}
	return nil
}

func sameTypes(a, b []api.ValueType) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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
