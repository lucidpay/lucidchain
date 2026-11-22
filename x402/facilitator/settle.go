package facilitator

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"sync"
	"time"
)

// Settle-time reason codes.
const (
	ReasonSettlementInProgress = "settlement_in_progress"
	ReasonBroadcastFailed      = "broadcast_failed"
	ReasonTxFailed             = "tx_failed"
	ReasonConfirmTimeout       = "confirmation_timeout"
)

func hashBytes(b []byte) []byte { h := sha256.Sum256(b); return h[:] }

// TxHash is the CometBFT transaction hash of raw tx bytes (uppercase hex SHA-256).
func TxHash(raw []byte) string { return strings.ToUpper(hex.EncodeToString(hashBytes(raw))) }

// Settler verifies, broadcasts and confirms a payment exactly once.
type Settler struct {
	Verifier       *Verifier
	Broadcaster    Broadcaster
	Store          PaymentStore
	PollInterval   time.Duration // default 500ms
	ConfirmTimeout time.Duration // default 15s
}

// Settle is idempotent: the same signed tx always maps to the same record, and
// a second call returns the first call's outcome instead of paying twice. The
// chain also rejects a repeated (sender, timeout_timestamp) pair, which is a
// second line of defence.
func (s *Settler) Settle(ctx context.Context, req SettleRequest) SettleResponse {
	network := req.PaymentRequirements.Network
	fail := func(reason, payer string) SettleResponse {
		return SettleResponse{Success: false, ErrorReason: reason, Payer: payer, Network: network}
	}

	// Cheap decode first so we can claim before doing any chain work.
	raw, err := base64.StdEncoding.DecodeString(req.PaymentPayload.Payload.Tx)
	if err != nil || len(raw) == 0 {
		return fail(ReasonMalformedPayload, "")
	}
	tx, err := s.Verifier.Parser.Parse(raw)
	if err != nil || tx == nil {
		return fail(ReasonMalformedPayload, "")
	}
	key := paymentKey(network, tx)

	rec, claimed, err := s.Store.Claim(ctx, key)
	if err != nil {
		return fail(ReasonInternal, tx.From)
	}
	if !claimed {
		if rec.Done {
			return rec.Response
		}
		return fail(ReasonSettlementInProgress, tx.From)
	}

	// From here we own the claim: always either Complete or Release it.
	if _, vr := s.Verifier.verify(ctx, req); !vr.IsValid {
		_ = s.Store.Release(ctx, key)
		return fail(vr.InvalidReason, tx.From)
	}

	hash := TxHash(raw)
	if _, err := s.Broadcaster.Broadcast(ctx, raw); err != nil {
		// The error may be ambiguous (timeout after acceptance). Re-broadcasting
		// identical bytes is safe, but check whether it already landed first.
		if found, code, _, qerr := s.Broadcaster.TxResult(ctx, hash); qerr == nil && found && code == 0 {
			return s.finish(ctx, key, SettleResponse{Success: true, Payer: tx.From, Transaction: hash, Network: network})
		}
		_ = s.Store.Release(ctx, key)
		return fail(ReasonBroadcastFailed, tx.From)
	}

	interval, timeout := s.PollInterval, s.ConfirmTimeout
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(interval)
	defer tick.Stop()

	for {
		found, code, _, err := s.Broadcaster.TxResult(ctx, hash)
		if err == nil && found {
			if code != 0 {
				return s.finish(ctx, key, SettleResponse{Success: false, ErrorReason: ReasonTxFailed, Payer: tx.From, Transaction: hash, Network: network})
			}
			return s.finish(ctx, key, SettleResponse{Success: true, Payer: tx.From, Transaction: hash, Network: network})
		}
		select {
		case <-ctx.Done():
			// Leave the claim in place: the tx may still land. A retry returns
			// "in progress" until an operator or reconciler completes it.
			return fail(ReasonConfirmTimeout, tx.From)
		case <-deadline.C:
			return fail(ReasonConfirmTimeout, tx.From)
		case <-tick.C:
		}
	}
}

func (s *Settler) finish(ctx context.Context, key string, resp SettleResponse) SettleResponse {
	_ = s.Store.Complete(ctx, key, resp)
	return resp
}

// MemoryStore is an in-process PaymentStore for tests and single-node use.
// Production needs a durable store (SQL/Redis) so claims survive restarts.
type MemoryStore struct {
	mu   sync.Mutex
	recs map[string]SettleRecord
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{recs: map[string]SettleRecord{}} }

func (m *MemoryStore) Claim(_ context.Context, key string) (SettleRecord, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.recs[key]; ok {
		return r, false, nil
	}
	m.recs[key] = SettleRecord{}
	return SettleRecord{}, true, nil
}

func (m *MemoryStore) Complete(_ context.Context, key string, resp SettleResponse) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recs[key] = SettleRecord{Done: true, Response: resp}
	return nil
}

func (m *MemoryStore) Release(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.recs[key]; ok && !r.Done {
		delete(m.recs, key)
	}
	return nil
}
