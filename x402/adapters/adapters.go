// Package adapters holds the Cosmos SDK / node implementations of the ports
// defined in x402/facilitator.

package adapters

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	"cosmossdk.io/core/address"

	"github.com/cosmos/cosmos-sdk/client"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
	signingtypes "github.com/cosmos/cosmos-sdk/types/tx/signing"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	txsigning "github.com/cosmos/cosmos-sdk/x/tx/signing"

	"github.com/lucidpay/lucidchain/x402/facilitator"
)

// Implemented: flip to true only after the tests listed at the bottom pass
// against real transactions from your chain (classic AND ML-DSA-65 signed).
const Implemented = false

// ErrNotImplemented is kept for callers/tests that still reference it.
var ErrNotImplemented = errors.New("x402 adapter not implemented")

var (
	_ facilitator.TxParser          = (*SDKTxParser)(nil)
	_ facilitator.SignatureVerifier = (*SDKSignatureVerifier)(nil)
	_ facilitator.ChainReader       = (*GRPCChain)(nil)
	_ facilitator.Broadcaster       = (*NodeBroadcaster)(nil)
)

func dial(addr string) (*grpc.ClientConn, error) {
	// Plaintext is acceptable ONLY because gRPC must stay on localhost.
	return grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
}

// decodeTx decodes raw into an sdk.Tx and rejects shapes that decode
// successfully but make later SDK accessors panic. In v0.55.0 an empty tx or
// one without AuthInfo.Fee decodes fine, then Tx.GetSigners dereferences the
// nil Fee (types/tx/types.go). Payment txs always carry a fee, so require it.
func decodeTx(txConfig client.TxConfig, raw []byte) (tx sdk.Tx, err error) {
	defer func() {
		if r := recover(); r != nil {
			tx, err = nil, fmt.Errorf("decode panicked: %v", r)
		}
	}()
	if len(raw) == 0 {
		return nil, errors.New("empty tx")
	}
	var txRaw sdktx.TxRaw
	if err := txRaw.Unmarshal(raw); err != nil {
		return nil, fmt.Errorf("unmarshal TxRaw: %w", err)
	}
	var authInfo sdktx.AuthInfo
	if err := authInfo.Unmarshal(txRaw.AuthInfoBytes); err != nil {
		return nil, fmt.Errorf("unmarshal AuthInfo: %w", err)
	}
	if authInfo.Fee == nil {
		return nil, errors.New("tx has no fee")
	}
	return txConfig.TxDecoder()(raw)
}

// ---------------------------------------------------------------------------
// TxParser
// ---------------------------------------------------------------------------

// SDKTxParser decodes TxRaw bytes into a facilitator.ParsedTx.
type SDKTxParser struct {
	txConfig client.TxConfig
	addrCdc  address.Codec // e.g. interfaceRegistry.SigningContext().AddressCodec()
}

func NewSDKTxParser(txConfig client.TxConfig, addrCdc address.Codec) *SDKTxParser {
	return &SDKTxParser{txConfig: txConfig, addrCdc: addrCdc}
}

