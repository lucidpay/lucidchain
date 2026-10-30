//! Writes test fixtures for the Go side: gen_fixtures <out_dir>
use av_zone::config::vk_id;
use av_zone::proof::prove_trip;
use av_zone::statement::*;
use std::fs;

fn main() {
    let dir = std::env::args().nth(1).expect("out dir");
    fs::create_dir_all(&dir).unwrap();
    let corners = [(1000, 1000), (3000, 1000), (3000, 3000), (1000, 3000)];
    let mut polygon = [corners[3]; K];
    polygon[..4].copy_from_slice(&corners);

    // (file tag, rows, t_start in seconds since the protocol epoch)
    let cases: [(&str, usize, u32); 4] = [
        ("256", 256, 1_000_000),
        ("16384", 16384, 1_000_000),
        ("256_rt", 256, 26_000_000),               // realistic: about Oct 2026
        ("256_cap", 256, MAX_TIME - 1_000),        // window ends just under the cap
    ];

    for (tag, n, t_start) in cases {
        let samples: Vec<Sample> = (0..n as u32)
            .map(|i| Sample { x: 1500 + (i * 7) % 1000, y: 1500 + (i * 13) % 1000, t: t_start + 2 + 3 * i })
            .collect();
        let t_end = samples.last().unwrap().t + 5;
        assert!(t_end < MAX_TIME, "case {tag} would exceed MAX_TIME");
        let st = Statement { binding: [7; BINDING_LIMBS], zone_id: 42, t_start, t_end, polygon };
        let proof = prove_trip(&st, &samples, &[0x11; 64]).unwrap();
        let mut pubs = (NUM_PUBLIC_VALUES as u32).to_le_bytes().to_vec();
        for x in st.to_values() { pubs.extend_from_slice(&x.to_le_bytes()); }
        fs::write(format!("{dir}/proof_{tag}.bin"), &proof).unwrap();
        fs::write(format!("{dir}/pub_{tag}.bin"), &pubs).unwrap();
        eprintln!("{tag}: rows={n} t_start={t_start} proof={} bytes", proof.len());
    }
    fs::write(format!("{dir}/vkid.bin"), vk_id()).unwrap();
}