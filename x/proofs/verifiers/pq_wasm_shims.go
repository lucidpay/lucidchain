package verifiers

import (
	"context"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

const shimModule = "wasi_snapshot_preview1"

const (
	i32         = api.ValueTypeI32
	errnoEBADF  = 8
	errnoEFAULT = 21
)

type importSig struct{ params, results []api.ValueType }

// guestImports is the complete import set the guest may have. These are not
// real WASI: each is a stub that grants no capability. Any other import, or a
// different signature, refuses to start the node.
var guestImports = map[string]importSig{
	"environ_sizes_get": {[]api.ValueType{i32, i32}, []api.ValueType{i32}},
	"environ_get":       {[]api.ValueType{i32, i32}, []api.ValueType{i32}},
	"fd_write":          {[]api.ValueType{i32, i32, i32, i32}, []api.ValueType{i32}},
	"proc_exit":         {[]api.ValueType{i32}, nil},
}

// registerGuestShims installs the stubs. They are stateless, so one host module
// serves every instance without sharing anything between requests.
func registerGuestShims(ctx context.Context, rt wazero.Runtime) error {
	b := rt.NewHostModuleBuilder(shimModule)
	fn := func(name string, f api.GoModuleFunc) {
		s := guestImports[name]
		b.NewFunctionBuilder().WithGoModuleFunction(f, s.params, s.results).Export(name)
	}

	// Empty environment.
	fn("environ_sizes_get", func(_ context.Context, m api.Module, st []uint64) {
		mem := m.Memory()
		if mem.WriteUint32Le(uint32(st[0]), 0) && mem.WriteUint32Le(uint32(st[1]), 0) {
			st[0] = 0
		} else {
			st[0] = errnoEFAULT
		}
	})
	fn("environ_get", func(_ context.Context, _ api.Module, st []uint64) { st[0] = 0 })

	// Output is discarded. Rust's stdio treats EBADF as success.
	fn("fd_write", func(_ context.Context, _ api.Module, st []uint64) { st[0] = errnoEBADF })

	// Any exit is a fault: the closed module makes the call return an error,
	// which callGuest already reports as a fault, never a verdict.
	fn("proc_exit", func(ctx context.Context, m api.Module, st []uint64) {
		_ = m.CloseWithExitCode(ctx, uint32(st[0]))
	})

	_, err := b.Instantiate(ctx)
	return err
}
