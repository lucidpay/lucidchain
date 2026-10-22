//! Writes test fixtures for the Go side: gen_fixtures <out_dir>
use av_zone::config::vk_id;
use av_zone::proof::prove_trip;
use av_zone::statement::*;
use std::fs;

fn main() {
    let dir = std::env::args().nth(1).expect("out dir");
    fs::create_dir_all(&dir).unwrap();
    let t_start = 1_000_000;
    let corners = [(1000, 1000), (3000, 1000), (3000, 3000), (1000, 3000)];
    let mut polygon = [corners[3]; K];
    polygon[..4].copy_from_slice(&corners);
    for n in [256usize, 16384] {
        let samples: Vec<Sample> = (0..n as u32)
            .map(|i| Sample { x: 1500 + (i * 7) % 1000, y: 1500 + (i * 13) % 1000, t: t_start + 2 + 3 * i })
            .collect();
        let t_end = samples.last().unwrap().t + 5;
        let st = Statement { binding: [7; BINDING_LIMBS], zone_id: 42, t_start, t_end, polygon };
        let proof = prove_trip(&st, &samples, &[0x11; 64]).unwrap();
        let mut pubs = (NUM_PUBLIC_VALUES as u32).to_le_bytes().to_vec();
        for x in st.to_values() { pubs.extend_from_slice(&x.to_le_bytes()); }
        fs::write(format!("{dir}/proof_{n}.bin"), &proof).unwrap();
        fs::write(format!("{dir}/pub_{n}.bin"), &pubs).unwrap();
        eprintln!("n={n} proof={} bytes", proof.len());
    }
    fs::write(format!("{dir}/vkid.bin"), vk_id()).unwrap();
}
