//! The guest's only entry point. Framing and dispatch; no I/O.
//! The exported symbols exist only on wasm32.
//!
//! ABI (host side: pq_wasm_transport.go). The host instantiates a FRESH module
//! per request, calls `alloc(frame_len)`, writes the frame at the returned
//! pointer, calls `verify_frame(ptr, frame_len)`, reads the u32 status, then
//! discards the instance. There is no `dealloc`: the instance's memory is
//! thrown away with it.

use crate::consts::{MAX_PROOF_BYTES, VKID_LEN};
use crate::verify::{verify, BABYBEAR_MODULUS, BINDING_LIMBS, MAX_PUBLIC_VALUES};

pub const WIRE_VERSION: u8 = 2;
const HEADER: usize = 1 + VKID_LEN + 4 + 4;
const MAX_PUB: usize = 4 + 4 * MAX_PUBLIC_VALUES;
pub const MAX_FRAME: usize = HEADER + MAX_PUB + MAX_PROOF_BYTES;
/// Never a verdict: the host maps any status > 2 to ErrVerifierFault.
pub const FAULT: u32 = 3;

// One definition per constant, with the copies that remain checked at build time.
const _: () = assert!(MAX_PROOF_BYTES == av_zone::config::MAX_PROOF_BYTES);
const _: () = assert!(BINDING_LIMBS == av_zone::statement::BINDING_LIMBS);
const _: () = assert!(BABYBEAR_MODULUS == av_zone::statement::MODULUS);
const _: () = assert!(
    MAX_PUBLIC_VALUES >= av_zone::statement::NUM_PUBLIC_VALUES
);

pub fn serve_frame(f: &[u8]) -> u32 {
    if f.len() < HEADER || f[0] != WIRE_VERSION {
        return FAULT;
    }
    let vk: [u8; VKID_LEN] = f[1..1 + VKID_LEN].try_into().unwrap();
    let pl = u32::from_le_bytes(f[33..37].try_into().unwrap()) as usize;
    let rl = u32::from_le_bytes(f[37..41].try_into().unwrap()) as usize;
    if pl > MAX_PUB || rl > MAX_PROOF_BYTES || f.len() != HEADER + pl + rl {
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
    /// Returns null (0) for a size no valid frame can have, so the host can
    /// refuse cleanly instead of the guest aborting on a huge allocation.
    /// The buffer is leaked on purpose: the instance is discarded after one call.
    #[no_mangle]
    pub extern "C" fn alloc(len: usize) -> *mut u8 {
        if len == 0 || len > super::MAX_FRAME {
            return core::ptr::null_mut();
        }
        Box::leak(vec![0u8; len].into_boxed_slice()).as_mut_ptr()
    }

    /// # Safety
    /// `ptr..ptr+len` must be a region previously returned by `alloc` and
    /// filled by the host.
    #[no_mangle]
    pub unsafe extern "C" fn verify_frame(ptr: *const u8, len: usize) -> u32 {
        super::serve_frame(core::slice::from_raw_parts(ptr, len))
    }
}
