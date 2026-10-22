//! The public statement and a plain-integer reference model of what the AIR
//! proves. The reference model is the oracle the AIR is tested against; it is
//! also what a prover uses to refuse to build a proof for a violating trip.
//!
//! A proof of this statement says: *there exists* a lc_sequence of samples
//! (x, y, t), hidden from the verifier, such that
//!   1. every sample lies inside (or on the boundary of) the public convex
//!      polygon,
//!   2. sample times strictly increase with every gap at most MAX_GAP seconds,
//!   3. the first sample is within MAX_GAP-1 s of `t_start` and the last
//!      within MAX_GAP-1 s of `t_end`, so the whole window is covered.
//!
//! It says NOTHING about whether those samples are real. Binding the hidden
//! samples to an attested trip log (a Poseidon2 commitment checked by lookup)
//! is the next stage and is NOT in this version.

pub const K: usize = 8; // polygon vertex slots (unused slots repeat the last vertex)
pub const COORD_BITS: usize = 14; // coordinates are integers in [0, 2^14)
pub const EDGE_BITS: usize = 29; // cross products lie in [0, 2^29) when inside
pub const GAP_BITS: usize = 4;
pub const MAX_GAP: u32 = 1 << GAP_BITS; // consecutive samples at most 16 s apart
pub const TIME_BITS: u32 = 30; // window bounds are < 2^30 so time never wraps the field
pub const BINDING_LIMBS: usize = 16; // 16 x 16-bit limbs, same as the Go adapter

pub const PV_ZONE_ID: usize = BINDING_LIMBS;
pub const PV_T_START: usize = PV_ZONE_ID + 1;
pub const PV_T_END: usize = PV_T_START + 1;
pub const PV_POLY: usize = PV_T_END + 1;
pub const NUM_PUBLIC_VALUES: usize = PV_POLY + 2 * K;

