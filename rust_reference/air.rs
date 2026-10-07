//! The AIR for the AV-zone core statement and its trace builder.
//!
//! One row per sample. Columns:
//!   x, y, t                      the hidden sample
//!   x_bits[14], y_bits[14]       range proof: coordinates < 2^14
//!   edge_bits[K][29]             cross product of each polygon edge, proven >= 0
//!   gap_bits[4]                  time gap to the next row (or to t_end on the last row)
//!   lead_bits[4]                 first row only: t - t_start
//!
//! Why bit decomposition is sound here: BabyBear is a 31-bit field. A negative
//! integer c with |c| < 2^29 is represented as p - |c| > 2^29, which cannot be
//! written as 29 bits, so "cross = sum of 29 booleans" proves 0 <= cross < 2^29.
//! That argument needs the coordinates to be small integers, hence the 14-bit
//! range proofs on x and y.
//!
//! Public values are constants to the AIR, so every cross product is linear in
//! (x, y) and the maximum constraint degree is 2.

use p3_air::{Air, AirBuilder, BaseAir, WindowAccess};
use p3_baby_bear::BabyBear;
use p3_field::PrimeCharacteristicRing;
use p3_matrix::dense::RowMajorMatrix;

use crate::statement::*;

pub const COL_X: usize = 0;
pub const COL_Y: usize = 1;
pub const COL_T: usize = 2;
pub const COL_XBITS: usize = 3;
pub const COL_YBITS: usize = COL_XBITS + COORD_BITS;
pub const COL_EBITS: usize = COL_YBITS + COORD_BITS;
pub const COL_GAP: usize = COL_EBITS + K * EDGE_BITS;
pub const COL_LEAD: usize = COL_GAP + GAP_BITS;
pub const WIDTH: usize = COL_LEAD + GAP_BITS;

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
        Some(2)
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

impl<AB: AirBuilder> Air<AB> for AvZoneAir {
    fn eval(&self, builder: &mut AB) {
        let main = builder.main();
        let local = main.current_slice();
        let next = main.next_slice();
        let pis: Vec<AB::PublicVar> = builder.public_values().to_vec();

        // Every decomposition column is a bit.
        for c in COL_XBITS..WIDTH {
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
            .assert_eq(t.clone() - t_start, recompose::<AB>(&local[COL_LEAD..WIDTH]));
        builder
            .when_transition()
            .assert_eq(next[COL_T].into() - t.clone() - AB::Expr::ONE, gap.clone());
        builder.when_last_row().assert_eq(t_end - t, gap);
    }
}

fn to_bits(v: u64, n: usize, out: &mut [BabyBear]) {
    for (j, o) in out.iter_mut().enumerate().take(n) {
        *o = BabyBear::from_u64((v >> j) & 1);
    }
}

/// Builds the trace for a trip. `samples.len()` must be a power of two, at
/// least 8. Refuses (via the reference model) any trip that violates the
/// statement, so an honest prover never produces a trace the AIR rejects.
pub fn generate_trace(
    st: &Statement,
    samples: &[Sample],
) -> Result<RowMajorMatrix<BabyBear>, TraceError> {
    check_trip(st, samples).map_err(TraceError::Violation)?;
    Ok(build_trace(st, samples))
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum TraceError {
    Violation(Violation),
    BadLength,
}

/// Builds a trace WITHOUT checking the statement. For negative tests only:
/// the result may be unsatisfiable, in which case proving must fail or the
/// proof must not verify.
pub fn build_trace(st: &Statement, samples: &[Sample]) -> RowMajorMatrix<BabyBear> {
    let n = samples.len();
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
            to_bits((s.t as i64 - st.t_start as i64) as u64, GAP_BITS, &mut row[COL_LEAD..WIDTH]);
        }
    }
    RowMajorMatrix::new(values, WIDTH)
}

/// Public values as field elements, in the layout the AIR reads.
pub fn public_values(st: &Statement) -> Vec<BabyBear> {
    st.to_values().into_iter().map(|v| BabyBear::from_u32(v)).collect()
}
