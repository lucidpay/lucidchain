use std::panic::{catch_unwind, AssertUnwindSafe};

use core::borrow::Borrow;
use av_zone::air::{P2Cols, COL_P2, P2_COLS, P2_HALF, WIDTH};
use av_zone::commit::trip_digest;
use av_zone::config::*;
use p3_field::{PrimeCharacteristicRing, PrimeField32};
use p3_matrix::dense::RowMajorMatrix;
use av_zone::proof::*;
use av_zone::statement::*;

const ENTROPY: [u8; 64] = [0x11; 64];
/// Smallest chunk the pinned parameters can prove in zero knowledge.
const N: usize = 256;

fn square(t_start: u32, t_end: u32) -> Statement {
    let corners = [(1000, 1000), (3000, 1000), (3000, 3000), (1000, 3000)];
    let mut polygon = [corners[3]; K];
    polygon[..4].copy_from_slice(&corners);
    Statement { binding: [7; BINDING_LIMBS], zone_id: 42, t_start, t_end, polygon, trip_digest: [0; DIGEST_ELEMS] }
}

/// n samples inside the square, 3 s apart, with a statement whose window the
/// trip covers exactly.
fn scenario(n: usize) -> (Statement, Vec<Sample>) {
    scenario_at(1_000_000, n)
}

/// Like `scenario`, with a chosen window start (seconds since the protocol epoch), so tests can
/// use realistic and near-cap times. The statement carries the digest of exactly these samples.
fn scenario_at(t_start: u32, n: usize) -> (Statement, Vec<Sample>) {
    let samples: Vec<Sample> = (0..n as u32)
        .map(|i| Sample { x: 1500 + (i * 7) % 1000, y: 1500 + (i * 13) % 1000, t: t_start + 2 + 3 * i })
        .collect();
    let t_end = samples.last().unwrap().t + 5;
    let mut st = square(t_start, t_end);
    st.trip_digest = trip_digest(&samples);
    (st, samples)
}

/// True if a violating trace cannot produce an accepted proof: either the
/// prover refuses (error or panic) or the verifier rejects what it produced.
fn rejected(st: &Statement, samples: &[Sample]) -> bool {
    // Isolate the violation under test: the statement always carries the digest of THESE samples,
    // so a digest mismatch cannot be what makes the proof fail.
    let mut st = st.clone();
    st.trip_digest = trip_digest(samples);
    rejected_as_is(&st, samples)
}

/// Same, but the statement is used exactly as given (its digest may not match the samples).
fn rejected_as_is(st: &Statement, samples: &[Sample]) -> bool {
    rejected_trace(st, unchecked_trace(st, samples))
}

#[derive(Debug, PartialEq, Eq)]
enum Outcome {
    ProverRefused,
    VerifierRejected,
    Accepted,
}

fn outcome(st: &Statement, trace: RowMajorMatrix<Val>) -> Outcome {
    match catch_unwind(AssertUnwindSafe(|| prove_trace(st, trace, &ENTROPY))) {
        Err(_) | Ok(Err(_)) => Outcome::ProverRefused,
        Ok(Ok(bytes)) => {
            if verify_bytes(&st.to_values(), &bytes) == Verdict::Valid {
                Outcome::Accepted
            } else {
                Outcome::VerifierRejected
            }
        }
    }
}

fn rejected_trace(st: &Statement, trace: RowMajorMatrix<Val>) -> bool {
    outcome(st, trace) != Outcome::Accepted
}

#[test]
fn honest_proofs_verify_at_several_sizes() {
    for n in [256usize, 512, 1024] {
        let (st, samples) = scenario(n);
        let bytes = prove_trip(&st, &samples, &ENTROPY).expect("prove");
        assert_eq!(verify_bytes(&st.to_values(), &bytes), Verdict::Valid, "n={n}");
        println!("rows={n:5}  width={WIDTH}  proof={:7} bytes", bytes.len());
    }
}

#[test]
fn pinned_parameters_meet_the_security_target() {
    // Plonky3's own estimator, evaluated at every accepted trace height.
    for log_rows in MIN_LOG_ROWS..=MAX_LOG_ROWS {
        let (proven, conjectured) = security_report(PINNED, log_rows);
        println!("rows=2^{log_rows:<2} proven={proven} conjectured={conjectured}");
        assert!(proven >= 100, "proven security {proven} bits at 2^{log_rows} rows");
        assert!(conjectured >= proven);
    }
    assert!(min_rows_for_zk(NUM_QUERIES) <= 1 << MIN_LOG_ROWS, "MIN_LOG_ROWS too small for ZK");

    // A real proof carries log_rows + 1 (the hiding bit).
    let (st, samples) = scenario(N);
    let bytes = prove_trip(&st, &samples, &ENTROPY).unwrap();
    let proof: p3_uni_stark::Proof<Config> = postcard::from_bytes(&bytes).unwrap();
    assert_eq!(proof.degree_bits, MIN_LOG_ROWS + 1);
}

