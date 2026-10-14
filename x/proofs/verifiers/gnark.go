package verifiers

// Package verifiers holds the proof-system implementations compiled into the
// chain binary. They are built on gnark (github.com/consensys/gnark), a
// pure-Go zk-SNARK library, so verification is deterministic, needs no cgo and
// cross-compiles with the rest of the node.

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/big"

	"github.com/consensys/gnark-crypto/ecc"
	frbls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	frbn254 "github.com/consensys/gnark-crypto/ecc/bn254/fr"
	"github.com/consensys/gnark/backend/groth16"
	"github.com/consensys/gnark/backend/plonk"
	"github.com/consensys/gnark/backend/witness"

	"github.com/lucidpay/lucidchain/x/proofs/types"
)

type scheme int

const (
	schemeGroth16 scheme = iota
	schemePlonk
)

// minPublicInputs is the number of public inputs the binding convention needs
// (the hi and lo limbs of the checkpoint binding).
const minPublicInputs = 2

// gnarkVerifier verifies gnark Groth16 or PLONK proofs on one curve.
//
// Encodings (all are gnark's own serialization, as produced by the prover):
//   - verification key: vk.WriteTo
//   - proof:            proof.WriteTo
//   - public inputs:    publicWitness.MarshalBinary
//
// Every input must be consumed exactly: trailing bytes are rejected, and a
// proof must re-encode to the same bytes, so each proof has a single valid
// encoding.
type gnarkVerifier struct {
	curve  ecc.ID
	scheme scheme
}

var _ types.ProofVerifier = gnarkVerifier{}

func newGnark(curve ecc.ID, s scheme) gnarkVerifier {
	return gnarkVerifier{curve: curve, scheme: s}
}

// ValidateKey implements types.ProofVerifier.
func (v gnarkVerifier) ValidateKey(vkBytes []byte) error {
	switch v.scheme {
	case schemeGroth16:
		vk := groth16.NewVerifyingKey(v.curve)
		if err := readExact(vk, vkBytes); err != nil {
			return fmt.Errorf("groth16 verifying key: %w", err)
		}
		if n := publicInputCount(vk, schemeGroth16); n < minPublicInputs {
			return fmt.Errorf("verifying key has %d public inputs, need at least %d for the checkpoint binding", n, minPublicInputs)
		}
	case schemePlonk:
		vk := plonk.NewVerifyingKey(v.curve)
		if err := readExact(vk, vkBytes); err != nil {
			return fmt.Errorf("plonk verifying key: %w", err)
		}
		if n := publicInputCount(vk, schemePlonk); n < minPublicInputs {
			return fmt.Errorf("verifying key has %d public inputs, need at least %d for the checkpoint binding", n, minPublicInputs)
		}
	default:
		return errors.New("unknown proof scheme")
	}
	return nil
}

// CheckBinding implements types.ProofVerifier: public inputs 0 and 1 must be
// the binding's hi and lo limbs.
func (v gnarkVerifier) CheckBinding(publicInputs []byte, binding types.Binding) error {
	w, err := v.parseWitness(publicInputs)
	if err != nil {
		return err
	}
	gotHi, gotLo, err := firstTwo(w)
	if err != nil {
		return err
	}
	wantHi, wantLo := binding.Limbs()
	if gotHi.Cmp(wantHi) != 0 || gotLo.Cmp(wantLo) != 0 {
		return errors.New("first two public inputs do not match the checkpoint binding")
	}
	return nil
}

