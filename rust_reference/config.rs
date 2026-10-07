//! The pinned proof system configuration. Everything in this file is part of
//! the vk_id: changing any constant changes the identity of the verifier.
//!
//! Choices, and why:
//!  * Hash-based commitments only (FRI over Merkle trees). No pairings or
//!    elliptic curves anywhere, so nothing falls to Shor's algorithm.
//!  * Keccak-256 for Merkle trees and Fiat-Shamir: conservative and
//!    standardised, rather than a newer algebraic hash.
//!  * Hiding FRI (zero-knowledge): leaves are salted and the trace and
//!    quotient are randomised, so the proof does not leak the samples. The
//!    PROVER must supply real entropy; the verifier never uses it.
//!  * BabyBear with a degree-4 extension as the challenge field (~124 bits).
//!  * All FRI parameters are fixed here and never read from the proof.

use p3_baby_bear::BabyBear;
use p3_challenger::{HashChallenger, SerializingChallenger32};
use p3_commit::ExtensionMmcs;
use p3_dft::Radix2DitParallel;
use p3_field::extension::BinomialExtensionField;
use p3_fri::{FriParameters, HidingFriPcs};
use p3_keccak::{Keccak256Hash, KeccakF};
use p3_merkle_tree::MerkleTreeHidingMmcs;
use p3_symmetric::{CompressionFunctionFromHasher, CryptographicHasher, PaddingFreeSponge, SerializingHasher};
use p3_uni_stark::StarkConfig;
use rand::rngs::StdRng;
use rand::SeedableRng;

use crate::statement::*;

pub type Val = BabyBear;
pub type Challenge = BinomialExtensionField<Val, 4>;
type ByteHash = Keccak256Hash;
type U64Hash = PaddingFreeSponge<KeccakF, 25, 17, 4>;
type FieldHash = SerializingHasher<U64Hash>;
type Compress = CompressionFunctionFromHasher<U64Hash, 2, 4>;
type ValMmcs = MerkleTreeHidingMmcs<
    [Val; p3_keccak::VECTOR_LEN],
    [u64; p3_keccak::VECTOR_LEN],
    FieldHash,
    Compress,
    StdRng,
    2,
    4,
    4,
>;
type ChallengeMmcs = ExtensionMmcs<Val, Challenge, ValMmcs>;
type Challenger = SerializingChallenger32<Val, HashChallenger<u8, ByteHash, 32>>;
type Dft = Radix2DitParallel<Val>;
type Pcs = HidingFriPcs<Val, Dft, ValMmcs, ChallengeMmcs, StdRng>;
pub type Config = StarkConfig<Pcs, Challenge, Challenger>;

// ---- pinned FRI parameters ----
pub const LOG_BLOWUP: usize = 3;
pub const LOG_FINAL_POLY_LEN: usize = 0;
pub const MAX_LOG_ARITY: usize = 1;
pub const NUM_QUERIES: usize = 100;
pub const BATCH_POW_BITS: usize = 10;
pub const COMMIT_POW_BITS: usize = 0;
pub const QUERY_POW_BITS: usize = 20;
pub const NUM_RANDOM_CODEWORDS: usize = 4;

/// Trace heights a verifier will accept (log2 rows). The proof carries its own
/// height, so the verifier must bound it.
pub const MIN_LOG_ROWS: usize = 8; // 256 rows: the hiding budget for 100 queries
pub const MAX_LOG_ROWS: usize = 14;

/// Hard cap on proof bytes accepted before any decoding.
pub const MAX_PROOF_BYTES: usize = 512 * 1024;

pub fn fri_params(mmcs: ChallengeMmcs) -> FriParameters<ChallengeMmcs> {
    FriParameters {
        log_blowup: LOG_BLOWUP,
        log_final_poly_len: LOG_FINAL_POLY_LEN,
        max_log_arity: MAX_LOG_ARITY,
        num_queries: NUM_QUERIES,
        batch_proof_of_work_bits: BATCH_POW_BITS,
        commit_proof_of_work_bits: COMMIT_POW_BITS,
        query_proof_of_work_bits: QUERY_POW_BITS,
        mmcs,
    }
}

/// Builds the configuration. `entropy` seeds the zero-knowledge randomness and
/// MUST come from the operating system's CSPRNG when proving. Verification
/// never consumes it, so verifiers pass a constant.
pub fn make_config(entropy: &[u8; 64]) -> Config {
    let byte_hash = ByteHash {};
    let u64_hash = U64Hash::new(KeccakF {});
    let field_hash = FieldHash::new(u64_hash);
    let compress = Compress::new(u64_hash);

    let mut seed_a = [0u8; 32];
    let mut seed_b = [0u8; 32];
    seed_a.copy_from_slice(&entropy[..32]);
    seed_b.copy_from_slice(&entropy[32..]);

    let val_mmcs = ValMmcs::new(field_hash, compress, 0, StdRng::from_seed(seed_a));
    let challenge_mmcs = ChallengeMmcs::new(val_mmcs.clone());
    let pcs = Pcs::new(
        Dft::default(),
        val_mmcs,
        fri_params(challenge_mmcs),
        NUM_RANDOM_CODEWORDS,
        StdRng::from_seed(seed_b),
    );
    Config::new(pcs, Challenger::from_hasher(vec![], byte_hash))
}

