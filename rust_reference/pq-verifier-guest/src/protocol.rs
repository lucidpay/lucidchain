//! Wire protocol, version 1. See WIRE_SPEC.md; the Go adapter in
//! x/proofs/verifiers/pq.go must agree with every constant here.
//!
//! Request:  version u8 | vk_id [32] | pub_len u32 LE | proof_len u32 LE | pub | proof
//! Response: status u8 | image_hash [32] | digest [16]
//!           digest = first 16 bytes of SHA-256 over the complete request frame
//!
//! Any framing violation (bad version, length above its cap, short read,
//! timeout) is a connection-level error: the caller closes the connection
//! without answering, which the host treats as a verifier fault. Lengths are
//! checked against their caps BEFORE any buffer is allocated.

use std::{fmt, io, time::Duration};

use tokio::io::{AsyncRead, AsyncReadExt, AsyncWrite, AsyncWriteExt};
use tokio::time::timeout;

pub const VERSION: u8 = 2;
pub use crate::consts::{MAX_PROOF_BYTES, STATUS_INVALID, STATUS_MALFORMED, STATUS_VALID, VKID_LEN};
pub const HASH_LEN: usize = 32;
/// Length of the request digest echoed in every response (stream-desync check).
pub const DIGEST_LEN: usize = 16;

/// 4-byte count + up to 64 four-byte field elements (matches maxPQPublicValues in Go).
pub const MAX_PUBLIC_INPUT_BYTES: usize = 4 + 4 * 64;


#[derive(Debug)]
pub struct Request {
    pub vk_id: [u8; VKID_LEN],
    pub public_inputs: Vec<u8>,
    pub proof: Vec<u8>,
}

#[derive(Debug)]
pub enum ProtocolError {
    Io(io::Error),
    Timeout,
    BadVersion(u8),
    PublicInputsTooLarge(u32),
    ProofTooLarge(u32),
}

impl fmt::Display for ProtocolError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Io(e) => write!(f, "io error: {e}"),
            Self::Timeout => write!(f, "timeout"),
            Self::BadVersion(v) => write!(f, "unsupported protocol version {v}"),
            Self::PublicInputsTooLarge(n) => write!(f, "public inputs length {n} exceeds cap"),
            Self::ProofTooLarge(n) => write!(f, "proof length {n} exceeds cap"),
        }
    }
}

impl std::error::Error for ProtocolError {}

impl From<io::Error> for ProtocolError {
    fn from(e: io::Error) -> Self {
        Self::Io(e)
    }
}

impl Request {
    /// First DIGEST_LEN bytes of SHA-256 over the exact request frame as sent
    /// (version | vk_id | pub_len | proof_len | pub | proof). The host recomputes
    /// it and treats a mismatch as a stream desync.
    pub fn digest(&self) -> [u8; DIGEST_LEN] {
        use sha2::{Digest, Sha256};
        let mut h = Sha256::new();
        h.update([VERSION]);
        h.update(self.vk_id);
        h.update((self.public_inputs.len() as u32).to_le_bytes());
        h.update((self.proof.len() as u32).to_le_bytes());
        h.update(&self.public_inputs);
        h.update(&self.proof);
        let full = h.finalize();
        let mut out = [0u8; DIGEST_LEN];
        out.copy_from_slice(&full[..DIGEST_LEN]);
        out
    }
}

/// Reads one request. Returns `Ok(None)` on a clean close between requests.
/// `idle` bounds the wait for the first byte; `io_timeout` bounds the rest of
/// the request once it has started.
pub async fn read_request<R: AsyncRead + Unpin>(
    r: &mut R,
    idle: Duration,
    io_timeout: Duration,
) -> Result<Option<Request>, ProtocolError> {
    let mut version = [0u8; 1];
    match timeout(idle, r.read(&mut version)).await {
        Err(_) => return Err(ProtocolError::Timeout),
        Ok(Err(e)) => return Err(e.into()),
        Ok(Ok(0)) => return Ok(None),
        Ok(Ok(_)) => {}
    }
    if version[0] != VERSION {
        return Err(ProtocolError::BadVersion(version[0]));
    }
    match timeout(io_timeout, read_rest(r)).await {
        Err(_) => Err(ProtocolError::Timeout),
        Ok(res) => res.map(Some),
    }
}

async fn read_rest<R: AsyncRead + Unpin>(r: &mut R) -> Result<Request, ProtocolError> {
    let mut vk_id = [0u8; VKID_LEN];
    r.read_exact(&mut vk_id).await?;

    let mut lens = [0u8; 8];
    r.read_exact(&mut lens).await?;
    let pub_len = u32::from_le_bytes(lens[0..4].try_into().unwrap());
    let proof_len = u32::from_le_bytes(lens[4..8].try_into().unwrap());

    // Validate before allocating: never size a buffer from an unchecked length.
    if pub_len as usize > MAX_PUBLIC_INPUT_BYTES {
        return Err(ProtocolError::PublicInputsTooLarge(pub_len));
    }
    if proof_len as usize > MAX_PROOF_BYTES {
        return Err(ProtocolError::ProofTooLarge(proof_len));
    }

    let mut public_inputs = vec![0u8; pub_len as usize];
    r.read_exact(&mut public_inputs).await?;
    let mut proof = vec![0u8; proof_len as usize];
    r.read_exact(&mut proof).await?;

    Ok(Request { vk_id, public_inputs, proof })
}

