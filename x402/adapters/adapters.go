// Package adapters holds the Cosmos SDK / node implementations of the ports
// defined in x402/facilitator.
//
// EVERY adapter here is currently a STUB. They all fail closed: none of them can
// ever approve a payment, so an unfinished facilitator rejects everything
// instead of accepting forged payments.
//
// When you implement an adapter, write it against your exact SDK version
// (v0.55.x) and your custom message types, and add tests with real encoded
// transactions from your chain (classic and PQ signed). The notes on each stub
// say what to use; they are from memory of the SDK API and are NOT verified
// against v0.55, so check them against the code.
//
// Once ALL of the adapters below are real, set Implemented to true. cmd/x402facilitator
// refuses to start while it is false (unless started with -allow-stubs).
package adapters

import (
	"context"
	"errors"
	"math/big"

	"github.com/lucidpay/lucidchain/x402/facilitator"
)

// Implemented must stay false until every adapter in this package is real.
const Implemented = false

// ErrNotImplemented is returned by every stub.
var ErrNotImplemented = errors.New("x402 adapter not implemented")

// Compile-time checks that the stubs satisfy the facilitator ports.
var (
	_ facilitator.TxParser          = (*SDKTxParser)(nil)
	_ facilitator.SignatureVerifier = (*SDKSignatureVerifier)(nil)
	_ facilitator.ChainReader       = (*GRPCChain)(nil)
	_ facilitator.Broadcaster       = (*NodeBroadcaster)(nil)
)

// ---------------------------------------------------------------------------
// TxParser
// ---------------------------------------------------------------------------

// SDKTxParser decodes TxRaw bytes into a facilitator.ParsedTx.
//
// Implementation notes:
//   - Decode with the app's TxConfig: app.TxConfig().TxDecoder() (reuse the
//     chain's codec so your custom message types and PQ key types are known).
//   - Fill ParsedTx.Raw with the original bytes, unchanged.
//   - MsgCount/MsgTypeURL from the tx messages; for a MsgSend fill From, To,
//     Denom, Amount (reject multi-coin sends: leave Amount nil).
//   - Signer, SignerCount and SignerKeyType (the pubkey's type URL) from the
//     tx's signers and signer infos.
//   - Memo, Unordered, Sequence and TimeoutTimestamp from the tx body/auth info.
//     Check how v0.55 exposes the unordered fields (an optional tx interface).
//   - FeePayer/FeeGranter from the tx fee.
//   - Return an error for anything you cannot represent faithfully; never guess.
type SDKTxParser struct {
	// TODO: inject the chain's client.TxConfig (or its TxDecoder).
}

func NewSDKTxParser() *SDKTxParser { return &SDKTxParser{} }

func (*SDKTxParser) Parse([]byte) (*facilitator.ParsedTx, error) { return nil, ErrNotImplemented }

// ---------------------------------------------------------------------------
// SignatureVerifier
// ---------------------------------------------------------------------------

// SDKSignatureVerifier verifies the payer's signature. SECURITY CRITICAL.
//
// Do NOT use the Simulate RPC for this: the SDK skips signature verification
// when simulating.
//
// Implementation notes (recommended approach, local verification):
//   - Look up the payer's account number (and stored pubkey) over gRPC.
//   - Rebuild the sign doc / signer data for the sign mode the payer used.
//   - Verify with the SDK's sign-mode handler through the PubKey interface, so
//     the same code covers classic and PQ keys.
//   - Make sure the account's sequence/unordered rules and chain-id are part of
//     what is verified, and test with tampered bytes, wrong chain-id and wrong
//     account number (all must fail).
//
// Alternative: a node-side CheckTx, only after confirming on your node that it
// does not mutate check state (it normally does, which can break the later
// broadcast).
type SDKSignatureVerifier struct{}

func NewSDKSignatureVerifier() *SDKSignatureVerifier { return &SDKSignatureVerifier{} }

// Verify ALWAYS fails while this is a stub. It must never return nil without
// real cryptographic verification.
func (*SDKSignatureVerifier) Verify(context.Context, *facilitator.ParsedTx) error {
	return ErrNotImplemented
}

// ---------------------------------------------------------------------------
// ChainReader
// ---------------------------------------------------------------------------

// GRPCChain reads chain state and simulates over the node's gRPC endpoint.
//
// Implementation notes:
//   - Balance: bank query for the account's balance of one denom.
//   - IsModuleAccount: derive the module-account addresses (from the module
//     names in app_config.go, e.g. with the SDK's module-address helper) and/or
//     query the auth module for module accounts; return true for ANY module
//     account, including gov, IBC transfer, ICA and the key-rotation pool, not
//     only the bank blocked list. Do not reuse app.BlockedAddresses() as it
//     returns module names, not addresses.
//   - Simulate: the tx service's Simulate RPC. It checks execution and gas but
//     NOT signatures.
//
// Keep gRPC on localhost and run this process next to a dedicated node.
type GRPCChain struct {
	// TODO: gRPC client conn to the node (e.g. localhost:9090).
	Addr string
}

func NewGRPCChain(addr string) *GRPCChain { return &GRPCChain{Addr: addr} }

func (*GRPCChain) Balance(context.Context, string, string) (*big.Int, error) {
	return nil, ErrNotImplemented
}

func (*GRPCChain) IsModuleAccount(context.Context, string) (bool, error) {
	// Fail closed: the verifier treats an error as internal_error and rejects.
	return false, ErrNotImplemented
}

func (*GRPCChain) Simulate(context.Context, []byte) (uint64, error) {
	return 0, ErrNotImplemented
}

// ---------------------------------------------------------------------------
// Broadcaster
// ---------------------------------------------------------------------------

// NodeBroadcaster submits transactions and reads their results.
//
// Implementation notes:
//   - Broadcast: the tx service's BroadcastTx in SYNC mode; a non-zero CheckTx
//     code must become an error. Return the tx hash. In "sponsored" fee mode
//     (not implemented yet) this adapter would also have to co-sign with the
//     sponsor key; keep that key in a keyring backend or KMS, never in config.
//   - TxResult: the tx service's GetTx by hash; "not found" is found=false, not
//     an error.
type NodeBroadcaster struct {
	// TODO: gRPC client conn, same node as GRPCChain.
	Addr string
}

func NewNodeBroadcaster(addr string) *NodeBroadcaster { return &NodeBroadcaster{Addr: addr} }

func (*NodeBroadcaster) Broadcast(context.Context, []byte) (string, error) {
	return "", ErrNotImplemented
}

func (*NodeBroadcaster) TxResult(context.Context, string) (bool, uint32, string, error) {
	return false, 0, "", ErrNotImplemented
}
