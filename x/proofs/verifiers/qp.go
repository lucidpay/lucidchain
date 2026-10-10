package verifiers

// Quantum-resistant verifier adapter.
//
// This does not verify anything itself. It validates and canonicalizes
// everything on the Go side (key id, public inputs, binding, size limits),
// then asks an isolated Rust verifier (running in a microVM, reached over
// vsock) for a verdict. The proof system is hash-based (FRI/STARK, e.g.
// Plonky3 over BabyBear), so there is no pairing or elliptic-curve assumption.
//
// Encodings:
//   - verification key: exactly 32 bytes, a vk_id. The vk_id commits to the
//     AIR, field, hash, FRI parameters, public-input layout and verifier
//     version. The parameters live inside the pinned verifier image, never in
//     the proof. A vk_id must be in the allowlist compiled into the binary.
//   - public inputs: u32 little-endian count n, then n u32 little-endian field
//     elements, each < the BabyBear modulus. The first bindingLimbs elements
//     are the checkpoint binding (16 limbs of 16 bits, least significant
//     first). Trailing bytes are rejected, so each input has one encoding.
//   - proof: opaque to Go; its size is capped and the guest decodes it
//     canonically.

// ┌─────────────────── Validator host  ───────────────────┐
// │  cosmos node (Go)                                     │
// │   x/proofs keeper                                     │
// │     ├─ verdict cache  key=(image_hash, proof_hash)    │
// │     └─ VerifierClient ──vsock──▶ microVM(s)           │
// │                                  ├ read-only rootfs   │
// │                                  ├ no network/disk    │
// │                                  └ Rust verifier svc  │
// └───────────────────────────────────────────────────────┘

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"

	"github.com/lucidpay/lucidchain/x/proofs/types"
)

// PQBabyBear is the proof_system_id of the hash-based verifier.
const PQBabyBear = "plonky3-babybear-v1"

const (
	// babyBearModulus is 2^31 - 2^27 + 1.
	babyBearModulus uint32 = 2013265921

	bindingLimbs = 16 // 16 limbs x 16 bits = 256-bit binding
	limbBits     = 16

	// VKIDLen is the length of a verification key (vk_id) in bytes.
	VKIDLen = 32

	maxPQPublicValues = 64
)

// Status is the verifier's verdict on a well-formed request.
type Status uint8

const (
	StatusValid     Status = 0
	StatusInvalid   Status = 1
	StatusMalformed Status = 2
)

// PQRequest is what the adapter sends to the isolated verifier.
type PQRequest struct {
	VKID         [VKIDLen]byte
	PublicInputs []byte // canonical encoding, already validated by the adapter
	Proof        []byte
}

// PQResponse is the verifier's answer. ImageHash identifies the verifier image
// that produced the verdict and must equal the pinned hash.
type PQResponse struct {
	Status    Status
	ImageHash [32]byte
}

// PQTransport carries one request to the isolated verifier. Any error it
// returns (crash, timeout, vsock failure, bad framing) is treated as a
// verifier fault, never as a verdict. Implementations must not retry with a
// different image and must not synthesize a verdict.
type PQTransport interface {
	Verify(req PQRequest) (PQResponse, error)
}

type pqVerifier struct {
	transport     PQTransport
	imageHash     [32]byte
	vkIDs         map[[VKIDLen]byte]struct{}
	maxProofBytes int
}

var _ types.ProofVerifier = pqVerifier{}

// NewPQ returns the hash-based verifier. imageHash is the pinned hash of the
// verifier image, vkIDs the allowlist of verification keys this binary
// accepts, and maxProofBytes the hard cap on proof size (checked before the
// transport is touched, so the microVM cannot be used as a DoS amplifier).
//
// Register it next to the gnark verifiers:
//
//	m := verifiers.Default()
//	m[verifiers.PQBabyBear] = verifiers.NewPQ(transport, imageHash, vkIDs, 512<<10)
func NewPQ(transport PQTransport, imageHash [32]byte, vkIDs [][VKIDLen]byte, maxProofBytes int) types.ProofVerifier {
	// run some sanity code first, so we don't panic in the middle of a fuzz run.
	if maxProofBytes < 1 || maxProofBytes > PQWireMaxProofBytes {
		panic(fmt.Sprintf("verifiers: maxProofBytes %d outside [1, %d]", maxProofBytes, PQWireMaxProofBytes))
	}
	if len(vkIDs) == 0 {
		panic("verifiers: empty vk_id allowlist")
	}

	set := make(map[[VKIDLen]byte]struct{}, len(vkIDs))
	for _, id := range vkIDs {
		set[id] = struct{}{}
	}
	return pqVerifier{transport: transport, imageHash: imageHash, vkIDs: set, maxProofBytes: maxProofBytes}
}

// PQSelfCheck probes the guest once per allowlisted vk_id with a junk proof.
// A healthy guest answers INVALID or MALFORMED. Anything else (no answer,
// VALID, wrong image) means the deployment is wrong and the node must not
// start, instead of halting on the first real proof.
func PQSelfCheck(tr PQTransport, imageHash [32]byte, vkIDs [][VKIDLen]byte) error {
	pub, err := PQPublicInputs(types.Binding{}, nil)
	if err != nil {
		return err
	}
	for _, id := range vkIDs {
		resp, err := tr.Verify(PQRequest{VKID: id, PublicInputs: pub, Proof: []byte{0}})
		if err != nil {
			return fmt.Errorf("self-check vk_id %x: %w", id[:4], err)
		}
		if resp.ImageHash != imageHash {
			return fmt.Errorf("self-check vk_id %x: image %x does not match pinned %x", id[:4], resp.ImageHash, imageHash)
		}
		if resp.Status != StatusInvalid && resp.Status != StatusMalformed {
			return fmt.Errorf("self-check vk_id %x: junk proof got status %d", id[:4], resp.Status)
		}
	}
	return nil
}

