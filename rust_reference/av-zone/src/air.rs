//! The AIR for the AV-zone statement with trip-commitment binding, and its trace builder.
//!
//! One row per sample. Columns:
//!   x, y, t                      the hidden sample
//!   x_bits[14], y_bits[14]       range proof: coordinates < 2^14
//!   edge_bits[K][29]             cross product of each polygon edge, proven >= 0
//!   gap_bits[4]                  time gap to the next row (or to t_end on the last row)
//!   lead_bits[4]                 first row only: t - t_start
//!   poseidon2[298]               one Poseidon2 permutation (width 16) per row
//!
//! Trip commitment. The permutations form a sponge chain over the SAME x, y, t cells the
//! geofence constraints use, so the hidden trip that is proven inside the zone is, by
//! construction, the trip whose digest is public:
//!   first row:   inputs = IV + block(x, y, t)   (capacity[1..8] is the hidden salt: a free witness)
//!   transition:  next.inputs = this.outputs + block(next.x, next.y, next.t)
//!   last row:    outputs[0..8] = public digest
//! The permutation itself is constrained by the unmodified `p3_poseidon2_air::Poseidon2Air`,
//! evaluated through a window onto the poseidon2 columns (see `SubBuilder`).
//!
//! Why bit decomposition is sound here: BabyBear is a 31-bit field. A negative integer c with
//! |c| < 2^29 is represented as p - |c| > 2^29, which cannot be written as 29 bits, so
//! "cross = sum of 29 booleans" proves 0 <= cross < 2^29. That argument needs the coordinates
//! to be small integers, hence the 14-bit range proofs on x and y.
//!
//! Public values are constants to the AIR, so every cross product is linear in (x, y). The
//! maximum constraint degree is 3 (the Poseidon2 S-box with one register).

use core::borrow::Borrow;
use std::sync::OnceLock;

use p3_air::{Air, AirBuilder, BaseAir, WindowAccess};
use p3_baby_bear::{
    BabyBear, GenericPoseidon2LinearLayersBabyBear, BABYBEAR_POSEIDON2_HALF_FULL_ROUNDS,
    BABYBEAR_POSEIDON2_PARTIAL_ROUNDS_16, BABYBEAR_POSEIDON2_RC_16_EXTERNAL_FINAL,
    BABYBEAR_POSEIDON2_RC_16_EXTERNAL_INITIAL, BABYBEAR_POSEIDON2_RC_16_INTERNAL,
    BABYBEAR_S_BOX_DEGREE,
};
use p3_field::PrimeCharacteristicRing;
use p3_matrix::dense::RowMajorMatrix;
use p3_poseidon2_air::{generate_trace_rows, num_cols, Poseidon2Air, Poseidon2Cols, RoundConstants};

use crate::commit::{initial_state, sample_block, Salt, DIGEST_ELEMS, RATE, STATE_WIDTH, ZERO_SALT};
use crate::statement::*;

// ---- the Poseidon2 block ----
pub const P2_HALF: usize = BABYBEAR_POSEIDON2_HALF_FULL_ROUNDS;
pub const P2_PARTIAL: usize = BABYBEAR_POSEIDON2_PARTIAL_ROUNDS_16;
pub type P2Air = Poseidon2Air<
    BabyBear,
    GenericPoseidon2LinearLayersBabyBear,
    STATE_WIDTH,
    { BABYBEAR_S_BOX_DEGREE },
    1,
    P2_HALF,
    P2_PARTIAL,
>;
pub type P2Cols<T> = Poseidon2Cols<T, STATE_WIDTH, { BABYBEAR_S_BOX_DEGREE }, 1, P2_HALF, P2_PARTIAL>;
pub const P2_COLS: usize = num_cols::<STATE_WIDTH, { BABYBEAR_S_BOX_DEGREE }, 1, P2_HALF, P2_PARTIAL>();

