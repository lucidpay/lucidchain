//! Trip commitment: a salted Poseidon2 sponge (BabyBear, width 16, rate 8, capacity 8)
//! over the sample sequence, one permutation per sample.
//!
//! This is the reference implementation of what the AIR proves row by row. Any
//! other implementation (device firmware, Go adapter) must reproduce it bit for bit;
//! `KAT_*` in the tests pin known-answer vectors for that purpose.
//!
//! state_0     = rate part all zero; capacity[0] = CHAIN_DOMAIN; capacity[1..8] = salt
//!               (salt[i] goes to state[RATE + 1 + i])
//! state_{i+1} = Poseidon2(state_i + (x_i, y_i, t_i, 0, 0, 0, 0, 0))   (added to the rate part)
//! digest      = first DIGEST_ELEMS elements (the rate part) of state_n
//!
//! Why a salt. The samples are low-entropy (14-bit coordinates, a known time window, routes
//! constrained by roads), so an unsalted digest lets anyone holding a candidate trajectory hash
//! it and compare against the public value. The zero-knowledge of the proof does not help: the
//! digest is a PUBLIC input. SALT_ELEMS field elements (about 217 bits) of device-chosen
//! randomness in the capacity close that. The salt is private witness: it is not a public value,
//! the AIR leaves it unconstrained, and the verifier never sees it. It MUST come from a CSPRNG
//! (rejection-sample each element below the modulus).

use p3_baby_bear::{default_babybear_poseidon2_16, BabyBear};
use p3_field::{PrimeCharacteristicRing, PrimeField32};
use p3_symmetric::Permutation;

use crate::statement::{Sample, MODULUS};

pub const STATE_WIDTH: usize = 16;
pub const RATE: usize = 8;
pub const DIGEST_ELEMS: usize = 8;
/// Domain separator placed in capacity[0] of the initial state ("AVZ1").
pub const CHAIN_DOMAIN: u32 = 0x4156_5a31;
/// Hidden salt: capacity[1..STATE_WIDTH - RATE], 7 field elements.
pub const SALT_ELEMS: usize = STATE_WIDTH - RATE - 1;

/// Canonical field elements (each below the BabyBear modulus).
pub type Salt = [u32; SALT_ELEMS];
/// The all-zero salt. Reproduces the unsalted Stage 2 chain exactly; used by the AIR to build the
/// fixed part of the initial state, and by tests as a regression anchor. Never a valid choice for
/// a real trip.
pub const ZERO_SALT: Salt = [0; SALT_ELEMS];

pub fn salt_is_canonical(salt: &Salt) -> bool {
    salt.iter().all(|&x| x < MODULUS)
}

/// Callers must check `salt_is_canonical` first.
pub fn initial_state(salt: &Salt) -> [BabyBear; STATE_WIDTH] {
    let mut s = [BabyBear::ZERO; STATE_WIDTH];
    s[RATE] = BabyBear::from_u32(CHAIN_DOMAIN);
    for (d, &x) in s[RATE + 1..].iter_mut().zip(salt.iter()) {
        *d = BabyBear::from_u32(x);
    }
    s
}

/// The block added to the rate part for one sample.
pub fn sample_block(s: &Sample) -> [BabyBear; RATE] {
    let mut b = [BabyBear::ZERO; RATE];
    b[0] = BabyBear::from_u64(s.x as u64);
    b[1] = BabyBear::from_u64(s.y as u64);
    b[2] = BabyBear::from_u64(s.t as u64);
    b
}

pub fn trip_digest(samples: &[Sample], salt: &Salt) -> [u32; DIGEST_ELEMS] {
    let perm = default_babybear_poseidon2_16();
    let mut state = initial_state(salt);
    for s in samples {
        for (a, b) in state.iter_mut().zip(sample_block(s)) {
            *a += b;
        }
        perm.permute_mut(&mut state);
    }
    let mut out = [0u32; DIGEST_ELEMS];
    for (o, e) in out.iter_mut().zip(state.iter()) {
        *o = e.as_canonical_u32();
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    const SALT: Salt = [1, 2, 3, 4, 5, 6, 7];

    fn trip(n: u32) -> Vec<Sample> {
        (0..n).map(|i| Sample { x: 1500 + i * 7, y: 1500 + i * 13, t: 1_000_000 + 2 + 3 * i }).collect()
    }

    #[test]
    fn digest_is_deterministic_and_order_length_and_value_sensitive() {
        let a = trip(16);
        assert_eq!(trip_digest(&a, &SALT), trip_digest(&a, &SALT));

        let mut swapped = a.clone();
        swapped.swap(3, 4);
        assert_ne!(trip_digest(&a, &SALT), trip_digest(&swapped, &SALT), "order must matter");

        let mut longer = a.clone();
        longer.push(Sample { x: 0, y: 0, t: 0 });
        assert_ne!(trip_digest(&a, &SALT), trip_digest(&longer, &SALT), "length must matter (zero block still permutes)");

        let mut nudged = a.clone();
        nudged[7].y += 1;
        assert_ne!(trip_digest(&a, &SALT), trip_digest(&nudged, &SALT), "every value must matter");

        assert_ne!(trip_digest(&[], &SALT), trip_digest(&a[..1], &SALT));
    }

    #[test]
    fn every_salt_element_matters() {
        let a = trip(16);
        let base = trip_digest(&a, &SALT);
        for i in 0..SALT_ELEMS {
            let mut s = SALT;
            s[i] += 1;
            assert_ne!(trip_digest(&a, &s), base, "salt element {i} does not affect the digest");
        }
    }

    #[test]
    fn canonical_salt_check() {
        assert!(salt_is_canonical(&ZERO_SALT));
        let mut s = SALT;
        s[3] = MODULUS - 1;
        assert!(salt_is_canonical(&s));
        s[3] = MODULUS;
        assert!(!salt_is_canonical(&s));
    }

    /// With the all-zero salt the chain is byte-for-byte the unsalted Stage 2 chain, so the
    /// original vector must still hold. This pins that the refactor changed nothing else.
    #[test]
    fn zero_salt_reproduces_the_unsalted_known_answer() {
        assert_eq!(trip_digest(&trip(16), &ZERO_SALT), KAT16_ZERO_SALT);
    }
    const KAT16_ZERO_SALT: [u32; DIGEST_ELEMS] =
        [1345458345, 153694567, 742226656, 793095500, 1404970896, 462234366, 1150301092, 1357741159];

    /// Known-answer vector for other implementations (device, Go adapter), salted.
    /// Samples: x = 1500 + 7i, y = 1500 + 13i, t = 1_000_002 + 3i, for i in 0..16; salt = [1..=7].
    /// Run once with `--nocapture`, paste the printed vector into KAT16_SALTED, then it is enforced.
    #[test]
    fn salted_known_answer_vector() {
        let d = trip_digest(&trip(16), &SALT);
        println!("KAT16_SALTED = {d:?}");
        match KAT16_SALTED {
            Some(k) => assert_eq!(d, k, "digest changed: the commitment is part of the vk_id"),
            None => println!("KAT16_SALTED not pinned yet: paste the vector above into the constant"),
        }
    }
    const KAT16_SALTED: Option<[u32; DIGEST_ELEMS]> = Some([246859429, 1627689361, 871247076, 533616870, 616768534, 197241660, 1573146428, 1651469256]);
}
