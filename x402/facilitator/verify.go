package facilitator

import (
	"context"
	"encoding/base64"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// Invalid-reason codes returned in VerifyResponse.InvalidReason.
const (
	ReasonUnsupportedScheme    = "unsupported_scheme"
	ReasonUnsupportedNetwork   = "unsupported_network"
	ReasonUnsupportedAsset     = "unsupported_asset"
	ReasonUnsupportedTransport = "unsupported_transport"
	ReasonMalformedPayload     = "malformed_payload"
	ReasonUnexpectedMessages   = "unexpected_messages"
	ReasonRecipientMismatch    = "recipient_mismatch"
	ReasonAmountMismatch       = "amount_mismatch"
	ReasonKeyTypeNotAllowed    = "key_type_not_allowed"
	ReasonNotUnordered         = "not_unordered"
	ReasonBadSequence          = "sequence_must_be_zero"
	ReasonExpiryTooSoon        = "expiry_too_soon"
	ReasonExpiryTooFar         = "expiry_too_far"
	ReasonMemoMismatch         = "memo_mismatch"
	ReasonPayToModuleAccount   = "payto_is_module_account"
	ReasonFeeFieldsNotAllowed  = "fee_fields_not_allowed"
	ReasonBadSignature         = "invalid_signature"
	ReasonInsufficientFunds    = "insufficient_funds"
	ReasonSimulationFailed     = "simulation_failed"
	ReasonInternal             = "internal_error"
)

// Policy holds the facilitator's fixed configuration.
type Policy struct {
	Network string // e.g. "cosmos:my-testnet-1"
	Asset   string // base denom

	// AllowedKeyTypes are pubkey type URLs the chain accepts for payments.
	// Classic and PQ keys coexist: list both, and remove classic on phase-out.
	AllowedKeyTypes map[string]bool

	// MinRemaining is the least time that must remain before the tx expires.
	// MaxWindow is the most. Keep MaxWindow below the chain's unordered-tx
	// limit (10 minutes by default in the SDK).
	MinRemaining time.Duration
	MaxWindow    time.Duration

	MemoPrefix string // e.g. "x402:"

	// SponsorAddr is the address allowed as FeePayer when FeeMode is
	// "sponsored". Empty means the payer pays fees and no fee fields are allowed.
	SponsorAddr string
	// MinFeeBalance is added to the amount when the payer pays fees.
	MinFeeBalance *big.Int
}

// Verifier performs the spec's verification steps.
type Verifier struct {
	Policy Policy
	Parser TxParser
	Sig    SignatureVerifier
	Chain  ChainReader
	Now    func() time.Time // injectable clock; defaults to time.Now
}

func (v *Verifier) now() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

func invalid(reason string) VerifyResponse {
	return VerifyResponse{IsValid: false, InvalidReason: reason}
}

// Verify runs every normative check. It does not broadcast anything.
func (v *Verifier) Verify(ctx context.Context, req VerifyRequest) VerifyResponse {
	_, resp := v.verify(ctx, req)
	return resp
}

func (v *Verifier) verify(ctx context.Context, req VerifyRequest) (*ParsedTx, VerifyResponse) {
	pr, pp := req.PaymentRequirements, req.PaymentPayload

	// 1. Scheme / network / asset must match what this facilitator supports.
	if pr.Scheme != SchemeExact || pp.Scheme != SchemeExact {
		return nil, invalid(ReasonUnsupportedScheme)
	}
	if pr.Network != v.Policy.Network || pp.Network != v.Policy.Network {
		return nil, invalid(ReasonUnsupportedNetwork)
	}
	if pr.Asset != v.Policy.Asset {
		return nil, invalid(ReasonUnsupportedAsset)
	}

	// 2. Transport: only inline is implemented.
	if pp.Payload.Transport != TransportInline || pp.Payload.Tx == "" {
		return nil, invalid(ReasonUnsupportedTransport)
	}

	// 3. Decode.
	raw, err := base64.StdEncoding.DecodeString(pp.Payload.Tx)
	if err != nil || len(raw) == 0 {
		return nil, invalid(ReasonMalformedPayload)
	}
	tx, err := v.Parser.Parse(raw)
	if err != nil || tx == nil {
		return nil, invalid(ReasonMalformedPayload)
	}

	// 4. Exactly one MsgSend, one signer, and nothing else.
	if tx.MsgCount != 1 || tx.MsgTypeURL != MsgSendTypeURL || tx.SignerCount != 1 {
		return nil, invalid(ReasonUnexpectedMessages)
	}
	if tx.From != tx.Signer {
		return nil, invalid(ReasonUnexpectedMessages)
	}

	// 5. Recipient and amount: "exact" means exactly what was required.
	if tx.To != pr.PayTo {
		return nil, invalid(ReasonRecipientMismatch)
	}
	want, ok := new(big.Int).SetString(pr.Amount, 10)
	if !ok || want.Sign() <= 0 || tx.Amount == nil || tx.Denom != pr.Asset || tx.Amount.Cmp(want) != 0 {
		return nil, invalid(ReasonAmountMismatch)
	}

	// 6. Key type: payload claim, tx reality and policy must agree.
	if tx.SignerKeyType != pp.Payload.KeyType || !v.Policy.AllowedKeyTypes[tx.SignerKeyType] {
		return nil, invalid(ReasonKeyTypeNotAllowed)
	}
	if len(pr.Extra.KeyTypes) > 0 && !contains(pr.Extra.KeyTypes, tx.SignerKeyType) {
		return nil, invalid(ReasonKeyTypeNotAllowed)
	}

	// 7. Unordered-tx rules and the expiry window.
	if !tx.Unordered {
		return nil, invalid(ReasonNotUnordered)
	}
	if tx.Sequence != 0 {
		return nil, invalid(ReasonBadSequence)
	}
	now := v.now()
	remaining := tx.TimeoutTimestamp.Sub(now)
	if remaining < v.Policy.MinRemaining {
		return nil, invalid(ReasonExpiryTooSoon)
	}
	maxWindow := v.Policy.MaxWindow
	if pr.MaxTimeoutSeconds > 0 {
		if d := time.Duration(pr.MaxTimeoutSeconds) * time.Second; d < maxWindow {
			maxWindow = d
		}
	}
	if remaining > maxWindow {
		return nil, invalid(ReasonExpiryTooFar)
	}

	// 8. Memo binds the payment to this request.
	if !strings.HasPrefix(tx.Memo, v.Policy.MemoPrefix) {
		return nil, invalid(ReasonMemoMismatch)
	}
	if pr.Extra.PaymentID != "" && strings.TrimPrefix(tx.Memo, v.Policy.MemoPrefix) != pr.Extra.PaymentID {
		return nil, invalid(ReasonMemoMismatch)
	}

	// 9. Fee fields. Payer-pays: none allowed. Sponsored: only our sponsor as payer.
	if v.Policy.SponsorAddr == "" {
		if tx.FeePayer != "" || tx.FeeGranter != "" {
			return nil, invalid(ReasonFeeFieldsNotAllowed)
		}
	} else if (tx.FeePayer != "" && tx.FeePayer != v.Policy.SponsorAddr) ||
		(tx.FeeGranter != "" && tx.FeeGranter != v.Policy.SponsorAddr) {
		return nil, invalid(ReasonFeeFieldsNotAllowed)
	}

	// 10. payTo must not be a module account. Bank's blocked list is narrower
	// than the set of module accounts (gov, ibc transfer, ica, ...).
	isMod, err := v.Chain.IsModuleAccount(ctx, pr.PayTo)
	if err != nil {
		return nil, invalid(ReasonInternal)
	}
	if isMod {
		return nil, invalid(ReasonPayToModuleAccount)
	}

	// 11. Signature (NOT Simulate: Simulate skips signature checks).
	if err := v.Sig.Verify(ctx, tx); err != nil {
		return nil, invalid(ReasonBadSignature)
	}

	// 12. Balance, including fees when the payer pays them.
	bal, err := v.Chain.Balance(ctx, tx.From, pr.Asset)
	if err != nil {
		return nil, invalid(ReasonInternal)
	}
	need := new(big.Int).Set(want)
	if v.Policy.SponsorAddr == "" && v.Policy.MinFeeBalance != nil {
		need.Add(need, v.Policy.MinFeeBalance)
	}
	if bal.Cmp(need) < 0 {
		return nil, invalid(ReasonInsufficientFunds)
	}

	// 13. Simulation: messages would execute. Gas is also returned for pricing.
	if _, err := v.Chain.Simulate(ctx, tx.Raw); err != nil {
		return nil, invalid(ReasonSimulationFailed)
	}

	return tx, VerifyResponse{IsValid: true, Payer: tx.From}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// paymentKey identifies a payment for idempotency: network, payer and the
// signed bytes. Two submissions of the same signed tx map to the same key.
func paymentKey(network string, tx *ParsedTx) string {
	return fmt.Sprintf("%s|%s|%x", network, tx.From, hashBytes(tx.Raw))
}
