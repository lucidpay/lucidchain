//! Framing-only entry points for the in-process (wazero) transport. No I/O, no
//! tokio. The exported symbols exist only on wasm32.

use crate::verify::{verify, VKID_LEN};

const WIRE_VERSION: u8 = 2;
const HEADER: usize = 1 + VKID_LEN + 4 + 4;
const MAX_PUB: usize = 4 + 4 * 64;
const MAX_PROOF: usize = 512 * 1024;
/// Never a verdict: the host maps any status > 2 to ErrVerifierFault.
pub const FAULT: u32 = 3;

pub fn serve_frame(f: &[u8]) -> u32 {
    if f.len() < HEADER || f[0] != WIRE_VERSION {
        return FAULT;
    }
    let vk: [u8; VKID_LEN] = f[1..33].try_into().unwrap();
    let pl = u32::from_le_bytes(f[33..37].try_into().unwrap()) as usize;
    let rl = u32::from_le_bytes(f[37..41].try_into().unwrap()) as usize;
    if pl > MAX_PUB || rl > MAX_PROOF || f.len() != HEADER + pl + rl {
        return FAULT;
    }
    let (pubs, proof) = f[HEADER..].split_at(pl);
    match verify(&vk, pubs, proof) {
        Ok(v) => v.status() as u32,
        Err(_) => FAULT,
    }
}

#[cfg(target_arch = "wasm32")]
mod exports {
    #[no_mangle]
    pub extern "C" fn alloc(len: usize) -> *mut u8 {
        Box::into_raw(vec![0u8; len].into_boxed_slice()) as *mut u8
    }
    #[no_mangle]
    pub unsafe extern "C" fn dealloc(ptr: *mut u8, len: usize) {
        drop(Box::from_raw(core::ptr::slice_from_raw_parts_mut(ptr, len)));
    }
    #[no_mangle]
    pub unsafe extern "C" fn verify_frame(ptr: *const u8, len: usize) -> u32 {
        super::serve_frame(core::slice::from_raw_parts(ptr, len))
    }
}