pub async fn write_response<W: AsyncWrite + Unpin>(
    w: &mut W,
    status: u8,
    image_hash: &[u8; HASH_LEN],
    digest: &[u8; DIGEST_LEN],
    io_timeout: Duration,
) -> Result<(), ProtocolError> {
    let mut out = [0u8; 1 + HASH_LEN + DIGEST_LEN];
    out[0] = status;
    out[1..1 + HASH_LEN].copy_from_slice(image_hash);
    out[1 + HASH_LEN..].copy_from_slice(digest);
    match timeout(io_timeout, async {
        w.write_all(&out).await?;
        w.flush().await
    })
    .await
    {
        Err(_) => Err(ProtocolError::Timeout),
        Ok(res) => res.map_err(Into::into),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use tokio::io::duplex;

    const T: Duration = Duration::from_secs(2);

    fn frame(version: u8, vk: u8, pub_len: u32, proof_len: u32, body: &[u8]) -> Vec<u8> {
        let mut v = vec![version];
        v.extend_from_slice(&[vk; VKID_LEN]);
        v.extend_from_slice(&pub_len.to_le_bytes());
        v.extend_from_slice(&proof_len.to_le_bytes());
        v.extend_from_slice(body);
        v
    }

    async fn parse(bytes: Vec<u8>) -> Result<Option<Request>, ProtocolError> {
        let (mut a, mut b) = duplex(1 << 20);
        a.write_all(&bytes).await.unwrap();
        drop(a);
        read_request(&mut b, T, T).await
    }

    #[tokio::test]
    async fn parses_valid_request() {
        let req = parse(frame(VERSION, 7, 3, 2, &[1, 2, 3, 9, 8])).await.unwrap().unwrap();
        assert_eq!(req.vk_id, [7u8; VKID_LEN]);
        assert_eq!(req.public_inputs, vec![1, 2, 3]);
        assert_eq!(req.proof, vec![9, 8]);
    }

    #[test]
    fn digest_matches_known_sha256_of_the_frame() {
        // Self-consistency only (same crate on both sides). The independent check is
        // the Go VsockTransport integration test, which recomputes the digest itself.
        let req = Request { vk_id: [7; VKID_LEN], public_inputs: vec![1, 2, 3], proof: vec![9, 8] };
        let frame = frame(VERSION, 7, 3, 2, &[1, 2, 3, 9, 8]);
        assert_eq!(frame.len(), 1 + 32 + 8 + 5);
        let expect = {
            use sha2::{Digest, Sha256};
            let d = Sha256::digest(&frame);
            let mut o = [0u8; DIGEST_LEN];
            o.copy_from_slice(&d[..DIGEST_LEN]);
            o
        };
        assert_eq!(req.digest(), expect);
    }

    #[tokio::test]
    async fn clean_eof_is_none() {
        assert!(parse(vec![]).await.unwrap().is_none());
    }

    #[tokio::test]
    async fn rejects_bad_version() {
        assert!(matches!(
            parse(frame(VERSION - 1, 0, 0, 0, &[])).await,
            Err(ProtocolError::BadVersion(v)) if v == VERSION - 1
        ));
    }

    #[tokio::test]
    async fn rejects_oversize_without_reading_body() {
        // No body bytes follow: if the cap were checked after allocation or
        // after reading, this would surface as UnexpectedEof instead.
        assert!(matches!(
            parse(frame(VERSION, 0, 0, MAX_PROOF_BYTES as u32 + 1, &[])).await,
            Err(ProtocolError::ProofTooLarge(_))
        ));
        assert!(matches!(
            parse(frame(VERSION, 0, MAX_PUBLIC_INPUT_BYTES as u32 + 1, 0, &[])).await,
            Err(ProtocolError::PublicInputsTooLarge(_))
        ));
        assert!(matches!(
            parse(frame(VERSION, 0, 0, u32::MAX, &[])).await,
            Err(ProtocolError::ProofTooLarge(_))
        ));
    }

    #[tokio::test]
    async fn rejects_truncated_body() {
        assert!(matches!(
            parse(frame(VERSION, 0, 4, 4, &[1, 2, 3])).await,
            Err(ProtocolError::Io(_))
        ));
    }

    #[tokio::test]
    async fn stalled_peer_times_out() {
        let (_keep_open, mut b) = duplex(64);
        let r = read_request(&mut b, Duration::from_millis(50), Duration::from_millis(50)).await;
        assert!(matches!(r, Err(ProtocolError::Timeout)));
    }
}
