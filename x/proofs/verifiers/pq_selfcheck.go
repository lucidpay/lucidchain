package verifiers

import (
	"bytes"
	_ "embed"
	"fmt"
)

// Copy proof_256.bin and pub_256.bin from the fixture generator to these names
// whenever the AIR or FRI parameters change (the vk_id changes with them).
//
//go:embed wasm/embed/selfcheck_pub.bin
var selfCheckPub []byte

//go:embed wasm/embed/selfcheck_proof.bin
var selfCheckProof []byte

// PQKnownAnswer is a proof that must verify under VKID.
type PQKnownAnswer struct {
	VKID  [VKIDLen]byte
	Pub   []byte
	Proof []byte
}

// DefaultKnownAnswers is the embedded 2^8-row av-zone proof for the first
// allowlisted vk_id.
func DefaultKnownAnswers() []PQKnownAnswer {
	return []PQKnownAnswer{{VKID: PQAllowedVKIDs[0], Pub: selfCheckPub, Proof: selfCheckProof}}
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
		if st, err = run(badPub, ka.Proof); err != nil {
			return fmt.Errorf("self-check vk_id %x (changed public value): %w", id, err)
		}
		if st != StatusInvalid {
			return fmt.Errorf("self-check vk_id %x: changed public value got status %d, want INVALID", id, st)
		}
	}
	return nil
}
