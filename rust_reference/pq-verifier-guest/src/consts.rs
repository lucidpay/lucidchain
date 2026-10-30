//! Constants shared by the verifier core and the wasm framing layer.
pub const VKID_LEN: usize = 32;
pub const MAX_PROOF_BYTES: usize = 512 * 1024;
pub const STATUS_VALID: u8 = 0;
pub const STATUS_INVALID: u8 = 1;
pub const STATUS_MALFORMED: u8 = 2;