/// BabyBear modulus. Public values must be below it (the Go adapter enforces
/// the same rule).
pub const MODULUS: u32 = 2_013_265_921;

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Statement {
    /// The checkpoint binding as 16-bit limbs (opaque to the geometry).
    pub binding: [u32; BINDING_LIMBS],
    pub zone_id: u32,
    pub t_start: u32,
    pub t_end: u32,
    /// Convex polygon, counter-clockwise; unused trailing slots repeat the
    /// last vertex.
    pub polygon: [(u32, u32); K],
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum StatementError {
    WrongLength,
    NotCanonical,
    BindingLimbTooLarge,
    TimeOutOfRange,
    CoordinateOutOfRange,
    PolygonDegenerate,
    PolygonNotConvexCcw,
}

impl Statement {
    /// Parses and fully validates public values. The AIR is only sound for
    /// statements that pass this, so every verifier must run it first.
    pub fn from_values(v: &[u32]) -> Result<Self, StatementError> {
        if v.len() != NUM_PUBLIC_VALUES {
            return Err(StatementError::WrongLength);
        }
        if v.iter().any(|&x| x >= MODULUS) {
            return Err(StatementError::NotCanonical);
        }
        let mut binding = [0u32; BINDING_LIMBS];
        binding.copy_from_slice(&v[..BINDING_LIMBS]);
        if binding.iter().any(|&x| x >= 1 << 16) {
            return Err(StatementError::BindingLimbTooLarge);
        }
        let (t_start, t_end) = (v[PV_T_START], v[PV_T_END]);
        if t_start >= 1 << TIME_BITS || t_end >= 1 << TIME_BITS || t_end < t_start {
            return Err(StatementError::TimeOutOfRange);
        }
        let mut polygon = [(0u32, 0u32); K];
        for (i, p) in polygon.iter_mut().enumerate() {
            *p = (v[PV_POLY + 2 * i], v[PV_POLY + 2 * i + 1]);
            if p.0 >= 1 << COORD_BITS || p.1 >= 1 << COORD_BITS {
                return Err(StatementError::CoordinateOutOfRange);
            }
        }
        check_convex_ccw(&polygon)?;
        Ok(Self { binding, zone_id: v[PV_ZONE_ID], t_start, t_end, polygon })
    }

    pub fn to_values(&self) -> Vec<u32> {
        let mut v = Vec::with_capacity(NUM_PUBLIC_VALUES);
        v.extend_from_slice(&self.binding);
        v.push(self.zone_id);
        v.push(self.t_start);
        v.push(self.t_end);
        for (x, y) in self.polygon {
            v.push(x);
            v.push(y);
        }
        v
    }
}

fn cross(a: (u32, u32), b: (u32, u32), p: (u32, u32)) -> i64 {
    let (ax, ay, bx, by, px, py) =
        (a.0 as i64, a.1 as i64, b.0 as i64, b.1 as i64, p.0 as i64, p.1 as i64);
    (bx - ax) * (py - ay) - (by - ay) * (px - ax)
}

/// Every vertex must lie on or left of every edge (this rejects concave and
/// self-overlapping polygons such as pentagrams), and the area must be
/// positive. Trailing repeated vertices are padding.
fn check_convex_ccw(poly: &[(u32, u32); K]) -> Result<(), StatementError> {
    let mut m = K;
    while m > 1 && poly[m - 1] == poly[m - 2] {
        m -= 1;
    }
    if m < 3 {
        return Err(StatementError::PolygonDegenerate);
    }
    for i in 0..m {
        if poly[i] == poly[(i + 1) % m] {
            return Err(StatementError::PolygonDegenerate);
        }
    }
    let mut area2: i64 = 0;
    for i in 0..m {
        let (a, b) = (poly[i], poly[(i + 1) % m]);
        area2 += a.0 as i64 * b.1 as i64 - b.0 as i64 * a.1 as i64;
        for j in 0..m {
            if cross(a, b, poly[j]) < 0 {
                return Err(StatementError::PolygonNotConvexCcw);
            }
        }
    }
    if area2 <= 0 {
        return Err(StatementError::PolygonNotConvexCcw);
    }
    Ok(())
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Sample {
    pub x: u32,
    pub y: u32,
    pub t: u32,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Violation {
    NoSamples,
    CoordinateOutOfRange { index: usize },
    OutsideZone { index: usize },
    LeadingGap,
    Gap { index: usize },
    TrailingGap,
}

/// Reference check of the statement for a concrete sample lc_sequence. Uses the
/// slot-level edges exactly as the AIR does (zero-length padding edges
/// included).
pub fn check_trip(st: &Statement, samples: &[Sample]) -> Result<(), Violation> {
    let first = samples.first().ok_or(Violation::NoSamples)?;
    for (i, s) in samples.iter().enumerate() {
        if s.x >= 1 << COORD_BITS || s.y >= 1 << COORD_BITS {
            return Err(Violation::CoordinateOutOfRange { index: i });
        }
        for e in 0..K {
            let (a, b) = (st.polygon[e], st.polygon[(e + 1) % K]);
            if cross(a, b, (s.x, s.y)) < 0 {
                return Err(Violation::OutsideZone { index: i });
            }
        }
    }
    if first.t < st.t_start || first.t - st.t_start >= MAX_GAP {
        return Err(Violation::LeadingGap);
    }
    for (i, w) in samples.windows(2).enumerate() {
        if w[1].t <= w[0].t || w[1].t - w[0].t > MAX_GAP {
            return Err(Violation::Gap { index: i });
        }
    }
    let last = samples.last().unwrap();
    if st.t_end < last.t || st.t_end - last.t >= MAX_GAP {
        return Err(Violation::TrailingGap);
    }
    Ok(())
}

#[cfg(test)]
pub(crate) mod testutil {
    use super::*;

    /// A square zone [1000, 3000)^2 padded to K slots.
    pub fn square_statement(t_start: u32, t_end: u32) -> Statement {
        let corners = [(1000, 1000), (3000, 1000), (3000, 3000), (1000, 3000)];
        let mut polygon = [corners[3]; K];
        polygon[..4].copy_from_slice(&corners);
        Statement { binding: [7; BINDING_LIMBS], zone_id: 42, t_start, t_end, polygon }
    }

    /// n samples drifting inside the square with 3 s spacing.
    pub fn good_trip(st: &Statement, n: usize) -> Vec<Sample> {
        (0..n)
            .map(|i| Sample {
                x: 1500 + (i as u32 * 7) % 1000,
                y: 1500 + (i as u32 * 13) % 1000,
                t: st.t_start + 2 + 3 * i as u32,
            })
            .collect()
    }
}

#[cfg(test)]
mod tests {
    use super::testutil::*;
    use super::*;

    #[test]
    fn statement_round_trips() {
        let st = square_statement(1_000_000, 1_000_000 + 3 * 64 + 4);
        assert_eq!(Statement::from_values(&st.to_values()), Ok(st));
    }

    #[test]
    fn statement_rejects_bad_public_values() {
        let st = square_statement(1_000_000, 1_000_300);
        let ok = st.to_values();

        assert_eq!(Statement::from_values(&ok[..ok.len() - 1]), Err(StatementError::WrongLength));

        let mut v = ok.clone();
        v[PV_ZONE_ID] = MODULUS;
        assert_eq!(Statement::from_values(&v), Err(StatementError::NotCanonical));

        let mut v = ok.clone();
        v[0] = 1 << 16;
        assert_eq!(Statement::from_values(&v), Err(StatementError::BindingLimbTooLarge));

        let mut v = ok.clone();
        v[PV_T_START] = 1 << TIME_BITS;
        assert_eq!(Statement::from_values(&v), Err(StatementError::TimeOutOfRange));

        let mut v = ok.clone();
        v[PV_T_END] = v[PV_T_START] - 1;
        assert_eq!(Statement::from_values(&v), Err(StatementError::TimeOutOfRange));

        let mut v = ok.clone();
        v[PV_POLY] = 1 << COORD_BITS;
        assert_eq!(Statement::from_values(&v), Err(StatementError::CoordinateOutOfRange));
    }

    #[test]
    fn polygon_must_be_convex_and_ccw() {
        let st = square_statement(0, 100);

        // clockwise
        let mut cw = st.clone();
        let rev = [(1000, 3000), (3000, 3000), (3000, 1000), (1000, 1000)];
        cw.polygon = [rev[3]; K];
        cw.polygon[..4].copy_from_slice(&rev);
        assert_eq!(Statement::from_values(&cw.to_values()), Err(StatementError::PolygonNotConvexCcw));

        // concave "L" shape
        let l = [(0, 0), (4000, 0), (4000, 1000), (1000, 1000), (1000, 4000), (0, 4000)];
        let mut p = [l[5]; K];
        p[..6].copy_from_slice(&l);
        let mut concave = st.clone();
        concave.polygon = p;
        assert_eq!(Statement::from_values(&concave.to_values()), Err(StatementError::PolygonNotConvexCcw));

        // pentagram: every turn is a left turn but it winds twice
        let star = [(2000, 0), (3236, 3804), (4, 1382), (4000, 1382), (764, 3804)];
        let mut p = [star[4]; K];
        p[..5].copy_from_slice(&star);
        let mut pent = st.clone();
        pent.polygon = p;
        assert_eq!(Statement::from_values(&pent.to_values()), Err(StatementError::PolygonNotConvexCcw));

        // fewer than three distinct vertices
        let mut line = st.clone();
        line.polygon = [(1, 1); K];
        assert_eq!(Statement::from_values(&line.to_values()), Err(StatementError::PolygonDegenerate));

        // a full 8-vertex convex polygon (no padding) is accepted
        let oct = [(2000, 0), (3414, 586), (4000, 2000), (3414, 3414), (2000, 4000), (586, 3414), (0, 2000), (586, 586)];
        let mut full = st.clone();
        full.polygon = oct;
        assert!(Statement::from_values(&full.to_values()).is_ok());
    }

    #[test]
    fn reference_model_accepts_good_and_rejects_each_violation() {
        let st = square_statement(1_000_000, 1_000_000 + 2 + 3 * 63 + 5);
        let good = good_trip(&st, 64);
        assert_eq!(check_trip(&st, &good), Ok(()));

        assert_eq!(check_trip(&st, &[]), Err(Violation::NoSamples));

        let mut t = good.clone();
        t[10].x = 3001; // just outside the right edge
        assert_eq!(check_trip(&st, &t), Err(Violation::OutsideZone { index: 10 }));

        let mut t = good.clone();
        t[10].x = 3000; // exactly on the boundary is inside
        assert_eq!(check_trip(&st, &t), Ok(()));

        let mut t = good.clone();
        t[5].x = 1 << COORD_BITS;
        assert_eq!(check_trip(&st, &t), Err(Violation::CoordinateOutOfRange { index: 5 }));

        // a hole in coverage: a 17 s gap lets the vehicle leave and return unseen
        let mut t = good.clone();
        for s in t.iter_mut().skip(20) {
            s.t += 14; // 3 s spacing + 14 = 17 s gap
        }
        assert_eq!(check_trip(&st, &t), Err(Violation::Gap { index: 19 }));

        // duplicate / non-increasing timestamps
        let mut t = good.clone();
        t[30].t = t[29].t;
        assert_eq!(check_trip(&st, &t), Err(Violation::Gap { index: 29 }));

        // starts late
        let mut t = good.clone();
        for s in t.iter_mut() {
            s.t += 20;
        }
        assert_eq!(check_trip(&st, &t), Err(Violation::LeadingGap));

        // ends early
        let mut st2 = st.clone();
        st2.t_end += 100;
        assert_eq!(check_trip(&st2, &good), Err(Violation::TrailingGap));
    }
}