// Verify implements types.ProofVerifier.
func (v gnarkVerifier) Verify(vkBytes, publicInputs, proofBytes []byte) error {
	w, err := v.parseWitness(publicInputs)
	if err != nil {
		return err
	}

	switch v.scheme {
	case schemeGroth16:
		vk := groth16.NewVerifyingKey(v.curve)
		if err := readExact(vk, vkBytes); err != nil {
			return fmt.Errorf("groth16 verifying key: %w", err)
		}
		proof := groth16.NewProof(v.curve)
		if err := readCanonical(proof, proofBytes); err != nil {
			return fmt.Errorf("groth16 proof: %w", err)
		}
		return groth16.Verify(proof, vk, w)

	case schemePlonk:
		vk := plonk.NewVerifyingKey(v.curve)
		if err := readExact(vk, vkBytes); err != nil {
			return fmt.Errorf("plonk verifying key: %w", err)
		}
		proof := plonk.NewProof(v.curve)
		if err := readCanonical(proof, proofBytes); err != nil {
			return fmt.Errorf("plonk proof: %w", err)
		}
		return plonk.Verify(proof, vk, w)
	}
	return errors.New("unknown proof scheme")
}

// maxWitnessElements bounds how many public inputs a witness may declare.
const maxWitnessElements = 1024

// witnessHeaderLen is gnark's witness header: nbPublic, nbSecret and the
// vector length, each a big-endian uint32.
const witnessHeaderLen = 12

// checkWitnessFrame validates the declared counts against the actual byte
// length BEFORE gnark decodes anything. gnark sizes its vector from the
// declared length, so without this check a 44-byte input can force a
// multi-gigabyte allocation. After this check, allocation is bounded by
// len(bz).
func (v gnarkVerifier) checkWitnessFrame(bz []byte) error {
	if len(bz) < witnessHeaderLen {
		return errors.New("public inputs too short")
	}
	nbPublic := binary.BigEndian.Uint32(bz[0:4])
	nbSecret := binary.BigEndian.Uint32(bz[4:8])
	vecLen := binary.BigEndian.Uint32(bz[8:12])

	if nbSecret != 0 {
		return errors.New("public inputs must not carry secret values")
	}
	if vecLen != nbPublic {
		return errors.New("public inputs: header counts disagree")
	}
	if vecLen > maxWitnessElements {
		return fmt.Errorf("public inputs: %d elements exceeds limit %d", vecLen, maxWitnessElements)
	}
	elem := uint64((v.curve.ScalarField().BitLen() + 7) / 8) // 32 for BN254 and BLS12-381
	if uint64(len(bz)) != witnessHeaderLen+uint64(vecLen)*elem {
		return errors.New("public inputs: length does not match declared element count")
	}
	return nil
}

// parseWitness decodes a public witness and requires its encoding to be
// canonical (re-encoding yields the same bytes).
func (v gnarkVerifier) parseWitness(publicInputs []byte) (witness.Witness, error) {
	if err := v.checkWitnessFrame(publicInputs); err != nil {
		return nil, err
	}
	w, err := witness.New(v.curve.ScalarField())
	if err != nil {
		return nil, fmt.Errorf("witness: %w", err)
	}
	if err := w.UnmarshalBinary(publicInputs); err != nil {
		return nil, fmt.Errorf("public inputs: %w", err)
	}
	again, err := w.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("public inputs: %w", err)
	}
	if !bytes.Equal(again, publicInputs) {
		return nil, errors.New("public inputs are not canonically encoded")
	}
	return w, nil
}

// firstTwo returns the first two witness elements as integers.
func firstTwo(w witness.Witness) (a, b *big.Int, err error) {
	switch vec := w.Vector().(type) {
	case frbn254.Vector:
		if len(vec) < minPublicInputs {
			return nil, nil, fmt.Errorf("need at least %d public inputs, got %d", minPublicInputs, len(vec))
		}
		return vec[0].BigInt(new(big.Int)), vec[1].BigInt(new(big.Int)), nil
	case frbls12381.Vector:
		if len(vec) < minPublicInputs {
			return nil, nil, fmt.Errorf("need at least %d public inputs, got %d", minPublicInputs, len(vec))
		}
		return vec[0].BigInt(new(big.Int)), vec[1].BigInt(new(big.Int)), nil
	default:
		return nil, nil, fmt.Errorf("unsupported witness vector type %T", vec)
	}
}

