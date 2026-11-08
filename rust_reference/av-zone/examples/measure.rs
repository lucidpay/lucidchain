use av_zone::commit::trip_digest;
use av_zone::config::*;
use av_zone::proof::*;
use av_zone::statement::*;
use std::time::Instant;

fn main() {
    for log in MIN_LOG_ROWS..=MAX_LOG_ROWS {
        let n = 1usize << log;
        let t_start = 26_000_000u32;
        let corners = [(1000, 1000), (3000, 1000), (3000, 3000), (1000, 3000)];
        let mut polygon = [corners[3]; K];
        polygon[..4].copy_from_slice(&corners);
        let samples: Vec<Sample> = (0..n as u32)
            .map(|i| Sample { x: 1500 + (i * 7) % 1000, y: 1500 + (i * 13) % 1000, t: t_start + 2 + 3 * i })
            .collect();
        let t_end = samples.last().unwrap().t + 5;
        let st = Statement { binding: [7; BINDING_LIMBS], zone_id: 42, t_start, t_end, polygon, trip_digest: trip_digest(&samples) };
        let t0 = Instant::now();
        let proof = prove_trip(&st, &samples, &[0x11; 64]).unwrap();
        let prove = t0.elapsed().as_secs_f64();
        let pv = st.to_values();
        let mut us: Vec<f64> = (0..30)
            .map(|_| {
                let t = Instant::now();
                assert_eq!(verify_bytes(&pv, &proof), Verdict::Valid);
                t.elapsed().as_secs_f64() * 1e6
            })
            .collect();
        us.sort_by(|a, b| a.partial_cmp(b).unwrap());
        let (proven, conj) = security_report(PINNED, log);
        println!(
            "rows={n:5}  proof={:7} B ({:4.1}% of cap)  prove={prove:5.1}s  native verify median={:5.0} us  security proven={proven} conjectured={conj}",
            proof.len(),
            100.0 * proof.len() as f64 / MAX_PROOF_BYTES as f64,
            us[us.len() / 2]
        );
    }
}
