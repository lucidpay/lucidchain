//! Proves an av-zone statement for a trip and writes the artifacts the chain needs.
//!
//!   av-zone-prove <spec.txt> <out_dir> [--insecure-test-entropy-hex <128 hex>]
//!
//! Spec (one item per line, '#' comments allowed):
//!   binding_hex <64 hex>      the 32-byte x/proofs/types.Binding (ComputeBinding output)
//!   zone_id <u32>
//!   t_start <u32>
//!   t_end <u32>
//!   polygon <x> <y>           3..=8 lines, counter-clockwise; padded by repeating the last
//!   sample <x> <y> <t>        count must be a power of two in [256, 16384]
//!
//! Writes <out_dir>/proof.bin and <out_dir>/pub.bin (canonical public inputs:
//! u32 LE count, then the u32 LE values: the exact bytes Go's PQPublicInputs makes).
//! Zero-knowledge randomness comes from /dev/urandom unless a TEST entropy is given.

use av_zone::config::vk_id;
use av_zone::proof::prove_trip;
use av_zone::statement::*;
use std::{fs, io::Read, process::exit};

fn die(msg: impl std::fmt::Display) -> ! {
    eprintln!("error: {msg}");
    exit(2)
}

fn hex(s: &str, n: usize) -> Vec<u8> {
    if s.len() != 2 * n || !s.is_ascii() {
        die(format!("expected {} hex characters, got {}", 2 * n, s.len()));
    }
    (0..n).map(|i| u8::from_str_radix(&s[2 * i..2 * i + 2], 16).unwrap_or_else(|_| die("bad hex"))).collect()
}

/// Same split as Go's bindingToLimbs: the 32 bytes are one big-endian 256-bit
/// integer; limb i is bits [16i, 16i+16), least significant limb first.
fn limbs_from_binding(d: &[u8]) -> [u32; BINDING_LIMBS] {
    let mut out = [0u32; BINDING_LIMBS];
    for (i, o) in out.iter_mut().enumerate() {
        *o = ((d[30 - 2 * i] as u32) << 8) | d[31 - 2 * i] as u32;
    }
    out
}

fn num(tok: Option<&str>, what: &str) -> u32 {
    tok.and_then(|t| t.parse().ok()).unwrap_or_else(|| die(format!("bad or missing {what}")))
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    if args.len() < 3 {
        die("usage: av-zone-prove <spec.txt> <out_dir> [--insecure-test-entropy-hex <128 hex>]");
    }
    let spec = fs::read_to_string(&args[1]).unwrap_or_else(|e| die(format!("read spec: {e}")));

    let (mut binding, mut zone_id, mut t_start, mut t_end) = (None, None, None, None);
    let mut poly: Vec<(u32, u32)> = vec![];
    let mut samples: Vec<Sample> = vec![];
    for line in spec.lines() {
        let line = line.split('#').next().unwrap().trim();
        if line.is_empty() {
            continue;
        }
        let mut it = line.split_whitespace();
        match it.next().unwrap() {
            "binding_hex" => binding = Some(limbs_from_binding(&hex(it.next().unwrap_or(""), 32))),
            "zone_id" => zone_id = Some(num(it.next(), "zone_id")),
            "t_start" => t_start = Some(num(it.next(), "t_start")),
            "t_end" => t_end = Some(num(it.next(), "t_end")),
            "polygon" => poly.push((num(it.next(), "polygon x"), num(it.next(), "polygon y"))),
            "sample" => samples.push(Sample { x: num(it.next(), "x"), y: num(it.next(), "y"), t: num(it.next(), "t") }),
            other => die(format!("unknown spec key {other:?}")),
        }
    }
    if poly.len() < 3 || poly.len() > K {
        die(format!("polygon needs 3..={K} vertices, got {}", poly.len()));
    }
    let mut polygon = [*poly.last().unwrap(); K];
    polygon[..poly.len()].copy_from_slice(&poly);
    let st = Statement {
        binding: binding.unwrap_or_else(|| die("binding_hex is required")),
        zone_id: zone_id.unwrap_or_else(|| die("zone_id is required")),
        t_start: t_start.unwrap_or_else(|| die("t_start is required")),
        t_end: t_end.unwrap_or_else(|| die("t_end is required")),
        polygon,
    };

    let mut entropy = [0u8; 64];
    if args.len() >= 5 && args[3] == "--insecure-test-entropy-hex" {
        eprintln!("WARNING: using caller-supplied entropy; zero-knowledge is NOT protected");
        entropy.copy_from_slice(&hex(&args[4], 64));
    } else {
        fs::File::open("/dev/urandom")
            .and_then(|mut f| f.read_exact(&mut entropy))
            .unwrap_or_else(|e| die(format!("cannot read OS entropy: {e}")));
    }

    let proof = prove_trip(&st, &samples, &entropy).unwrap_or_else(|e| die(format!("cannot prove: {e:?}")));

    let vals = st.to_values();

    use av_zone::proof::{verify_bytes, Verdict};
    if verify_bytes(&vals, &proof) != Verdict::Valid {
        die("self-check: the proof just built does not verify");
    }
    
    let mut pubs = (vals.len() as u32).to_le_bytes().to_vec();
    for v in &vals {
        pubs.extend_from_slice(&v.to_le_bytes());
    }
    fs::create_dir_all(&args[2]).unwrap_or_else(|e| die(e));
    fs::write(format!("{}/proof.bin", args[2]), &proof).unwrap_or_else(|e| die(e));
    fs::write(format!("{}/pub.bin", args[2]), &pubs).unwrap_or_else(|e| die(e));
    let id: String = vk_id().iter().map(|b| format!("{b:02x}")).collect();
    println!("vk_id={id} proof_bytes={} samples={}", proof.len(), samples.len());
}