// readCanonical is readExact plus a re-encoding check: gnark's reader also
// accepts the uncompressed form, so a proof must re-encode (WriteTo) to exactly
// the input bytes to keep a single valid encoding per proof.
func readCanonical(obj interface {
	io.ReaderFrom
	io.WriterTo
}, bz []byte) error {
	if err := readExact(obj, bz); err != nil {
		return err
	}
	var buf bytes.Buffer
	if _, err := obj.WriteTo(&buf); err != nil {
		return err
	}
	if !bytes.Equal(buf.Bytes(), bz) {
		return errors.New("not canonically encoded")
	}
	return nil
}

// readExact reads a gnark object from bz and requires that all bytes are used.
func readExact(r io.ReaderFrom, bz []byte) error {
	n, err := r.ReadFrom(bytes.NewReader(bz))
	if err != nil {
		return err
	}
	if n != int64(len(bz)) {
		return fmt.Errorf("%d trailing bytes", int64(len(bz))-n)
	}
	return nil
}

// PublicInputCount implements types.ProofVerifier: the number of public inputs
// the key expects, as counted in a public witness (the Groth16 constant-one
// wire is already subtracted, so a registration of 7 means 7 for both schemes).
func (v gnarkVerifier) PublicInputCount(vkBytes []byte) (int, error) {
	switch v.scheme {
	case schemeGroth16:
		vk := groth16.NewVerifyingKey(v.curve)
		if err := readExact(vk, vkBytes); err != nil {
			return 0, fmt.Errorf("groth16 verifying key: %w", err)
		}
		return publicInputCount(vk, schemeGroth16), nil
	case schemePlonk:
		vk := plonk.NewVerifyingKey(v.curve)
		if err := readExact(vk, vkBytes); err != nil {
			return 0, fmt.Errorf("plonk verifying key: %w", err)
		}
		return publicInputCount(vk, schemePlonk), nil
	}
	return 0, errors.New("unknown proof scheme")
}

// publicInputCount returns how many public inputs a key expects, as counted
// in a public witness. gnark's Groth16 key also counts the implicit constant-one
// wire, PLONK's does not. TestPublicInputCountConvention pins this for the
// gnark version in use: re-run it on every gnark upgrade.
func publicInputCount(vk interface{ NbPublicWitness() int }, s scheme) int {
	n := vk.NbPublicWitness()
	if s == schemeGroth16 {
		n--
	}
	return n
}

// CheckInputs implements types.ProofVerifier.
func (v gnarkVerifier) CheckInputs(publicInputs []byte, expected []types.ExpectedInput) error {
	w, err := v.parseWitness(publicInputs) // frame + canonical re-encode checks
	if err != nil {
		return err
	}
	for _, e := range expected {
		got, err := elementAt(w, e.Index)
		if err != nil {
			return err
		}
		if got.Cmp(new(big.Int).SetBytes(e.Value)) != 0 {
			return fmt.Errorf("public input %d does not match chain state", e.Index)
		}
	}
	return nil
}

func elementAt(w witness.Witness, i uint32) (*big.Int, error) {
	switch vec := w.Vector().(type) {
	case frbn254.Vector:
		if uint64(i) < uint64(len(vec)) {
			return vec[i].BigInt(new(big.Int)), nil
		}
	case frbls12381.Vector:
		if uint64(i) < uint64(len(vec)) {
			return vec[i].BigInt(new(big.Int)), nil
		}
	default:
		return nil, fmt.Errorf("unsupported witness vector type %T", vec)
	}
	return nil, fmt.Errorf("public input %d out of range", i)
}

// BindingWidth implements types.ProofVerifier: the checkpoint binding occupies
// the first two public inputs (hi and lo).
func (v gnarkVerifier) BindingWidth() int { return minPublicInputs }
