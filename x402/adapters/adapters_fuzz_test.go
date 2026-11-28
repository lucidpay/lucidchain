package adapters

// Fuzz tests for the two adapters that parse attacker-controlled bytes.
//
//	go test -run=Fuzz ./x402/adapters                       # seeds only (runs in normal CI)
//	go test -fuzz=FuzzParse  -fuzztime=2m ./x402/adapters
//	go test -fuzz=FuzzVerify -fuzztime=2m ./x402/adapters
//
// A failing input is saved under testdata/fuzz/; commit it as a regression test.
//
// Properties checked:
//   - Parse and Verify never panic. Both contain a recover() backstop, so a
//     recovered panic is reported as a FAILURE here: it means a hostile shape
//     reached unguarded SDK code and deserves an explicit check in decodeTx.
//   - Verify returns nil ONLY for a tx whose signed content (body, auth info and
//     signature) is exactly one of the known-valid seeds. Anything else being
//     accepted would be a forgery.

import (
	"bytes"
	"context"
	"strings"
	"testing"

	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
)

// fuzzSeeds returns an env whose chain knows the signers of the valid seeds,
// the valid seed txs, and a set of invalid seeds (tampered, stripped, junk).
func fuzzSeeds(f *testing.F) (e *env, valid, invalid [][]byte) {
	f.Helper()
	e = newEnv(f)
	for _, kind := range keyKinds {
		k := genKey(f, kind)
		e.addAccount(k, 7, 0, false)
		v := e.signedTx(e.opts(k))
		valid = append(valid, v)
		invalid = append(invalid, tamperMemo(f, v), tamperSignature(f, v), stripAuthInfo(f, v))
	}
	invalid = append(invalid, nil, []byte{}, []byte("junk"), valid[0][:len(valid[0])/2])
	return e, valid, invalid
}

func noPanic(t *testing.T, where string, err error) {
	t.Helper()
	if err != nil && strings.Contains(err.Error(), "panicked") {
		t.Fatalf("%s hit a recovered panic (add an explicit guard): %v", where, err)
	}
}

func FuzzParse(f *testing.F) {
	e, valid, invalid := fuzzSeeds(f)
	for _, s := range append(append([][]byte{}, valid...), invalid...) {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		p, err := e.parser.Parse(raw)
		noPanic(t, "Parse", err)
		if err != nil {
			return
		}
		if !bytes.Equal(p.Raw, raw) {
			t.Fatal("Raw must be the original bytes")
		}
		if p.MsgCount < 0 || p.SignerCount < 0 {
			t.Fatalf("negative counts: %d %d", p.MsgCount, p.SignerCount)
		}
		if p.Amount != nil && (p.Amount.Sign() <= 0 || p.Denom == "") {
			t.Fatalf("non-positive or denom-less amount: %v %q", p.Amount, p.Denom)
		}
	})
}

type signedParts struct {
	body, auth []byte
	sigs       [][]byte
}

func partsOf(raw []byte) (signedParts, bool) {
	var r sdktx.TxRaw
	if err := r.Unmarshal(raw); err != nil {
		return signedParts{}, false
	}
	return signedParts{body: r.BodyBytes, auth: r.AuthInfoBytes, sigs: r.Signatures}, true
}

func samePartsAs(a, b signedParts) bool {
	if !bytes.Equal(a.body, b.body) || !bytes.Equal(a.auth, b.auth) || len(a.sigs) != len(b.sigs) {
		return false
	}
	for i := range a.sigs {
		if !bytes.Equal(a.sigs[i], b.sigs[i]) {
			return false
		}
	}
	return true
}

func FuzzVerify(f *testing.F) {
	e, valid, invalid := fuzzSeeds(f)
	var known []signedParts
	for _, v := range valid {
		p, ok := partsOf(v)
		if !ok {
			f.Fatal("seed does not decode")
		}
		known = append(known, p)
	}
	for _, s := range append(append([][]byte{}, valid...), invalid...) {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, raw []byte) {
		p, err := e.parser.Parse(raw)
		noPanic(t, "Parse", err)
		if err != nil {
			return
		}
		err = e.ver.Verify(context.Background(), p)
		noPanic(t, "Verify", err)
		if err != nil {
			return
		}

		// Accepted: it must be a known-valid signed payload, nothing else.
		got, ok := partsOf(raw)
		if !ok {
			t.Fatal("SECURITY: Verify accepted bytes that are not a TxRaw")
		}
		for _, k := range known {
			if samePartsAs(got, k) {
				if p.SignerCount != 1 {
					t.Fatalf("accepted tx with %d signers", p.SignerCount)
				}
				return
			}
		}
		t.Fatal("SECURITY: Verify accepted a tx whose signed content is not a known-valid seed")
	})
}
