//! Proving and verification entry points.
//!
//! `verify_bytes` is the function the WASM guest calls. It is a total
//! function: every input yields a verdict or a clean error, never a panic.

use p3_uni_stark::{prove, verify, Proof, VerificationError};

use crate::air::{build_trace, generate_trace, public_values, AvZoneAir, TraceError};
use crate::commit::{salt_is_canonical, Salt};
use crate::config::*;
use crate::statement::*;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Verdict {
    Valid,
    Invalid,
    Malformed,
}

#[derive(Debug)]
pub enum ProveError {
    Statement(StatementError),
    Trace(TraceError),
    BadLength,
    NonCanonicalSalt,
    Prover(String),
}

/// Proves that `samples` satisfy `st`, and that they hash under `salt` to `st.trip_digest`.
/// `salt` is private witness (device CSPRNG); `entropy` is separate, fresh OS randomness for the
/// zero-knowledge of the proof. The sample count must be a power of two within the pinned row range.
pub fn prove_trip(
    st: &Statement,
    samples: &[Sample],
    salt: &Salt,
    entropy: &[u8; 64],
) -> Result<Vec<u8>, ProveError> {
    if !salt_is_canonical(salt) {
        return Err(ProveError::NonCanonicalSalt);
    }
    Statement::from_values(&st.to_values()).map_err(ProveError::Statement)?;
    check_rows(samples.len()).map_err(|_| ProveError::BadLength)?;
    let trace = generate_trace(st, samples, salt).map_err(ProveError::Trace)?;
    prove_trace(st, trace, entropy)
}

/// Proves an arbitrary trace. Exposed for negative tests; honest callers use
/// `prove_trip`.
pub fn prove_trace(
    st: &Statement,
    trace: p3_matrix::dense::RowMajorMatrix<Val>,
    entropy: &[u8; 64],
) -> Result<Vec<u8>, ProveError> {
    let config = make_config(entropy);
    let pis = public_values(st);
    let proof = prove(&config, &AvZoneAir, trace, &pis).map_err(|e| ProveError::Prover(format!("{e:?}")))?;
    postcard::to_allocvec(&proof).map_err(|e| ProveError::Prover(e.to_string()))
}

/// Builds a trace without checking the statement (negative tests only).
pub fn unchecked_trace(st: &Statement, samples: &[Sample], salt: &Salt) -> p3_matrix::dense::RowMajorMatrix<Val> {
    build_trace(st, samples, salt)
}

fn check_rows(n: usize) -> Result<(), ()> {
    if n.is_power_of_two() && (MIN_LOG_ROWS..=MAX_LOG_ROWS).contains(&(n.trailing_zeros() as usize)) {
        Ok(())
    } else {
        Err(())
    }
}

/// Verifies a proof against public values (the canonical u32 elements). Pure
/// and deterministic.
///
///  * `Malformed`: the public values or the proof encoding are not canonical,
///    or the proof's shape is outside the pinned parameters.
///  * `Invalid`: well formed, but the proof does not verify.
pub fn verify_bytes(public_values_u32: &[u32], proof_bytes: &[u8]) -> Verdict {
    let Ok(st) = Statement::from_values(public_values_u32) else {
        return Verdict::Malformed;
    };
    if proof_bytes.is_empty() || proof_bytes.len() > MAX_PROOF_BYTES {
        return Verdict::Malformed;
    }
    let Ok(proof) = postcard::from_bytes::<Proof<Config>>(proof_bytes) else {
        return Verdict::Malformed;
    };
    // One valid encoding per proof: re-encoding must reproduce the input.
    match postcard::to_allocvec(&proof) {
        Ok(again) if again == proof_bytes => {}
        _ => return Verdict::Malformed,
    }
    // The proof states its own trace height; hold it to the pinned range.
    // Hiding FRI commits one extra bit, so degree_bits = log_rows + 1.
    let bits = proof.degree_bits;
    if bits <= MIN_LOG_ROWS || bits > MAX_LOG_ROWS + 1 {
        return Verdict::Malformed;
    }
    let config = verifier_config();
    match verify(&config, &AvZoneAir, &proof, &public_values(&st)) {
        Ok(()) => Verdict::Valid,
        Err(VerificationError::InvalidProofShape(_)) => Verdict::Malformed,
        Err(_) => Verdict::Invalid,
    }
}