fn p2_constants() -> RoundConstants<BabyBear, STATE_WIDTH, P2_HALF, P2_PARTIAL> {
    // Plonky3's standard BabyBear width-16 constants: the same ones `default_babybear_poseidon2_16`
    // uses, so the native reference in `commit.rs` and this AIR compute the same permutation.
    RoundConstants::new(
        BABYBEAR_POSEIDON2_RC_16_EXTERNAL_INITIAL,
        BABYBEAR_POSEIDON2_RC_16_INTERNAL,
        BABYBEAR_POSEIDON2_RC_16_EXTERNAL_FINAL,
    )
}

fn p2_air() -> &'static P2Air {
    static AIR: OnceLock<P2Air> = OnceLock::new();
    AIR.get_or_init(|| P2Air::new(p2_constants()))
}

// ---- columns ----
pub const COL_X: usize = 0;
pub const COL_Y: usize = 1;
pub const COL_T: usize = 2;
pub const COL_XBITS: usize = 3;
pub const COL_YBITS: usize = COL_XBITS + COORD_BITS;
pub const COL_EBITS: usize = COL_YBITS + COORD_BITS;
pub const COL_GAP: usize = COL_EBITS + K * EDGE_BITS;
pub const COL_LEAD: usize = COL_GAP + GAP_BITS;
/// First column of the Poseidon2 block. Columns before it are the boolean columns below.
pub const COL_P2: usize = COL_LEAD + GAP_BITS;
pub const WIDTH: usize = COL_P2 + P2_COLS;

#[derive(Clone, Copy, Debug, Default)]
pub struct AvZoneAir;

impl<F> BaseAir<F> for AvZoneAir {
    fn width(&self) -> usize {
        WIDTH
    }
    fn num_public_values(&self) -> usize {
        NUM_PUBLIC_VALUES
    }
    fn max_constraint_degree(&self) -> Option<usize> {
        Some(3)
    }
}

// ---- a window onto a sub-range of the row, so the stock Poseidon2 AIR can be reused unchanged ----

#[derive(Clone, Copy)]
struct SubWindow<'a, V> {
    cur: &'a [V],
    nxt: &'a [V],
}

impl<V> WindowAccess<V> for SubWindow<'_, V> {
    fn current_slice(&self) -> &[V] {
        self.cur
    }
    fn next_slice(&self) -> &[V] {
        self.nxt
    }
}

/// Forwards everything to `inner` except `main()`, which sees only the given sub-window.
/// Constraints, selectors and public values are the inner builder's own.
struct SubBuilder<'a, 'w, AB: AirBuilder> {
    inner: &'a mut AB,
    win: SubWindow<'w, AB::Var>,
}

impl<'w, AB: AirBuilder> AirBuilder for SubBuilder<'_, 'w, AB> {
    type F = AB::F;
    type Expr = AB::Expr;
    type Var = AB::Var;
    type PreprocessedWindow = AB::PreprocessedWindow;
    type MainWindow = SubWindow<'w, AB::Var>;
    type PublicVar = AB::PublicVar;
    type PeriodicVar = AB::PeriodicVar;

    fn main(&self) -> Self::MainWindow {
        self.win
    }
    fn preprocessed(&self) -> &Self::PreprocessedWindow {
        self.inner.preprocessed()
    }
    fn is_first_row(&self) -> Self::Expr {
        self.inner.is_first_row()
    }
    fn is_last_row(&self) -> Self::Expr {
        self.inner.is_last_row()
    }
    fn is_transition(&self) -> Self::Expr {
        self.inner.is_transition()
    }
    fn assert_zero<I: Into<Self::Expr>>(&mut self, x: I) {
        self.inner.assert_zero(x)
    }
    fn public_values(&self) -> &[Self::PublicVar] {
        self.inner.public_values()
    }
    fn periodic_values(&self) -> &[Self::PeriodicVar] {
        self.inner.periodic_values()
    }
}

/// sum_j bits[j] * 2^j as an expression.
fn recompose<AB: AirBuilder>(bits: &[AB::Var]) -> AB::Expr {
    let mut acc = AB::Expr::ZERO;
    for (j, b) in bits.iter().enumerate() {
        acc += AB::Expr::from_u64(1u64 << j) * (*b).into();
    }
    acc
}

