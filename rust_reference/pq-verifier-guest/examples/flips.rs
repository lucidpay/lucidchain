use pq_verifier_guest::wasm::serve_frame;
use std::fs;
fn main() {
    let d = std::env::args().nth(1).unwrap();
    let proof = fs::read(format!("{d}/proof_256.bin")).unwrap();
    let pubs = fs::read(format!("{d}/pub_256.bin")).unwrap();
    let vk = fs::read(format!("{d}/vkid.bin")).unwrap();
    let n = proof.len();
    for k in 0..400usize {
        let pos = (k.wrapping_mul(2_654_435_761)) % n;
        let mut m = proof.clone();
        m[pos] ^= 1 << (k % 8);
        let mut f = vec![2u8];
        f.extend_from_slice(&vk);
        f.extend_from_slice(&(pubs.len() as u32).to_le_bytes());
        f.extend_from_slice(&(m.len() as u32).to_le_bytes());
        f.extend_from_slice(&pubs);
        f.extend_from_slice(&m);
        println!("{k} {pos} {}", serve_frame(&f));
    }
}
