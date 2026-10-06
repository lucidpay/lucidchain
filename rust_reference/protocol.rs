//! Wire protocol, version 1. See WIRE_SPEC.md; the Go adapter in
//! x/proofs/verifiers/pq.go must agree with every constant here.
//!
//! Request:  version u8 | vk_id [32] | pub_len u32 LE | proof_len u32 LE | pub | proof
//! Response: status u8 | image_hash [32]
//!
//! Any framing violation (bad version, length above its cap, short read,
//! timeout) is a connection-level error: the caller closes the connection
//! without answering, which the host treats as a verifier fault. Lengths are
//! checked against their caps BEFORE any buffer is allocated.

use std::{fmt, io, time::Duration};

use tokio::io::{AsyncRead, AsyncReadExt, AsyncWrite, AsyncWriteExt};
use tokio::time::timeout;

pub const VERSION: u8 = 1;
pub const VKID_LEN: usize = 32;
pub const HASH_LEN: usize = 32;

/// 4-byte count + up to 64 four-byte field elements (matches maxPQPublicValues in Go).
pub const MAX_PUBLIC_INPUT_BYTES: usize = 4 + 4 * 64;
/// Matches the maxProofBytes the Go adapter is constructed with.
pub const MAX_PROOF_BYTES: usize = 512 * 1024;

pub const STATUS_VALID: u8 = 0;
pub const STATUS_INVALID: u8 = 1;
pub const STATUS_MALFORMED: u8 = 2;

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
    io_timeout: Duration,
) -> Result<(), ProtocolError> {
    let mut out = [0u8; 1 + HASH_LEN];
    out[0] = status;
    out[1..].copy_from_slice(image_hash);
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

    #[tokio::test]
    async fn clean_eof_is_none() {
        assert!(parse(vec![]).await.unwrap().is_none());
    }

    #[tokio::test]
    async fn rejects_bad_version() {
        assert!(matches!(
            parse(frame(2, 0, 0, 0, &[])).await,
            Err(ProtocolError::BadVersion(2))
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