pub fn verifier_config() -> Config {
    make_config(&[0u8; 64])
}

/// Identity of this verifier: a hash over everything that determines which
/// statements verify. Bump `VERSION` whenever the AIR's constraints change;
/// the numeric constants below are folded in automatically.
pub const VERSION: &str = "av-zone.core.v0";

pub fn vk_descriptor() -> String {
    format!(
        "{VERSION}|p3=0.8.0|field=babybear|challenge=ext4|hash=keccak256|zk=hiding({NUM_RANDOM_CODEWORDS})\
|K={K}|coord_bits={COORD_BITS}|edge_bits={EDGE_BITS}|gap_bits={GAP_BITS}|time_bits={TIME_BITS}\
|binding_limbs={BINDING_LIMBS}|public_values={NUM_PUBLIC_VALUES}|cols={}\
|fri(log_blowup={LOG_BLOWUP},final={LOG_FINAL_POLY_LEN},arity={MAX_LOG_ARITY},queries={NUM_QUERIES},\
batch_pow={BATCH_POW_BITS},commit_pow={COMMIT_POW_BITS},query_pow={QUERY_POW_BITS})\
|rows=2^{MIN_LOG_ROWS}..2^{MAX_LOG_ROWS}|max_proof={MAX_PROOF_BYTES}",
        crate::air::WIDTH
    )
}

pub fn vk_id() -> [u8; 32] {
    ByteHash {}.hash_iter(vk_descriptor().bytes())
}

/// Candidate or pinned FRI shape, for the security estimator.
#[derive(Clone, Copy, Debug)]
pub struct FriShape {
    pub log_blowup: usize,
    pub num_queries: usize,
    pub batch_pow: usize,
    pub commit_pow: usize,
    pub query_pow: usize,
}

pub const PINNED: FriShape = FriShape {
    log_blowup: LOG_BLOWUP,
    num_queries: NUM_QUERIES,
    batch_pow: BATCH_POW_BITS,
    commit_pow: COMMIT_POW_BITS,
    query_pow: QUERY_POW_BITS,
};

/// Smallest trace height (rows) at which the hiding PCS can hide everything the
/// verifier learns: 2 * (ext_degree * 2 opening points + queries), rounded up
/// to a power of two. Below this the prover refuses to build a ZK proof.
pub fn min_rows_for_zk(num_queries: usize) -> usize {
    (2 * (4 * 2 + num_queries)).next_power_of_two()
}

/// (proven_bits, conjectured_bits) from Plonky3's own estimator, for a trace of
/// 2^log_rows rows. `degree_bits` includes the one extra bit hiding FRI commits.
pub fn security_report(shape: FriShape, log_rows: usize) -> (usize, usize) {
    use p3_air::symbolic::AirLayout;
    use p3_field::coset::TwoAdicMultiplicativeCoset;
    use p3_field::PrimeCharacteristicRing;
    use p3_uni_stark::{ConjecturedSecurity, OpeningShape, ProvenSecurity, StarkSecurityParams};

    let u64_hash = U64Hash::new(KeccakF {});
    let val_mmcs = ValMmcs::new(
        FieldHash::new(u64_hash),
        Compress::new(u64_hash),
        0,
        StdRng::from_seed([0; 32]),
    );
    let fri = FriParameters {
        log_blowup: shape.log_blowup,
        log_final_poly_len: LOG_FINAL_POLY_LEN,
        max_log_arity: MAX_LOG_ARITY,
        num_queries: shape.num_queries,
        batch_proof_of_work_bits: shape.batch_pow,
        commit_proof_of_work_bits: shape.commit_pow,
        query_proof_of_work_bits: shape.query_pow,
        mmcs: ChallengeMmcs::new(val_mmcs),
    };
    let air = crate::air::AvZoneAir;
    let params = StarkSecurityParams::from_air::<Val, Challenge, _>(
        fri.security_regime(),
        &air,
        AirLayout::from_air::<Val>(&air),
        TwoAdicMultiplicativeCoset::new(Val::ONE, log_rows).unwrap(),
        124, // bits of the degree-4 BabyBear challenge field
        128, // collision resistance of the Merkle hash, in bits (classical)
        2,   // the AIR reads the current and next row
        OpeningShape::hiding(NUM_RANDOM_CODEWORDS),
        fri.grinding_sites(),
    );
    let degree_bits = log_rows + 1;
    (
        ProvenSecurity::compute_from_proof(degree_bits, &params).security_bits(),
        ConjecturedSecurity::compute_from_params(&params, degree_bits).security_bits,
    )
}
