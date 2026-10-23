// signrotation produces the current-signer-set approvals that
// MsgUpdateSidechainSigners requires (finding N9 of the v0.4-Beta review).
//
// It works for both signer profiles:
//   - classical: secp256k1 keys, as in tools/signcheckpoint
//   - quantum-resistant: ML-DSA-65 keys, as in tools/qr/signcheckpoint
//
// DEV/TESTNET ONLY: private keys are passed on the command line as the hex
// produced by `lucidchaind keys export <name> --unarmored-hex --unsafe`
// (32 bytes for secp256k1, the 4032-byte packed FIPS 204 key for ML-DSA-65;
// the type is inferred from the length).
//
// Usage:
//
//	signrotation -chain-id my-testnet-1 -sidechain-id sc-1 -current-version 1 \
//	  -not-after-height <current height + a few hundred> \
//	  -new-threshold 2 \
//	  -new-key "$(lucidchaind keys show newkey1 --pubkey)" \
//	  -new-key "$(lucidchaind keys show newkey2 --pubkey)" \
//	  -new-key "$(lucidchaind keys show newkey3 --pubkey)" \
//	  -key 0=<hex priv of CURRENT signer 0> -key 2=<hex priv of CURRENT signer 2>
//
// -new-key takes the JSON object printed by `lucidchaind keys show --pubkey`
// (any supported type), or a raw hex secp256k1 public key for backwards
// compatibility. Pass the new keys in the exact order used for --signer-keys
// on the CLI: the order is part of the signed document.
//
// It prints the exact sign bytes to stderr and one
// `--current-set-signatures '{...}'` CLI snippet per approving signer to stdout.
package main

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/crypto/keys/mldsa65"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"

	"github.com/lucidpay/lucidchain/x/sidechain/types"
)

type list []string

func (l *list) String() string     { return strings.Join(*l, ",") }
func (l *list) Set(v string) error { *l = append(*l, v); return nil }

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

const (
	secp256k1PrivKeySize = 32
	mldsa65PrivKeySize   = 4032
)

// parseNewKey accepts the `keys show --pubkey` JSON object or a hex secp256k1 key.
func parseNewKey(v string) (*codectypes.Any, error) {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "{") {
		var pk struct {
			Type string `json:"@type"`
			Key  string `json:"key"`
		}
		if err := json.Unmarshal([]byte(v), &pk); err != nil {
			return nil, fmt.Errorf("bad -new-key JSON: %w", err)
		}
		raw, err := base64.StdEncoding.DecodeString(pk.Key)
		if err != nil {
			return nil, fmt.Errorf("bad -new-key base64: %w", err)
		}
		switch pk.Type {
		case "/cosmos.crypto.secp256k1.PubKey":
			return codectypes.NewAnyWithValue(&secp256k1.PubKey{Key: raw})
		case "/cosmos.crypto.mldsa65.PubKey":
			return codectypes.NewAnyWithValue(&mldsa65.PubKey{Key: raw})
		default:
			return nil, fmt.Errorf("unsupported -new-key type %q", pk.Type)
		}
	}
	raw, err := hex.DecodeString(v)
	if err != nil {
		return nil, fmt.Errorf("-new-key is neither a pubkey JSON object nor hex: %w", err)
	}
	return codectypes.NewAnyWithValue(&secp256k1.PubKey{Key: raw})
}

// parsePrivKey infers the key type from the exported length.
func parsePrivKey(privHex string) (cryptotypes.PrivKey, error) {
	raw, err := hex.DecodeString(privHex)
	if err != nil {
		return nil, err
	}
	switch len(raw) {
	case secp256k1PrivKeySize:
		return &secp256k1.PrivKey{Key: raw}, nil
	case mldsa65PrivKeySize:
		pk, err := mldsa65.NewPrivateKeyFromBytes(raw)
		if err != nil {
			return nil, err
		}
		return &pk, nil
	default:
		return nil, fmt.Errorf("private key is %d bytes; want %d (secp256k1) or %d (ML-DSA-65)",
			len(raw), secp256k1PrivKeySize, mldsa65PrivKeySize)
	}
}

func main() {
	chainID := flag.String("chain-id", "my-testnet-1", "chain id")
	sidechain := flag.String("sidechain-id", "", "sidechain id")
	version := flag.Uint64("current-version", 1, "CURRENT signer_set_version of the sidechain")
	threshold := flag.Uint("new-threshold", 1, "signature threshold of the NEW set")
	notAfter := flag.Uint64("not-after-height", 0, "last block height at which the rotation may execute (required)")
	var newKeys, keys list
	flag.Var(&newKeys, "new-key", "NEW signer public key: `keys show --pubkey` JSON, or secp256k1 hex; repeat in --signer-keys order")
	flag.Var(&keys, "key", "CURRENT signer as <index>=<private key hex>; repeat per approving signer")
	flag.Parse()

	if *sidechain == "" || len(newKeys) == 0 || len(keys) == 0 || *notAfter == 0 {
		must(fmt.Errorf("-sidechain-id, -not-after-height, at least one -new-key and at least one -key are required"))
	}

	anys := make([]*codectypes.Any, 0, len(newKeys))
	for _, k := range newKeys {
		a, err := parseNewKey(k)
		must(err)
		anys = append(anys, a)
	}

	sc := types.Sidechain{Id: *sidechain, SignerSetVersion: *version}
	signBytes, err := types.SignerRotationSignBytes(types.RotationSignDocFor(*chainID, sc, anys, uint32(*threshold), *notAfter))
	must(err)
	fmt.Fprintf(os.Stderr, "chain=%s sc=%s current_version=%d new_keys=%d new_threshold=%d not_after_height=%d\nsignbytes=%x\n",
		*chainID, *sidechain, *version, len(anys), *threshold, *notAfter, signBytes)
	fmt.Printf("--not-after-height %d \\\n", *notAfter)

	for _, k := range keys {
		idxStr, privHex, ok := strings.Cut(k, "=")
		if !ok {
			must(fmt.Errorf("bad -key %q, want <index>=<hex>", k))
		}
		idx, err := strconv.ParseUint(idxStr, 10, 32)
		must(err)
		priv, err := parsePrivKey(privHex)
		must(err)
		sig, err := priv.Sign(signBytes)
		must(err)
		// Fail early, off-chain: the signature must verify under its own public key.
		if pub := priv.PubKey(); pub == nil || !pub.VerifySignature(signBytes, sig) {
			must(fmt.Errorf("signer %d: signature does not verify against its own public key", idx))
		}
		js, _ := json.Marshal(map[string]any{
			"signer_index": idx,
			"signature":    base64.StdEncoding.EncodeToString(sig),
		})
		fmt.Printf("--current-set-signatures '%s' \\\n", js)
	}
}
