package verifiers

// Request frame shared with the Rust guest (see WIRE_SPEC.md). In-process only:
// there is no response frame, digest or image_hash any more.
//
//	version u8 | vk_id [32] | pub_len u32 LE | proof_len u32 LE | pub | proof

import "encoding/binary"

const (
	wireVersion byte = 2

	// PQWireMaxProofBytes is the largest proof the guest accepts. Pass a value
	// no larger than this as maxProofBytes to NewPQ.
	PQWireMaxProofBytes = 512 * 1024

	wireMaxPublicInputBytes = 4 + 4*maxPQPublicValues
	wireHeaderLen           = 1 + VKIDLen + 4 + 4
)

func frameHeader(req PQRequest) [wireHeaderLen]byte {
	var h [wireHeaderLen]byte
	h[0] = wireVersion
	copy(h[1:1+VKIDLen], req.VKID[:])
	binary.LittleEndian.PutUint32(h[1+VKIDLen:], uint32(len(req.PublicInputs)))
	binary.LittleEndian.PutUint32(h[1+VKIDLen+4:], uint32(len(req.Proof)))
	return h
}

// encodeRequest builds a whole frame. The transport does not need it (it writes
// the parts straight into guest memory); tests and fuzzers do.
func encodeRequest(req PQRequest) []byte {
	h := frameHeader(req)
	buf := make([]byte, 0, wireHeaderLen+len(req.PublicInputs)+len(req.Proof))
	buf = append(buf, h[:]...)
	buf = append(buf, req.PublicInputs...)
	return append(buf, req.Proof...)
}
