use av_zone::config::vk_id;
use av_zone::proof::prove_trip;
use av_zone::statement::*;
use pq_verifier_guest::wasm::{serve_frame, FAULT};

pub fn scenario(n: usize) -> (Statement, Vec<Sample>) {
    let t_start = 1_000_000;
    let corners = [(1000, 1000), (3000, 1000), (3000, 3000), (1000, 3000)];
    let mut polygon = [corners[3]; K];
    polygon[..4].copy_from_slice(&corners);
    let samples: Vec<Sample> = (0..n as u32)
        .map(|i| Sample { x: 1500 + (i * 7) % 1000, y: 1500 + (i * 13) % 1000, t: t_start + 2 + 3 * i })
        .collect();
    let t_end = samples.last().unwrap().t + 5;
    (Statement { binding: [7; BINDING_LIMBS], zone_id: 42, t_start, t_end, polygon }, samples)
}

pub fn encode_pub(vals: &[u32]) -> Vec<u8> {
    let mut v = (vals.len() as u32).to_le_bytes().to_vec();
    for x in vals { v.extend_from_slice(&x.to_le_bytes()); }
    v
}

pub fn frame(version: u8, vk: &[u8; 32], pubs: &[u8], proof: &[u8]) -> Vec<u8> {
    let mut f = vec![version];
    f.extend_from_slice(vk);
    f.extend_from_slice(&(pubs.len() as u32).to_le_bytes());
    f.extend_from_slice(&(proof.len() as u32).to_le_bytes());
    f.extend_from_slice(pubs);
    f.extend_from_slice(proof);
    f
}

#[test]
fn real_proof_through_the_wasm_framing_layer() {
    let (st, samples) = scenario(256);
    let proof = prove_trip(&st, &samples, &[0x11; 64]).unwrap();
    let pubs = encode_pub(&st.to_values());
    let vk = vk_id();

    assert_eq!(serve_frame(&frame(2, &vk, &pubs, &proof)), 0, "valid proof");

    // wrong public value -> deterministic rejection, not a fault
    let mut bad = st.clone();
    bad.zone_id += 1;
    let s = serve_frame(&frame(2, &vk, &encode_pub(&bad.to_values()), &proof));
    assert!(s == 1 || s == 2, "status {s}");

    // flipped proof bytes -> 1 or 2, never 0, never a fault
    for pos in [0usize, 1, 100, proof.len() / 2, proof.len() - 1] {
        let mut m = proof.clone();
        m[pos] ^= 0x40;
        let s = serve_frame(&frame(2, &vk, &pubs, &m));
        assert!(s == 1 || s == 2, "flip at {pos}: status {s}");
    }

    // infrastructure problems are faults, never verdicts
    assert_eq!(serve_frame(&frame(1, &vk, &pubs, &proof)), FAULT, "bad version");
    assert_eq!(serve_frame(&frame(2, &[9; 32], &pubs, &proof)), FAULT, "unknown vk_id");
    assert_eq!(serve_frame(&frame(2, &vk, &pubs, &proof)[..40]), FAULT, "short frame");
    let mut trailing = frame(2, &vk, &pubs, &proof);
    trailing.push(0);
    assert_eq!(serve_frame(&trailing), FAULT, "length mismatch");
    // pq self-check shaped request (16 values, 1-byte proof): Malformed
    assert_eq!(serve_frame(&frame(2, &vk, &encode_pub(&[0u32; 16]), &[0])), 2);
}

#[test]
fn embedded_selfcheck_files_native() {
    let dir = std::env::var("EMBED")
        .unwrap_or_else(|_| "../../x/proofs/verifiers/wasm/embed".into());
    let vk = vk_id();
    for (proof_f, pub_f) in [
        ("selfcheck_proof.bin", "selfcheck_pub.bin"),
        ("selfcheck_cap_proof.bin", "selfcheck_cap_pub.bin"),
    ] {
        let proof = std::fs::read(format!("{dir}/{proof_f}")).unwrap();
        let pubs = std::fs::read(format!("{dir}/{pub_f}")).unwrap();
        let mut bad = pubs.clone();
        bad[4 + 4 * 16] += 1; // zone_id low byte
        println!(
            "{proof_f}: valid={} zone_id+1={}",
            serve_frame(&frame(2, &vk, &pubs, &proof)),
            serve_frame(&frame(2, &vk, &bad, &proof)),
        );
    }
}

#[test]
fn embedded_mutation_table_native() {
    let dir = "../../x/proofs/verifiers/wasm/embed";
    let vk = vk_id();
    let proof = std::fs::read(format!("{dir}/selfcheck_proof.bin")).unwrap();
    let pubs = std::fs::read(format!("{dir}/selfcheck_pub.bin")).unwrap();
    let mut row = String::new();
    for i in 0..35 {
        let mut bad = pubs.clone();
        bad[4 + 4 * i] ^= 1; // low bit of public value i
        row.push_str(&format!("{}", serve_frame(&frame(2, &vk, &bad, &proof))));
    }
    println!("NATIVE {row}");
}