impl<AB: AirBuilder<F = BabyBear>> Air<AB> for AvZoneAir {
    fn eval(&self, builder: &mut AB) {
        let main = builder.main();
        let local = main.current_slice();
        let next = main.next_slice();
        let pis: Vec<AB::PublicVar> = builder.public_values().to_vec();

        // Every decomposition column is a bit.
        for c in COL_XBITS..COL_P2 {
            builder.assert_bool(local[c]);
        }

        // Coordinates are small integers.
        builder.assert_eq(local[COL_X], recompose::<AB>(&local[COL_XBITS..COL_YBITS]));
        builder.assert_eq(local[COL_Y], recompose::<AB>(&local[COL_YBITS..COL_EBITS]));

        // Inside the zone: cross(a, b, p) >= 0 for every edge slot.
        let x: AB::Expr = local[COL_X].into();
        let y: AB::Expr = local[COL_Y].into();
        for e in 0..K {
            let a = (pis[PV_POLY + 2 * e], pis[PV_POLY + 2 * e + 1]);
            let nb = (e + 1) % K;
            let b = (pis[PV_POLY + 2 * nb], pis[PV_POLY + 2 * nb + 1]);
            let (ax, ay): (AB::Expr, AB::Expr) = (a.0.into(), a.1.into());
            let (bx, by): (AB::Expr, AB::Expr) = (b.0.into(), b.1.into());
            let cross = (bx - ax.clone()) * (y.clone() - ay) - (by - a.1.into()) * (x.clone() - ax);
            let bits = &local[COL_EBITS + e * EDGE_BITS..COL_EBITS + (e + 1) * EDGE_BITS];
            builder.assert_eq(cross, recompose::<AB>(bits));
        }

        // Time coverage.
        let t: AB::Expr = local[COL_T].into();
        let gap = recompose::<AB>(&local[COL_GAP..COL_LEAD]);
        let t_start: AB::Expr = pis[PV_T_START].into();
        let t_end: AB::Expr = pis[PV_T_END].into();

        builder
            .when_first_row()
            .assert_eq(t.clone() - t_start, recompose::<AB>(&local[COL_LEAD..COL_P2]));
        builder
            .when_transition()
            .assert_eq(next[COL_T].into() - t.clone() - AB::Expr::ONE, gap.clone());
        builder.when_last_row().assert_eq(t_end - t, gap);

        // ---- trip commitment ----
        // 1. The permutation in every row is a correct Poseidon2 permutation of its own inputs.
        let win = SubWindow {
            cur: &local[COL_P2..COL_P2 + P2_COLS],
            nxt: &next[COL_P2..COL_P2 + P2_COLS],
        };
        {
            let mut sub = SubBuilder { inner: &mut *builder, win };
            <P2Air as Air<SubBuilder<'_, '_, AB>>>::eval(p2_air(), &mut sub);
        }

        // 2. Those permutations are chained over exactly this row's x, y, t.
        let cur: &P2Cols<AB::Var> = local[COL_P2..COL_P2 + P2_COLS].borrow();
        let nxt: &P2Cols<AB::Var> = next[COL_P2..COL_P2 + P2_COLS].borrow();
        let out = &cur.ending_full_rounds[P2_HALF - 1].post;
        // Only the fixed part of the initial state (rate = 0, capacity[0] = domain) is used below.
    let iv = initial_state(&ZERO_SALT);
        let block = |row: &[AB::Var], j: usize| -> AB::Expr {
            match j {
                0 => row[COL_X].into(),
                1 => row[COL_Y].into(),
                2 => row[COL_T].into(),
                _ => AB::Expr::ZERO,
            }
        };
        for j in 0..STATE_WIDTH {
            let blk = if j < RATE { block(local, j) } else { AB::Expr::ZERO };
            // Rate part and capacity[0] are pinned on the first row. capacity[1..] is the hidden
            // salt: deliberately unconstrained here; it then flows through every transition.
            if j <= RATE {
                builder.when_first_row().assert_eq(cur.inputs[j], AB::Expr::from(iv[j]) + blk);
            }

            let nblk = if j < RATE { block(next, j) } else { AB::Expr::ZERO };
            builder.when_transition().assert_eq(nxt.inputs[j], out[j] + nblk);
        }

        // 3. The final state's rate part is the public digest.
        for j in 0..DIGEST_ELEMS {
            builder.when_last_row().assert_eq(out[j], pis[PV_DIGEST + j]);
        }
    }
}

