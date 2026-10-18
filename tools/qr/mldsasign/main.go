// Command mldsasign signs a message with an ML-DSA-65 private key.
//
// DEV/TESTNET ONLY. The private key is read from stdin as the hex printed by
// `lucidchaind keys export <name> --unarmored-hex --unsafe` (4032-byte packed
// FIPS 204 key = 8064 hex characters). It is read from stdin, not argv, so it
// does not show up in `ps`.
//
// Usage:
//   printf '%s' "$KEY_HEX" | mldsasign --msg-file msg.bin
//
// Output (stdout, one JSON object):
//   {"pubkey_b64": "...", "signature_hex": "...", "signature_b64": "..."}
package main

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/cosmos/cosmos-sdk/crypto/keys/mldsa65"
)

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

func main() {
	msgFile := flag.String("msg-file", "", "file holding the exact bytes to sign")
	flag.Parse()
	if *msgFile == "" {
		fail(fmt.Errorf("--msg-file is required"))
	}

	msg, err := os.ReadFile(*msgFile)
	if err != nil {
		fail(err)
	}
	in, err := io.ReadAll(os.Stdin)
	if err != nil {
		fail(err)
	}
	raw, err := hex.DecodeString(strings.TrimSpace(string(in)))
	if err != nil {
		fail(fmt.Errorf("private key is not valid hex: %w", err))
	}

	priv, err := mldsa65.NewPrivateKeyFromBytes(raw) // checks length and encoding
	if err != nil {
		fail(err)
	}
	sig, err := priv.Sign(msg) // raw message, deterministic, empty context
	if err != nil {
		fail(err)
	}
	pub := priv.PubKey()
	if pub == nil || !pub.VerifySignature(msg, sig) {
		fail(fmt.Errorf("signature does not verify against its own public key"))
	}

	out, _ := json.Marshal(map[string]string{
		"pubkey_b64":    base64.StdEncoding.EncodeToString(pub.Bytes()),
		"signature_hex": hex.EncodeToString(sig),
		"signature_b64": base64.StdEncoding.EncodeToString(sig),
	})
	fmt.Println(string(out))
}