#[test]
fn zero_knowledge_randomness_changes_the_proof() {
    let (st, samples) = scenario(N);
    let a = prove_trip(&st, &samples, &[1; 64]).unwrap();
    let b = prove_trip(&st, &samples, &[2; 64]).unwrap();
    assert_ne!(a, b, "same witness, different entropy must give different proofs");
    assert_eq!(verify_bytes(&st.to_values(), &a), Verdict::Valid);
    assert_eq!(verify_bytes(&st.to_values(), &b), Verdict::Valid);
    // and the same entropy is deterministic
    assert_eq!(a, prove_trip(&st, &samples, &[1; 64]).unwrap());
}

#[test]
fn proof_is_bound_to_every_public_value() {
    let (st, samples) = scenario(N);
    let bytes = prove_trip(&st, &samples, &ENTROPY).unwrap();
    let good = st.to_values();
    assert_eq!(verify_bytes(&good, &bytes), Verdict::Valid);

    // Change each public value in turn to another well-formed value.
    let mut changed = 0;
    for i in 0..good.len() {
        let mut v = good.clone();
        v[i] = match i {
            i if i < BINDING_LIMBS => v[i] ^ 1,           // binding limb
            PV_ZONE_ID => v[i] + 1,                       // zone id
            PV_T_START => v[i] - 1,                       // window start
            PV_T_END => v[i] + 1,                         // window end
            i if (PV_DIGEST..PV_DIGEST + DIGEST_ELEMS).contains(&i) => v[i] ^ 1, // trip digest
            _ => v[i],                                    // polygon handled below
        };
        if v == good {
            continue;
        }
        assert_ne!(verify_bytes(&v, &bytes), Verdict::Valid, "public value {i} is not bound");
        changed += 1;
    }
    assert!(changed >= BINDING_LIMBS + 3 + DIGEST_ELEMS);

    // A different (still convex) zone must not accept a proof made for this one.
    let mut other = st.clone();
    other.polygon[1].0 = 3001;
    assert_ne!(verify_bytes(&other.to_values(), &bytes), Verdict::Valid);
}

#[test]
fn trip_leaving_the_zone_cannot_be_proven() {
    let (st, mut samples) = scenario(N);
    samples[17].x = 3001; // one metre outside the right edge
    assert!(rejected(&st, &samples));

    let (st, mut samples) = scenario(N);
    samples[40].y = 999;
    assert!(rejected(&st, &samples));

    // way outside, and out of the 14-bit coordinate range
    let (st, mut samples) = scenario(N);
    samples[3].x = 1 << COORD_BITS;
    assert!(rejected(&st, &samples));

    // wrap-around attempt: a negative-looking coordinate
    let (st, mut samples) = scenario(N);
    samples[5].x = u32::MAX - 100;
    assert!(rejected(&st, &samples));
}

#[test]
fn boundary_points_are_inside() {
    let (mut st, mut samples) = scenario(N);
    samples[9] = Sample { x: 3000, y: 3000, t: samples[9].t }; // a corner
    samples[10] = Sample { x: 1000, y: 2000, t: samples[10].t }; // on an edge
    st.trip_digest = trip_digest(&samples);
    let bytes = prove_trip(&st, &samples, &ENTROPY).expect("boundary is inside");
    assert_eq!(verify_bytes(&st.to_values(), &bytes), Verdict::Valid);
}

#[test]
fn coverage_gaps_cannot_be_proven() {
    // a 17 s hole: the vehicle could leave and return unseen
    let (st, mut samples) = scenario(N);
    for s in samples.iter_mut().skip(30) {
        s.t += 14;
    }
    let mut st2 = st.clone();
    st2.t_end += 14;
    assert!(rejected(&st2, &samples));

    // repeated timestamp
    let (st, mut samples) = scenario(N);
    samples[20].t = samples[19].t;
    assert!(rejected(&st, &samples));

    // time going backwards
    let (st, mut samples) = scenario(N);
    samples[20].t = samples[19].t - 5;
    assert!(rejected(&st, &samples));

    // starts too late for the window
    let (mut st, samples) = scenario(N);
    st.t_start -= 30;
    assert!(rejected(&st, &samples));

    // ends too early for the window
    let (mut st, samples) = scenario(N);
    st.t_end += 40;
    assert!(rejected(&st, &samples));

    // a sample before the window opens
    let (mut st, samples) = scenario(N);
    st.t_start += 10;
    assert!(rejected(&st, &samples));
}