fn to_bits(v: u64, n: usize, out: &mut [BabyBear]) {
    for (j, o) in out.iter_mut().enumerate().take(n) {
        *o = BabyBear::from_u64((v >> j) & 1);
    }
}

/// Builds the trace for a trip. `samples.len()` must be a power of two within the pinned range.
/// Refuses (via the reference model) any trip that violates the statement, so an honest prover
/// never produces a trace the AIR rejects.
pub fn generate_trace(
    st: &Statement,
    samples: &[Sample],
    salt: &Salt,
) -> Result<RowMajorMatrix<BabyBear>, TraceError> {
    check_trip(st, samples, salt).map_err(TraceError::Violation)?;
    Ok(build_trace(st, samples, salt))
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum TraceError {
    Violation(Violation),
    BadLength,
}

/// Builds a trace WITHOUT checking the statement. For negative tests only:
/// the result may be unsatisfiable, in which case proving must fail or the
/// proof must not verify.
pub fn build_trace(st: &Statement, samples: &[Sample], salt: &Salt) -> RowMajorMatrix<BabyBear> {
    let n = samples.len();
    let consts = p2_constants();
    let mut state = initial_state(salt);
    let mut values = vec![BabyBear::ZERO; n * WIDTH];
    for (i, s) in samples.iter().enumerate() {
        let row = &mut values[i * WIDTH..(i + 1) * WIDTH];
        row[COL_X] = BabyBear::from_u64(s.x as u64);
        row[COL_Y] = BabyBear::from_u64(s.y as u64);
        row[COL_T] = BabyBear::from_u64(s.t as u64);
        to_bits(s.x as u64, COORD_BITS, &mut row[COL_XBITS..COL_YBITS]);
        to_bits(s.y as u64, COORD_BITS, &mut row[COL_YBITS..COL_EBITS]);
        for e in 0..K {
            let (a, b) = (st.polygon[e], st.polygon[(e + 1) % K]);
            let c = (b.0 as i64 - a.0 as i64) * (s.y as i64 - a.1 as i64)
                - (b.1 as i64 - a.1 as i64) * (s.x as i64 - a.0 as i64);
            // For a violating trip the low bits are written anyway; the
            // recomposition constraint then fails.
            to_bits(c as u64, EDGE_BITS, &mut row[COL_EBITS + e * EDGE_BITS..COL_EBITS + (e + 1) * EDGE_BITS]);
        }
        let gap = if i + 1 < n {
            samples[i + 1].t as i64 - s.t as i64 - 1
        } else {
            st.t_end as i64 - s.t as i64
        };
        to_bits(gap as u64, GAP_BITS, &mut row[COL_GAP..COL_LEAD]);
        if i == 0 {
            to_bits((s.t as i64 - st.t_start as i64) as u64, GAP_BITS, &mut row[COL_LEAD..COL_P2]);
        }

        // Sponge chain: absorb this sample into the previous output state, then permute.
        for (a, b) in state.iter_mut().zip(sample_block(s)) {
            *a += b;
        }
        let perm = generate_trace_rows::<
            BabyBear,
            GenericPoseidon2LinearLayersBabyBear,
            STATE_WIDTH,
            { BABYBEAR_S_BOX_DEGREE },
            1,
            P2_HALF,
            P2_PARTIAL,
        >(vec![state], &consts, 0);
        row[COL_P2..COL_P2 + P2_COLS].copy_from_slice(&perm.values);
        let cols: &P2Cols<BabyBear> = perm.values.as_slice().borrow();
        state = cols.ending_full_rounds[P2_HALF - 1].post;
    }
    RowMajorMatrix::new(values, WIDTH)
}

/// Public values as field elements, in the layout the AIR reads.
pub fn public_values(st: &Statement) -> Vec<BabyBear> {
    st.to_values().into_iter().map(|v| BabyBear::from_u32(v)).collect()
}
