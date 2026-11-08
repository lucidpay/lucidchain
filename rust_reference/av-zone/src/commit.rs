//! Trip commitment: a Poseidon2 sponge (BabyBear, width 16, rate 8, capacity 8)
//! over the sample sequence, one permutation per sample.
//!
//! This is the reference implementation of what the AIR proves row by row. Any
//! other implementation (device firmware, Go adapter) must reproduce it bit for bit;
//! `KAT_*` in the tests pin a known-answer vector for that purpose.
//!
//! state_0     = IV                      (all zero, except capacity[0] = CHAIN_DOMAIN)
//! state_{i+1} = Poseidon2(state_i + (x_i, y_i, t_i, 0, 0, 0, 0, 0))   (added to the rate part)
//! digest      = first DIGEST_ELEMS elements (the rate part) of state_n

use p3_baby_bear::{default_babybear_poseidon2_16, BabyBear};
use p3_field::{PrimeCharacteristicRing, PrimeField32};
use p3_symmetric::Permutation;

use crate::statement::Sample;

pub const STATE_WIDTH: usize = 16;
pub const RATE: usize = 8;
pub const DIGEST_ELEMS: usize = 8;
/// Domain separator placed in capacity[0] of the initial state ("AVZ1").
pub const CHAIN_DOMAIN: u32 = 0x4156_5a31;

pub fn initial_state() -> [BabyBear; STATE_WIDTH] {
    let mut s = [BabyBear::ZERO; STATE_WIDTH];
    s[RATE] = BabyBear::from_u32(CHAIN_DOMAIN);
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

pub fn trip_digest(samples: &[Sample]) -> [u32; DIGEST_ELEMS] {
    let perm = default_babybear_poseidon2_16();
    let mut state = initial_state();
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

    fn trip(n: u32) -> Vec<Sample> {
        (0..n).map(|i| Sample { x: 1500 + i * 7, y: 1500 + i * 13, t: 1_000_000 + 2 + 3 * i }).collect()
    }

    #[test]
    fn digest_is_deterministic_and_order_length_and_value_sensitive() {
        let a = trip(16);
        assert_eq!(trip_digest(&a), trip_digest(&a));

        let mut swapped = a.clone();
        swapped.swap(3, 4);
        assert_ne!(trip_digest(&a), trip_digest(&swapped), "order must matter");

        let mut longer = a.clone();
        longer.push(Sample { x: 0, y: 0, t: 0 });
        assert_ne!(trip_digest(&a), trip_digest(&longer), "length must matter (zero block still permutes)");

        let mut nudged = a.clone();
        nudged[7].y += 1;
        assert_ne!(trip_digest(&a), trip_digest(&nudged), "every value must matter");

        assert_ne!(trip_digest(&[]), trip_digest(&a[..1]));
    }

    /// Known-answer vector for other implementations (device, Go adapter).
    /// Samples: x = 1500 + 7i, y = 1500 + 13i, t = 1_000_002 + 3i, for i in 0..16.
    #[test]
    fn known_answer_vector() {
        let d = trip_digest(&trip(16));
        println!("KAT16 = {d:?}");
        assert_eq!(d, KAT16, "digest changed: the commitment is part of the vk_id");
    }
    const KAT16: [u32; DIGEST_ELEMS] = [1345458345, 153694567, 742226656, 793095500, 1404970896, 462234366, 1150301092, 1357741159];
}