#[test]
fn prover_refuses_bad_inputs_early() {
    let (st, samples) = scenario(N);
    assert!(matches!(prove_trip(&st, &samples[..N - 1], &ENTROPY), Err(ProveError::BadLength)));
    assert!(matches!(prove_trip(&st, &samples[..4], &ENTROPY), Err(ProveError::BadLength)));
    let mut bad = samples.clone();
    bad[1].x = 5000;
    assert!(matches!(prove_trip(&st, &bad, &ENTROPY), Err(ProveError::Trace(_))));
}

#[test]
fn malformed_inputs_are_classified_not_panicked() {
    let (st, samples) = scenario(N);
    let bytes = prove_trip(&st, &samples, &ENTROPY).unwrap();
    let pv = st.to_values();

    assert_eq!(verify_bytes(&pv, &[]), Verdict::Malformed, "empty");
    assert_eq!(verify_bytes(&pv, &bytes[..bytes.len() / 2]), Verdict::Malformed, "truncated");
    let mut trailing = bytes.clone();
    trailing.push(0);
    assert_eq!(verify_bytes(&pv, &trailing), Verdict::Malformed, "trailing byte");
    assert_eq!(verify_bytes(&pv, &vec![0u8; MAX_PROOF_BYTES + 1]), Verdict::Malformed, "oversize");
    assert_eq!(verify_bytes(&pv[..10], &bytes), Verdict::Malformed, "short public values");
    let mut noncanon = pv.clone();
    noncanon[PV_ZONE_ID] = MODULUS;
    assert_eq!(verify_bytes(&noncanon, &bytes), Verdict::Malformed, "non-canonical public value");
}

#[test]
fn bit_flips_never_panic_and_never_verify() {
    let (st, samples) = scenario(N);
    let bytes = prove_trip(&st, &samples, &ENTROPY).unwrap();
    let pv = st.to_values();
    // Deterministic spread of positions across the whole proof.
    let n = bytes.len();
    let mut positions: Vec<usize> = (0..400).map(|i| (i * 2_654_435_761usize) % n).collect();
    positions.extend([0, 1, 2, 3, n - 1, n - 2, n / 2]);
    for (k, &pos) in positions.iter().enumerate() {
        let mut m = bytes.clone();
        m[pos] ^= 1 << (k % 8);
        let verdict = catch_unwind(AssertUnwindSafe(|| verify_bytes(&pv, &m)))
            .unwrap_or_else(|_| panic!("verifier panicked on flip at byte {pos}"));
        assert_ne!(verdict, Verdict::Valid, "flip at byte {pos} still verified");
    }
}

#[test]
fn last_row_output_is_the_public_digest() {
    let (st, samples) = scenario(N);
    let trace = unchecked_trace(&st, &samples);
    let row = &trace.values[(N - 1) * WIDTH + COL_P2..N * WIDTH];
    let cols: &P2Cols<Val> = row.borrow();
    let out = &cols.ending_full_rounds[P2_HALF - 1].post;
    for j in 0..DIGEST_ELEMS {
        assert_eq!(out[j].as_canonical_u32(), st.trip_digest[j], "digest element {j}");
    }
}

#[test]
fn proof_is_bound_to_the_committed_trip() {
    let (st, samples) = scenario(N);
    let bytes = prove_trip(&st, &samples, &ENTROPY).unwrap();
    // another trip that also satisfies the geometry and time rules, with its own digest
    let mut other = samples.clone();
    other[100].x += 1;
    let mut st2 = st.clone();
    st2.trip_digest = trip_digest(&other);
    assert_ne!(st2.trip_digest, st.trip_digest);
    assert_ne!(verify_bytes(&st2.to_values(), &bytes), Verdict::Valid);
}

#[test]
fn prover_cannot_substitute_samples_for_the_committed_ones() {
    // Every mutation keeps geometry and timing valid, so only the commitment can catch it.
    let (st, samples) = scenario(N);
    type Mutation = Box<dyn Fn(&mut Vec<Sample>)>;
    let cases: Vec<(&str, Mutation)> = vec![
        ("nudge one coordinate", Box::new(|s| s[100].x += 1)),
        (
            "swap the positions of two samples",
            Box::new(|s| {
                let (a, b) = (s[10], s[11]);
                (s[10].x, s[10].y, s[11].x, s[11].y) = (b.x, b.y, a.x, a.y);
            }),
        ),
        (
            "duplicate one position over its neighbour",
            Box::new(|s| {
                s[51].x = s[50].x;
                s[51].y = s[50].y;
            }),
        ),
        ("shift one timestamp inside its slack", Box::new(|s| s[7].t += 1)),
    ];
    for (name, mutate) in cases {
        let mut m = samples.clone();
        mutate(&mut m);
        assert!(rejected_as_is(&st, &m), "{name}: a different trip was accepted");
    }
}

