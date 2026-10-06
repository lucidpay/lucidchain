//! The verification layer: a total function from (vk_id, public inputs, proof)
//! to a verdict or a fault.
//!
//! FAIL CLOSED: until the real Plonky3 verifier is linked in, every request
//! ends in `VerifyError::NotImplemented`, which closes the connection and makes
//! the host halt. A mock that accepts everything exists only behind the
//! `insecure-accept-all` feature and cannot be compiled into a release build.

#[cfg(all(feature = "insecure-accept-all", not(debug_assertions)))]
compile_error!("the `insecure-accept-all` feature must never be used in a release build");

use crate::protocol::{MAX_PROOF_BYTES, STATUS_INVALID, STATUS_MALFORMED, STATUS_VALID, VKID_LEN};

/// BabyBear modulus, 2^31 - 2^27 + 1. Must match the Go adapter.
pub const BABYBEAR_MODULUS: u32 = 2_013_265_921;
pub const BINDING_LIMBS: usize = 16;
pub const MAX_PUBLIC_VALUES: usize = 64;

/// vk_ids this image can verify. Populated when the fixed statement's AIR and
/// pinned FRI parameters exist; the vk_id is the hash committing to all of
/// them. Must equal the allowlist compiled into the Go binary.
#[cfg_attr(feature = "insecure-accept-all", allow(dead_code))]
const KNOWN_VK_IDS: &[[u8; VKID_LEN]] = &[];

/// A deterministic answer about a well-formed request.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Verdict {
    Valid,
    Invalid,
    Malformed,
}

impl Verdict {
    pub fn status(self) -> u8 {
        match self {
            Verdict::Valid => STATUS_VALID,
            Verdict::Invalid => STATUS_INVALID,
            Verdict::Malformed => STATUS_MALFORMED,
        }
    }
}

/// Conditions that are NOT verdicts. The server drops the connection and the
/// host raises a verifier fault instead of recording anything.
#[derive(Debug, PartialEq, Eq)]
pub enum VerifyError {
    /// The image does not know this vk_id: the Go allowlist and this image
    /// disagree, which is a deployment error, not a property of the proof.
    UnknownVkId,
    /// The real verifier is not linked in.
    NotImplemented,
}

/// Strictly decodes the canonical public-input encoding: u32 LE count n with
/// BINDING_LIMBS <= n <= MAX_PUBLIC_VALUES, then exactly n u32 LE field
/// elements, each below the BabyBear modulus, with no trailing bytes.
pub fn decode_public_inputs(bz: &[u8]) -> Option<Vec<u32>> {
    let head: [u8; 4] = bz.get(..4)?.try_into().ok()?;
    let n = u32::from_le_bytes(head) as usize;
    if !(BINDING_LIMBS..=MAX_PUBLIC_VALUES).contains(&n) {
        return None;
    }
    if bz.len() != 4 + 4 * n {
        return None;
    }
    bz[4..]
        .chunks_exact(4)
        .map(|c| {
            let x = u32::from_le_bytes(c.try_into().unwrap());
            (x < BABYBEAR_MODULUS).then_some(x)
        })
        .collect()
}

fn is_known_vk(id: &[u8; VKID_LEN]) -> bool {
    #[cfg(feature = "insecure-accept-all")]
    {
        let _ = id;
        true
    }
    #[cfg(not(feature = "insecure-accept-all"))]
    {
        KNOWN_VK_IDS.iter().any(|k| k == id)
    }
}

/// Verifies one request. Pure: no clock, no randomness, no I/O.
pub fn verify(
    vk_id: &[u8; VKID_LEN],
    public_inputs: &[u8],
    proof: &[u8],
) -> Result<Verdict, VerifyError> {
    if !is_known_vk(vk_id) {
        return Err(VerifyError::UnknownVkId);
    }
    let Some(values) = decode_public_inputs(public_inputs) else {
        return Ok(Verdict::Malformed);
    };
    if proof.is_empty() || proof.len() > MAX_PROOF_BYTES {
        return Ok(Verdict::Malformed);
    }
    run_verifier(vk_id, &values, proof)
}

#[cfg(feature = "insecure-accept-all")]
fn run_verifier(_: &[u8; VKID_LEN], _: &[u32], _: &[u8]) -> Result<Verdict, VerifyError> {
    // MOCK. Replace with: select the AIR and FRI parameters from the compiled-in
    // table keyed by vk_id (never from the proof), decode the proof canonically
    // (reject trailing bytes and non-canonical field elements), call
    // p3_uni_stark::verify, and map decode errors to Malformed and verification
    // failures to Invalid. No panics on any input.
    Ok(Verdict::Valid)
}

#[cfg(not(feature = "insecure-accept-all"))]
fn run_verifier(_: &[u8; VKID_LEN], _: &[u32], _: &[u8]) -> Result<Verdict, VerifyError> {
    Err(VerifyError::NotImplemented)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn encode(vals: &[u32]) -> Vec<u8> {
        let mut v = (vals.len() as u32).to_le_bytes().to_vec();
        for x in vals {
            v.extend_from_slice(&x.to_le_bytes());
        }
        v
    }

    #[test]
    fn decodes_canonical_inputs() {
        let vals: Vec<u32> = (0..17).collect();
        assert_eq!(decode_public_inputs(&encode(&vals)), Some(vals));
    }

    #[test]
    fn rejects_non_canonical_inputs() {
        let ok: Vec<u32> = vec![1; 16];
        assert!(decode_public_inputs(&encode(&ok)).is_some());

        let mut trailing = encode(&ok);
        trailing.push(0);
        assert!(decode_public_inputs(&trailing).is_none(), "trailing byte");

        let short = encode(&ok[..15]);
        assert!(decode_public_inputs(&short).is_none(), "fewer than binding limbs");

        let mut big = ok.clone();
        big[3] = BABYBEAR_MODULUS;
        assert!(decode_public_inputs(&encode(&big)).is_none(), "element == modulus");

        let mut count_lie = encode(&ok);
        count_lie[0] = 17;
        assert!(decode_public_inputs(&count_lie).is_none(), "count larger than data");

        assert!(decode_public_inputs(&[]).is_none());
        assert!(decode_public_inputs(&[1, 2, 3]).is_none());

        let too_many = encode(&vec![0u32; MAX_PUBLIC_VALUES + 1]);
        assert!(decode_public_inputs(&too_many).is_none());
    }

    #[cfg(not(feature = "insecure-accept-all"))]
    #[test]
    fn fails_closed_by_default() {
        let pubs = encode(&[0u32; 16]);
        // Unknown vk_id: fault, never a verdict.
        assert_eq!(verify(&[1; 32], &pubs, &[1, 2, 3]), Err(VerifyError::UnknownVkId));
    }

    #[cfg(feature = "insecure-accept-all")]
    #[test]
    fn mock_maps_decode_failures_to_malformed() {
        let pubs = encode(&[0u32; 16]);
        assert_eq!(verify(&[1; 32], &pubs, &[1]), Ok(Verdict::Valid));
        assert_eq!(verify(&[1; 32], &pubs, &[]), Ok(Verdict::Malformed));
        assert_eq!(verify(&[1; 32], &[0, 1], &[1]), Ok(Verdict::Malformed));
    }
}
