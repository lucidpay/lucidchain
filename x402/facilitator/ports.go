package x402facilitator

import (
	"context"
	"math/big"
	"time"
)

// ParsedTx is the facilitator's SDK-independent view of a decoded transaction.
// Your SDK adapter (TxParser) fills this from a decoded v0.55 tx.
type ParsedTx struct {
	Raw []byte // TxRaw bytes exactly as the payer signed them

	MsgCount   int
	MsgTypeURL string // type URL of the first message

	// Fields of the single MsgSend (valid when MsgTypeURL == MsgSendTypeURL).
	From   string
	To     string
	Denom  string
	Amount *big.Int

	Signer        string // bech32 address of the (first) signer
	SignerKeyType string // pubkey type URL, e.g. "/cosmos.crypto.secp256k1.PubKey"
	SignerCount   int

	Memo             string
	Unordered        bool
	Sequence         uint64
	TimeoutTimestamp time.Time

	FeePayer   string // empty if unset
	FeeGranter string // empty if unset
}

// TxParser decodes TxRaw bytes (SDK adapter).
type TxParser interface {
	Parse(raw []byte) (*ParsedTx, error)
}

// SignatureVerifier checks the payer's signature (SDK adapter).
//
// IMPORTANT: do not use Simulate for this. The SDK skips signature
// verification when simulating. Verify against the sign doc using the SDK's
// sign-mode handlers (works for any key type through the PubKey interface), or
// run a read-only check you have confirmed does not mutate node check-state.
type SignatureVerifier interface {
	Verify(ctx context.Context, tx *ParsedTx) error
}

// ChainReader reads chain state and simulates execution (gRPC adapter).
type ChainReader interface {
	Balance(ctx context.Context, addr, denom string) (*big.Int, error)
	IsModuleAccount(ctx context.Context, addr string) (bool, error)
	// Simulate runs the tx through the node's Simulate RPC. It checks that the
	// messages would execute and returns gas used. It does NOT verify signatures.
	Simulate(ctx context.Context, raw []byte) (gasUsed uint64, err error)
}

// Broadcaster submits transactions and looks up results (RPC/gRPC adapter).
type Broadcaster interface {
	// Broadcast submits in sync mode and returns the tx hash. It returns an
	// error if CheckTx rejected the transaction.
	Broadcast(ctx context.Context, raw []byte) (txHash string, err error)
	// TxResult looks a tx up by hash. found is false until it is in a block.
	TxResult(ctx context.Context, txHash string) (found bool, code uint32, rawLog string, err error)
}

// SettleRecord is the stored outcome of a settlement attempt.
type SettleRecord struct {
	Done     bool
	Response SettleResponse
}

// PaymentStore makes settlement idempotent across retries and concurrent calls.
type PaymentStore interface {
	// Claim atomically reserves key. If it was already claimed, claimed is
	// false and rec holds the existing record (Done may still be false).
	Claim(ctx context.Context, key string) (rec SettleRecord, claimed bool, err error)
	Complete(ctx context.Context, key string, resp SettleResponse) error
	// Release frees a claim after a failure that is safe to retry.
	Release(ctx context.Context, key string) error
}