func (p *SDKTxParser) Parse(raw []byte) (res *facilitator.ParsedTx, err error) {
	// Hostile input must produce an error, never a panic.
	defer func() {
		if r := recover(); r != nil {
			res, err = nil, fmt.Errorf("parse panicked on malformed tx: %v", r)
		}
	}()
	tx, err := decodeTx(p.txConfig, raw)
	if err != nil {
		return nil, fmt.Errorf("decode tx: %w", err)
	}

	msgs := tx.GetMsgs() // VERIFIED: sdk.HasMsgs.GetMsgs() []proto.Message (gogoproto)
	out := &facilitator.ParsedTx{
		Raw:      raw, // unchanged
		MsgCount: len(msgs),
	}
	// MsgTypeURL and the MsgSend fields describe the FIRST message; the
	// verifier must still reject MsgCount != 1.
	if len(msgs) > 0 {
		out.MsgTypeURL = sdk.MsgTypeURL(msgs[0])
		if send, ok := msgs[0].(*banktypes.MsgSend); ok {
			out.From, out.To = send.FromAddress, send.ToAddress
			// Multi-coin sends: leave Denom/Amount unset so the verifier rejects.
			if len(send.Amount) == 1 && send.Amount[0].Amount.IsPositive() {
				out.Denom = send.Amount[0].Denom
				out.Amount = send.Amount[0].Amount.BigInt()
			}
		}
	}

	sigTx, ok := tx.(authsigning.SigVerifiableTx) // VERIFIED
	if !ok {
		return nil, fmt.Errorf("tx %T is not signature-verifiable", tx)
	}
	signers, err := sigTx.GetSigners()
	if err != nil {
		return nil, fmt.Errorf("get signers: %w", err)
	}
	out.SignerCount = len(signers)
	sigs, err := sigTx.GetSignaturesV2()
	if err != nil {
		return nil, fmt.Errorf("get signatures: %w", err)
	}
	// Fill the FIRST signer even when there are several; the verifier rejects
	// SignerCount != 1.
	if len(signers) > 0 {
		if out.Signer, err = p.addrCdc.BytesToString(signers[0]); err != nil {
			return nil, fmt.Errorf("signer address: %w", err)
		}
	}
	if len(sigs) > 0 {
		out.Sequence = sigs[0].Sequence
		if pk := sigs[0].PubKey; pk != nil {
			out.SignerKeyType = sdk.MsgTypeURL(pk) // e.g. /cosmos.crypto.mldsa65.PubKey
		}
	}

	if m, ok := tx.(sdk.TxWithMemo); ok {
		out.Memo = m.GetMemo()
	}
	// VERIFIED: TxWithUnordered embeds TxWithTimeoutTimeStamp.
	if u, ok := tx.(sdk.TxWithUnordered); ok {
		out.Unordered = u.GetUnordered()
		out.TimeoutTimestamp = u.GetTimeoutTimeStamp()
	}

	// FeePayer/FeeGranter: read the EXPLICIT values from the signed AuthInfo.
	// Do not use sdk.FeeTx.FeePayer(): VERIFIED in x/auth/tx/builder.go that it
	// defaults to the first signer when unset (never empty) and panics on a
	// malformed payer string.
	var txRaw sdktx.TxRaw
	if err := txRaw.Unmarshal(raw); err != nil {
		return nil, fmt.Errorf("unmarshal TxRaw: %w", err)
	}
	var authInfo sdktx.AuthInfo
	if err := authInfo.Unmarshal(txRaw.AuthInfoBytes); err != nil {
		return nil, fmt.Errorf("unmarshal AuthInfo: %w", err)
	}
	if authInfo.Fee != nil {
		for _, f := range []struct {
			in  string
			out *string
		}{{authInfo.Fee.Payer, &out.FeePayer}, {authInfo.Fee.Granter, &out.FeeGranter}} {
			if f.in == "" {
				continue // stays empty = unset
			}
			if _, err := p.addrCdc.StringToBytes(f.in); err != nil {
				return nil, fmt.Errorf("invalid fee payer/granter %q: %w", f.in, err)
			}
			*f.out = f.in
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// SignatureVerifier  (SECURITY CRITICAL)
// ---------------------------------------------------------------------------

// SDKSignatureVerifier verifies the payer's signature locally, mirroring
// SigVerificationDecorator + SetPubKeyDecorator in x/auth/ante/sigverify.go.
type SDKSignatureVerifier struct {
	txConfig client.TxConfig
	registry codectypes.InterfaceRegistry
	addrCdc  address.Codec
	auth     authtypes.QueryClient
	conn     *grpc.ClientConn
	chainID  string
}

func NewSDKSignatureVerifier(txConfig client.TxConfig, registry codectypes.InterfaceRegistry, addrCdc address.Codec, grpcAddr, chainID string) (*SDKSignatureVerifier, error) {
	if chainID == "" {
		return nil, errors.New("chainID required")
	}
	conn, err := dial(grpcAddr)
	if err != nil {
		return nil, err
	}
	return &SDKSignatureVerifier{
		txConfig: txConfig, registry: registry, addrCdc: addrCdc, chainID: chainID,
		auth: authtypes.NewQueryClient(conn), conn: conn,
	}, nil
}

func (v *SDKSignatureVerifier) Close() error { return v.conn.Close() }

// Verify never returns nil without a successful cryptographic verification.
func (v *SDKSignatureVerifier) Verify(ctx context.Context, p *facilitator.ParsedTx) (err error) {
	// Any panic inside SDK code on malformed input is a verification failure.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("verify panicked on malformed tx: %v", r)
		}
	}()
	if p == nil || len(p.Raw) == 0 {
		return errors.New("no tx")
	}
	// Re-decode from the raw bytes: never trust the parsed summary for crypto.
	tx, err := decodeTx(v.txConfig, p.Raw)
	if err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	sigTx, ok := tx.(authsigning.SigVerifiableTx)
	if !ok {
		return errors.New("tx not signature-verifiable")
	}
	adaptable, ok := tx.(authsigning.V2AdaptableTx) // VERIFIED
	if !ok {
		return fmt.Errorf("tx %T not V2AdaptableTx", tx)
	}
	signers, err := sigTx.GetSigners()
	if err != nil || len(signers) != 1 {
		return errors.New("exactly one signer required")
	}
	sigs, err := sigTx.GetSignaturesV2()
	if err != nil || len(sigs) != 1 {
		return errors.New("exactly one signature required")
	}
	sig := sigs[0]

	// Single signatures only; allow only the modes you actually support.
	// SIGN_MODE_TEXTUAL no longer exists in v0.55 (removed, #26456).
	single, ok := sig.Data.(*signingtypes.SingleSignatureData)
	if !ok {
		return errors.New("multisig not supported")
	}
	switch single.SignMode {
	case signingtypes.SignMode_SIGN_MODE_DIRECT, signingtypes.SignMode_SIGN_MODE_LEGACY_AMINO_JSON:
	default:
		return fmt.Errorf("sign mode %s not allowed", single.SignMode)
	}

	signerAddr, err := v.addrCdc.BytesToString(signers[0])
	if err != nil {
		return err
	}
	// The caller's summary must describe the same tx we are verifying.
	if p.Signer != signerAddr {
		return errors.New("parsed signer does not match raw tx")
	}

	// Account state over gRPC. AccountInfo (BaseAccount) VERIFIED to exist.
	resp, err := v.auth.AccountInfo(ctx, &authtypes.QueryAccountInfoRequest{Address: signerAddr})
	if err != nil {
		return fmt.Errorf("account lookup: %w", err) // includes NotFound: unknown account fails closed
	}
	info := resp.Info
	if info == nil {
		return errors.New("empty account info")
	}

	// Pubkey selection == ante: stored key wins; else the tx's key must hash to
	// the signer address (SetPubKeyDecorator).
	var pubKey cryptotypes.PubKey
	if info.PubKey != nil {
		if err := v.registry.UnpackAny(info.PubKey, &pubKey); err != nil {
			return fmt.Errorf("unpack stored pubkey: %w", err)
		}
		if sig.PubKey != nil && !sig.PubKey.Equals(pubKey) {
			return errors.New("tx pubkey differs from account pubkey")
		}
	} else {
		pubKey = sig.PubKey
	}
	if pubKey == nil {
		return errors.New("no pubkey available")
	}
	if !bytes.Equal(pubKey.Address(), signers[0]) {
		return errors.New("pubkey does not match signer address")
	}

	// Sequence rules == ante.
	unordered := false
	if u, ok := tx.(sdk.TxWithUnordered); ok && u.GetUnordered() {
		unordered = true
		if sig.Sequence > 0 {
			return errors.New("sequence not allowed for unordered tx")
		}
	}
	if !unordered && sig.Sequence != info.Sequence {
		return fmt.Errorf("sequence mismatch: account %d, tx %d", info.Sequence, sig.Sequence)
	}

	anyPk, err := codectypes.NewAnyWithValue(pubKey) // VERIFIED
	if err != nil {
		return err
	}
	signerData := txsigning.SignerData{
		Address:       signerAddr,
		ChainID:       v.chainID, // OUR configured chain-id, never the tx's
		AccountNumber: info.AccountNumber,
		Sequence:      sig.Sequence,
		PubKey:        &anypb.Any{TypeUrl: anyPk.TypeUrl, Value: anyPk.Value},
	}
	// VERIFIED signature of authsigning.VerifySignature at v0.55.0.
	if err := authsigning.VerifySignature(ctx, pubKey, signerData, sig.Data,
		v.txConfig.SignModeHandler(), adaptable.GetSigningTxData()); err != nil {
		return fmt.Errorf("signature invalid: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// ChainReader
// ---------------------------------------------------------------------------

type GRPCChain struct {
	Addr     string
	conn     *grpc.ClientConn
	bank     banktypes.QueryClient
	auth     authtypes.QueryClient
	tx       sdktx.ServiceClient
	registry codectypes.InterfaceRegistry
	addrCdc  address.Codec
	// Raw module-account addresses derived from module names (app_config.go),
	// to catch module accounts that do not exist on-chain yet.
	moduleAddrs map[string]struct{}
}

// moduleNames: every module name in app_config.go (gov, transfer, ica..., your
// key-rotation pool, fee_collector, bonded_tokens_pool, ...).
func NewGRPCChain(addr string, registry codectypes.InterfaceRegistry, addrCdc address.Codec, moduleNames []string) (*GRPCChain, error) {
	conn, err := dial(addr)
	if err != nil {
		return nil, err
	}
	m := make(map[string]struct{}, len(moduleNames))
	for _, n := range moduleNames {
		m[string(authtypes.NewModuleAddress(n))] = struct{}{} // VERIFIED
	}
	return &GRPCChain{
		Addr: addr, conn: conn, registry: registry, addrCdc: addrCdc, moduleAddrs: m,
		bank: banktypes.NewQueryClient(conn), auth: authtypes.NewQueryClient(conn),
		tx: sdktx.NewServiceClient(conn),
	}, nil
}

func (c *GRPCChain) Close() error { return c.conn.Close() }

// Balance returns the SPENDABLE balance (excludes locked vesting coins).
func (c *GRPCChain) Balance(ctx context.Context, addr, denom string) (*big.Int, error) {
	r, err := c.bank.SpendableBalanceByDenom(ctx, &banktypes.QuerySpendableBalanceByDenomRequest{Address: addr, Denom: denom}) // VERIFIED
	if err != nil {
		return nil, err
	}
	if r.Balance == nil {
		return nil, errors.New("empty balance response")
	}
	return r.Balance.Amount.BigInt(), nil
}

func (c *GRPCChain) IsModuleAccount(ctx context.Context, addr string) (bool, error) {
	acc, err := c.addrCdc.StringToBytes(addr)
	if err != nil {
		return false, err
	}
	if _, ok := c.moduleAddrs[string(acc)]; ok {
		return true, nil
	}
	r, err := c.auth.Account(ctx, &authtypes.QueryAccountRequest{Address: addr})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return false, nil // not a known module address and no account yet
		}
		return false, err // fail closed
	}
	var a sdk.AccountI
	if err := c.registry.UnpackAny(r.Account, &a); err != nil {
		return false, err
	}
	_, isModule := a.(sdk.ModuleAccountI) // VERIFIED
	return isModule, nil
}

// Simulate checks execution and gas only (NOT signatures).
func (c *GRPCChain) Simulate(ctx context.Context, txBytes []byte) (uint64, error) {
	r, err := c.tx.Simulate(ctx, &sdktx.SimulateRequest{TxBytes: txBytes})
	if err != nil {
		return 0, err
	}
	if r.GasInfo == nil {
		return 0, errors.New("empty gas info")
	}
	return r.GasInfo.GasUsed, nil
}

// ---------------------------------------------------------------------------
// Broadcaster
// ---------------------------------------------------------------------------

type NodeBroadcaster struct {
	Addr string
	conn *grpc.ClientConn
	tx   sdktx.ServiceClient
}

func NewNodeBroadcaster(addr string) (*NodeBroadcaster, error) {
	conn, err := dial(addr)
	if err != nil {
		return nil, err
	}
	return &NodeBroadcaster{Addr: addr, conn: conn, tx: sdktx.NewServiceClient(conn)}, nil
}

func (b *NodeBroadcaster) Close() error { return b.conn.Close() }

func (b *NodeBroadcaster) Broadcast(ctx context.Context, txBytes []byte) (string, error) {
	r, err := b.tx.BroadcastTx(ctx, &sdktx.BroadcastTxRequest{
		TxBytes: txBytes,
		Mode:    sdktx.BroadcastMode_BROADCAST_MODE_SYNC, // VERIFIED (BLOCK is deprecated)
	})
	if err != nil {
		return "", err
	}
	if r.TxResponse == nil {
		return "", errors.New("empty broadcast response")
	}
	if r.TxResponse.Code != 0 {
		return "", fmt.Errorf("checktx failed: code=%d codespace=%s log=%s",
			r.TxResponse.Code, r.TxResponse.Codespace, r.TxResponse.RawLog)
	}
	return r.TxResponse.TxHash, nil
}

// TxResult returns (found, code, rawLog, err). NotFound is found=false, not an error.
func (b *NodeBroadcaster) TxResult(ctx context.Context, hash string) (bool, uint32, string, error) {
	r, err := b.tx.GetTx(ctx, &sdktx.GetTxRequest{Hash: hash})
	if err != nil {
		if status.Code(err) == codes.NotFound { // VERIFIED: x/auth/tx/service.go
			return false, 0, "", nil
		}
		return false, 0, "", err
	}
	if r.TxResponse == nil {
		return false, 0, "", errors.New("empty tx response")
	}
	return true, r.TxResponse.Code, r.TxResponse.RawLog, nil
}

// Tests required before setting Implemented = true (real encoded txs):
//   - valid classic (secp256k1) tx: Verify == nil
//   - valid ML-DSA-65 tx: Verify == nil
//   - tampered body bytes, tampered signature: must fail
//   - wrong chain-id (signed for another chain), wrong account number: must fail
//   - wrong sequence (ordered) / sequence>0 (unordered): must fail
//   - pubkey not matching signer address: must fail
//   - multisig, 2 signers, SIGN_MODE_DIRECT_AUX: must fail
//   - module account, vesting account with locked funds, unknown account