#[test]
fn corrupted_hash_cells_are_rejected() {
    // A malicious prover can write any cell. Corrupt the permutation trace directly: inputs, the
    // chain link into a row, a middle round, the final output.
    let (st, samples) = scenario(N);
    let honest = unchecked_trace(&st, &samples);
    let cells = [
        (0usize, 0usize),
        (0, 5),
        (1, 40),
        (50, 3),  // input that must equal previous output + x/y/t
        (50, 12), // capacity input that must equal previous output
        (100, 150),
        (100, P2_COLS - 1),
        (N - 1, P2_COLS - 1),
        (N - 1, P2_COLS - 9), // a digest element
    ];
    for (row, off) in cells {
        let mut t = honest.clone();
        t.values[row * WIDTH + COL_P2 + off] += Val::ONE;
        assert!(rejected_trace(&st, t), "corrupt cell (row {row}, hash col {off}) was accepted");
    }
}

#[test]
fn realistic_and_boundary_times_verify() {
    // About Oct 2026 in seconds since 2026-01-01, and a window ending just under the cap.
    for t_start in [26_000_000u32, MAX_TIME - 1_000] {
        let (st, samples) = scenario_at(t_start, N);
        assert!(st.t_end < MAX_TIME);
        let bytes = prove_trip(&st, &samples, &ENTROPY).expect("prove");
        assert_eq!(verify_bytes(&st.to_values(), &bytes), Verdict::Valid, "t_start={t_start}");
    }
}

#[test]
fn times_at_or_above_the_cap_are_malformed() {
    let (mut st, samples) = scenario_at(26_000_000, N);
    let bytes = prove_trip(&st, &samples, &ENTROPY).unwrap();
    st.t_end = MAX_TIME;
    assert_eq!(verify_bytes(&st.to_values(), &bytes), Verdict::Malformed);
}

#[test]
fn largest_accepted_proof_fits_the_size_cap() {
    let (st, samples) = scenario(1 << MAX_LOG_ROWS);
    let bytes = prove_trip(&st, &samples, &ENTROPY).unwrap();
    println!("largest accepted proof: {} of {} bytes", bytes.len(), MAX_PROOF_BYTES);
    assert!(bytes.len() <= MAX_PROOF_BYTES, "an honest proof exceeds MAX_PROOF_BYTES");
    assert_eq!(verify_bytes(&st.to_values(), &bytes), Verdict::Valid);
    // one row count above the pinned range is refused up front
    let (st, samples) = scenario(1 << (MAX_LOG_ROWS + 1));
    assert!(matches!(prove_trip(&st, &samples, &ENTROPY), Err(ProveError::BadLength)));
}

#[cfg(not(debug_assertions))]
#[test]
fn in_release_the_verifier_is_what_rejects() {
    let (st, mut samples) = scenario(N);
    samples[17].x = 3001;
    let mut st1 = st.clone();
    st1.trip_digest = trip_digest(&samples);
    assert_eq!(outcome(&st1, unchecked_trace(&st1, &samples)), Outcome::VerifierRejected, "outside zone");

    let (st, samples) = scenario(N);
    let mut other = samples.clone();
    other[100].x += 1;
    assert_eq!(outcome(&st, unchecked_trace(&st, &other)), Outcome::VerifierRejected, "substituted sample");

    let (st, samples) = scenario(N);
    let mut t = unchecked_trace(&st, &samples);
    t.values[100 * WIDTH + COL_P2 + 150] += Val::ONE;
    assert_eq!(outcome(&st, t), Outcome::VerifierRejected, "corrupted hash cell");
}

#[test]
fn vk_id_is_stable() {
    let id = vk_id();
    let hex: String = id.iter().map(|b| format!("{b:02x}")).collect();
    println!("vk_id = {hex}");
    println!("descriptor = {}", vk_descriptor());
    assert_eq!(hex, GOLDEN_VK_ID, "vk_id changed: a constant or the AIR version changed");
}

const GOLDEN_VK_ID: &str = "8dac5a6811948ebe92516fd9256ab4fad7fa844d8880bbdda91587e090c3f35e";
