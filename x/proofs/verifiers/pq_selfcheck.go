package verifiers

import (
	"bytes"
	_ "embed"
	"encoding/binary"
	"fmt"
)

// Realistic-time known answer (256 rows, t_start 26,000,000 s after the epoch).
// Regenerate all four files with gen_fixtures whenever the AIR, the FRI
// parameters or the time rules change (the vk_id changes with them).
//
//go:embed wasm/embed/selfcheck_pub.bin
var selfCheckPub []byte

//go:embed wasm/embed/selfcheck_proof.bin
var selfCheckProof []byte

// Same statement shape, window ending just under pqMaxTime.
//
//go:embed wasm/embed/selfcheck_cap_pub.bin
var selfCheckCapPub []byte

//go:embed wasm/embed/selfcheck_cap_proof.bin
var selfCheckCapProof []byte

const (
	// Index of t_end among the public values, and the exclusive time bound.
	// Must equal PV_T_END and MAX_TIME in av-zone/src/statement.rs.
	avZoneIdxTEnd = 18
	pqMaxTime     = 2013265921 - 1<<19
)

// PQKnownAnswer is a proof that must verify under VKID.
type PQKnownAnswer struct {
	VKID  [VKIDLen]byte
	Pub   []byte
	Proof []byte
}

// DefaultKnownAnswers returns the embedded av-zone proofs for the first
// allowlisted vk_id: a realistic-time window and a window just under the cap.
func DefaultKnownAnswers() []PQKnownAnswer {
	id := PQAllowedVKIDs[0]
	return []PQKnownAnswer{
		{VKID: id, Pub: selfCheckPub, Proof: selfCheckProof},
		{VKID: id, Pub: selfCheckCapPub, Proof: selfCheckCapProof},
	}
}

// PQSelfCheck runs each known answer through the transport and requires:
//   - the genuine proof is VALID,
//   - the same proof with one flipped byte is not VALID,
//   - the same proof against a changed public value is INVALID.
//
// This exercises the real STARK verifier. Junk-proof probes cannot: they are
// rejected during decoding, before any verification code runs. Any failure
// means a bad deployment, and the node must not start.
func PQSelfCheck(tr PQTransport, answers []PQKnownAnswer) error {
	if len(answers) == 0 {
		return fmt.Errorf("self-check needs at least one known answer")
	}
	for _, ka := range answers {
		id := ka.VKID[:4]
		run := func(pub, proof []byte) (Status, error) {
			resp, err := tr.Verify(PQRequest{VKID: ka.VKID, PublicInputs: pub, Proof: proof})
			return resp.Status, err
		}

		st, err := run(ka.Pub, ka.Proof)
		if err != nil {
			return fmt.Errorf("self-check vk_id %x: %w", id, err)
		}
		if st != StatusValid {
			return fmt.Errorf("self-check vk_id %x: known-good proof got status %d", id, st)
		}

		flipped := bytes.Clone(ka.Proof)
		flipped[len(flipped)/2] ^= 0x01
		if st, err = run(ka.Pub, flipped); err != nil {
			return fmt.Errorf("self-check vk_id %x (flipped proof): %w", id, err)
		}
		if st == StatusValid {
			return fmt.Errorf("self-check vk_id %x: tampered proof verified", id)
		}

		badPub := bytes.Clone(ka.Pub)
		badPub[4+4*AVZoneIdxZoneID]++ // zone_id: still canonical, so it reaches the verifier
		stBad, err := run(badPub, ka.Proof)
		if err != nil {
			return fmt.Errorf("self-check vk_id %x (changed public value): %w", id, err)
		}
		if stBad != StatusInvalid {
			return fmt.Errorf("self-check vk_id %x: changed public value got status %d, want INVALID", id, stBad)
		}

		// A window end at the cap is outside the statement's range, so the
		// guest must classify it MALFORMED. This guards the time rule.
		capped := bytes.Clone(ka.Pub)
		binary.LittleEndian.PutUint32(capped[4+4*avZoneIdxTEnd:], pqMaxTime)
		stCap, err := run(capped, ka.Proof)
		if err != nil {
			return fmt.Errorf("self-check vk_id %x (t_end at cap): %w", id, err)
		}
		if stCap != StatusMalformed {
			return fmt.Errorf("self-check vk_id %x: t_end at cap got status %d, want MALFORMED", id, stCap)
		}

		if st != StatusInvalid {
			return fmt.Errorf("self-check vk_id %x: changed public value got status %d, want INVALID", id, st)
		}
	}
	return nil
}
