//! Tamper-and-fuzz harness for `serve_frame`, the guest's only entry point.
//!
//!   cargo run --release -p pq-verifier-guest --example flips -- <fixture_dir> [rounds]
//!
//! Reads proof_<tag>.bin / pub_<tag>.bin / vkid.bin from <fixture_dir> for tags 256 and 1024 (those
//! that exist) and checks the rules a consensus verifier must keep:
//!   * the untouched frame is VALID (0);
//!   * ANY tampering with the proof or the public inputs gives INVALID (1) or MALFORMED (2): never
//!     VALID (0), never a fault (3), never a panic;
//!   * framing lies (bad version, wrong vk_id, wrong lengths, a proof over the size cap) give
//!     FAULT (3), never a verdict.
//! Exits non-zero on the first rule that breaks, after printing every failure.

use pq_verifier_guest::consts::MAX_PROOF_BYTES;
use pq_verifier_guest::wasm::{serve_frame, FAULT};
use std::panic::{catch_unwind, AssertUnwindSafe};
use std::{fs, process};

fn frame(version: u8, vk: &[u8], pubs: &[u8], proof: &[u8]) -> Vec<u8> {
    let mut f = vec![version];
    f.extend_from_slice(vk);
    f.extend_from_slice(&(pubs.len() as u32).to_le_bytes());
    f.extend_from_slice(&(proof.len() as u32).to_le_bytes());
    f.extend_from_slice(pubs);
    f.extend_from_slice(proof);
    f
}

#[derive(Default)]
struct Tally {
    counts: [usize; 4],
    failures: Vec<String>,
}

impl Tally {
    fn run(&mut self, label: String, f: &[u8], allowed: &[u32]) {
        match catch_unwind(AssertUnwindSafe(|| serve_frame(f))) {
            Err(_) => self.failures.push(format!("{label}: PANIC")),
            Ok(s) => {
                self.counts[(s as usize).min(3)] += 1;
                if !allowed.contains(&s) {
                    self.failures.push(format!("{label}: status {s}, allowed {allowed:?}"));
                }
            }
        }
    }
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    let Some(dir) = args.get(1) else {
        eprintln!("usage: flips <fixture_dir> [rounds]");
        process::exit(2);
    };
    let rounds: usize = args.get(2).and_then(|s| s.parse().ok()).unwrap_or(400);
    let vk = fs::read(format!("{dir}/vkid.bin")).unwrap_or_else(|e| {
        eprintln!("cannot read {dir}/vkid.bin: {e}");
        process::exit(2)
    });

    let mut t = Tally::default();
    let tamper = [1u32, 2]; // INVALID or MALFORMED
    let mut tags_run = 0;
    for tag in ["256", "1024"] {
        let (Ok(proof), Ok(pubs)) =
            (fs::read(format!("{dir}/proof_{tag}.bin")), fs::read(format!("{dir}/pub_{tag}.bin")))
        else {
            eprintln!("skipping {tag}: fixtures not found");
            continue;
        };
        tags_run += 1;
        let n = proof.len();

        // 0. the honest frame must verify
        let good = frame(2, &vk, &pubs, &proof);
        t.run(format!("[{tag}] honest frame"), &good, &[0]);

        // 1. single-bit flips in the proof: spread across the body, plus both ends
        let mut positions: Vec<usize> = (0..rounds).map(|k| k.wrapping_mul(2_654_435_761) % n).collect();
        positions.extend(0..16);
        positions.extend(n - 16..n);
        for (k, &pos) in positions.iter().enumerate() {
            let mut m = proof.clone();
            m[pos] ^= 1 << (k % 8);
            t.run(format!("[{tag}] proof bit flip at {pos}"), &frame(2, &vk, &pubs, &m), &tamper);
        }

        // 2. every byte of the public inputs, two different bits
        for pos in 0..pubs.len() {
            for bit in [0u8, 7] {
                let mut m = pubs.clone();
                m[pos] ^= 1 << bit;
                t.run(format!("[{tag}] pub flip at {pos} bit {bit}"), &frame(2, &vk, &m, &proof), &tamper);
            }
        }

        // 3. structural mutations of the proof
        let mut structural: Vec<(String, Vec<u8>)> = vec![
            ("empty proof".into(), vec![]),
            ("one byte".into(), vec![0]),
            ("all zeros".into(), vec![0; n]),
            ("all 0xff".into(), vec![0xff; n]),
            ("truncated by one".into(), proof[..n - 1].to_vec()),
            ("truncated to half".into(), proof[..n / 2].to_vec()),
            ("one extra byte".into(), [&proof[..], &[0u8]].concat()),
            ("doubled".into(), [&proof[..], &proof[..]].concat()),
            ("reversed".into(), proof.iter().rev().copied().collect()),
        ];
        for blk in [64usize, 1024] {
            let mut m = proof.clone();
            let (a, b) = (n / 3, 2 * n / 3);
            for i in 0..blk {
                m.swap(a + i, b + i);
            }
            structural.push((format!("swapped two {blk}-byte blocks"), m));
            let mut z = proof.clone();
            z[n / 2..n / 2 + blk].fill(0);
            structural.push((format!("zeroed {blk} bytes mid-proof"), z));
        }
        for (name, m) in structural {
            // a proof over the size cap is a framing violation (the host must reject it first)
            let allowed: &[u32] = if m.len() > MAX_PROOF_BYTES { &[FAULT] } else { &tamper };
            t.run(format!("[{tag}] {name}"), &frame(2, &vk, &pubs, &m), allowed);
        }

        // 4. framing lies: infrastructure faults, never verdicts
        t.run(format!("[{tag}] bad version"), &frame(1, &vk, &pubs, &proof), &[FAULT]);
        t.run(format!("[{tag}] version 3"), &frame(3, &vk, &pubs, &proof), &[FAULT]);
        let mut bad_vk = vk.clone();
        bad_vk[0] ^= 1;
        t.run(format!("[{tag}] wrong vk_id"), &frame(2, &bad_vk, &pubs, &proof), &[FAULT]);
        t.run(format!("[{tag}] short frame"), &good[..40], &[FAULT]);
        t.run(format!("[{tag}] frame shorter than header"), &good[..10], &[FAULT]);
        let mut trailing = good.clone();
        trailing.push(0);
        t.run(format!("[{tag}] trailing byte"), &trailing, &[FAULT]);
        let mut lie_pub = good.clone();
        lie_pub[33..37].copy_from_slice(&(pubs.len() as u32 + 1).to_le_bytes());
        t.run(format!("[{tag}] pub_len too large by one"), &lie_pub, &[FAULT]);
        let mut huge = good.clone();
        huge[37..41].copy_from_slice(&u32::MAX.to_le_bytes());
        t.run(format!("[{tag}] proof_len = u32::MAX"), &huge, &[FAULT]);
    }

    if tags_run == 0 {
        eprintln!("no fixtures found in {dir}");
        process::exit(2);
    }
    println!(
        "valid={} invalid={} malformed={} fault={}  ({} cases)",
        t.counts[0],
        t.counts[1],
        t.counts[2],
        t.counts[3],
        t.counts.iter().sum::<usize>()
    );
    if t.failures.is_empty() {
        println!("OK: no tampered input was accepted, no fault or panic on tampered content");
    } else {
        for f in t.failures.iter().take(30) {
            eprintln!("FAIL {f}");
        }
        eprintln!("{} rule violations", t.failures.len());
        process::exit(1);
    }
}