// ValidateKey implements types.ProofVerifier.
func (v pqVerifier) ValidateKey(vkBytes []byte) error {
	_, err := v.parseKey(vkBytes)
	return err
}

// CheckBinding implements types.ProofVerifier: the first bindingLimbs public
// values must be the binding's limbs.
func (v pqVerifier) CheckBinding(publicInputs []byte, binding types.Binding) error {
	vals, err := parsePQPublicInputs(publicInputs)
	if err != nil {
		return err
	}
	want, err := bindingToLimbs(binding)
	if err != nil {
		return err
	}
	for i, w := range want {
		if vals[i] != w {
			return errors.New("public inputs do not match the checkpoint binding")
		}
	}
	return nil
}

// Verify implements types.ProofVerifier. Errors wrapping types.ErrVerifierFault
// are infrastructure failures and must halt the node; all other errors are
// deterministic rejections.
func (v pqVerifier) Verify(vkBytes, publicInputs, proof []byte) error {
	id, err := v.parseKey(vkBytes)
	if err != nil {
		return err
	}
	if _, err := parsePQPublicInputs(publicInputs); err != nil {
		return err
	}
	if len(proof) == 0 {
		return errors.New("empty proof")
	}
	if len(proof) > v.maxProofBytes {
		return fmt.Errorf("proof is %d bytes, limit is %d", len(proof), v.maxProofBytes)
	}

	resp, err := v.transport.Verify(PQRequest{VKID: id, PublicInputs: publicInputs, Proof: proof})
	if err != nil {
		return fmt.Errorf("%w: %v", types.ErrVerifierFault, err)
	}
	if resp.ImageHash != v.imageHash {
		return fmt.Errorf("%w: verifier image %x does not match pinned image %x",
			types.ErrVerifierFault, resp.ImageHash, v.imageHash)
	}

	switch resp.Status {
	case StatusValid:
		return nil
	case StatusInvalid:
		return errors.New("proof is invalid")
	case StatusMalformed:
		return errors.New("proof is malformed")
	default:
		return fmt.Errorf("%w: unknown verdict status %d", types.ErrVerifierFault, resp.Status)
	}
}

func (v pqVerifier) parseKey(vkBytes []byte) ([VKIDLen]byte, error) {
	var id [VKIDLen]byte
	if len(vkBytes) != VKIDLen {
		return id, fmt.Errorf("verification key must be %d bytes (a vk_id), got %d", VKIDLen, len(vkBytes))
	}
	copy(id[:], vkBytes)
	if _, ok := v.vkIDs[id]; !ok {
		return id, errors.New("unknown vk_id: not in this binary's allowlist")
	}
	return id, nil
}

// PQPublicInputs encodes the binding followed by any extra statement values in
// the canonical public-input format. Provers and clients use it to build the
// public inputs the chain will accept.
func PQPublicInputs(binding types.Binding, extra []uint32) ([]byte, error) {
	limbs, err := bindingToLimbs(binding)
	if err != nil {
		return nil, err
	}
	vals := append(limbs, extra...)
	if len(vals) > maxPQPublicValues {
		return nil, fmt.Errorf("too many public values: %d", len(vals))
	}
	for _, x := range vals {
		if x >= babyBearModulus {
			return nil, fmt.Errorf("value %d is not a canonical field element", x)
		}
	}
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, uint32(len(vals)))
	_ = binary.Write(&buf, binary.LittleEndian, vals)
	return buf.Bytes(), nil
}

// parsePQPublicInputs decodes the canonical encoding strictly.
func parsePQPublicInputs(bz []byte) ([]uint32, error) {
	if len(bz) < 4 {
		return nil, errors.New("public inputs too short")
	}
	n := binary.LittleEndian.Uint32(bz[:4])
	if n < bindingLimbs || n > maxPQPublicValues {
		return nil, fmt.Errorf("public input count %d outside [%d, %d]", n, bindingLimbs, maxPQPublicValues)
	}
	if uint64(len(bz)) != 4+4*uint64(n) {
		return nil, errors.New("public inputs have wrong length or trailing bytes")
	}
	vals := make([]uint32, n)
	for i := range vals {
		x := binary.LittleEndian.Uint32(bz[4+4*i:])
		if x >= babyBearModulus {
			return nil, errors.New("public inputs contain a non-canonical field element")
		}
		vals[i] = x
	}
	return vals, nil
}

// bindingToLimbs splits the binding (hi||lo, each limb up to 128 bits) into 16
// limbs of 16 bits, least significant first. 16-bit limbs are always below the
// BabyBear modulus.
func bindingToLimbs(b types.Binding) ([]uint32, error) {
	hi, lo := b.Limbs()
	if hi.Sign() < 0 || lo.Sign() < 0 || hi.BitLen() > 128 || lo.BitLen() > 128 {
		return nil, errors.New("binding limbs must each fit in 128 bits")
	}
	v := new(big.Int).Lsh(hi, 128)
	v.Or(v, lo)
	mask := big.NewInt(1<<limbBits - 1)
	out := make([]uint32, bindingLimbs)
	for i := range out {
		limb := new(big.Int).Rsh(v, uint(i*limbBits))
		out[i] = uint32(limb.And(limb, mask).Uint64())
	}
	return out, nil
}
