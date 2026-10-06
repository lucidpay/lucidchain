//! Per-connection request loop, generic over the stream so it can be tested
//! without vsock.

use std::time::Duration;

use tokio::io::{AsyncRead, AsyncWrite};
use tokio::task::spawn_blocking;
use tokio::time::timeout;

use crate::protocol::{read_request, write_response, HASH_LEN};
use crate::verify::verify;

#[derive(Clone)]
pub struct Config {
    /// Pinned hash reported with every verdict. Self-reported, so it only
    /// catches accidental mismatches; the real anchor is the host measuring
    /// kernel and rootfs before boot.
    pub image_hash: [u8; HASH_LEN],
    /// Max wait for the next request on an open connection.
    pub idle_timeout: Duration,
    /// Max time to read the rest of a request / write a response.
    pub io_timeout: Duration,
    /// Max time to wait for one verification.
    pub verify_timeout: Duration,
}

impl Config {
    pub fn new(image_hash: [u8; HASH_LEN]) -> Self {
        Self {
            image_hash,
            idle_timeout: Duration::from_secs(600),
            io_timeout: Duration::from_secs(5),
            verify_timeout: Duration::from_secs(30),
        }
    }
}

/// Serves requests until the peer closes cleanly (`Ok`) or anything goes wrong
/// (`Err`). On `Err` the caller drops the stream without sending a response,
/// which the host treats as a verifier fault. A panic, a timeout or an unknown
/// vk_id is never converted into a verdict.
pub async fn handle_connection<S>(mut stream: S, cfg: &Config) -> Result<(), String>
where
    S: AsyncRead + AsyncWrite + Unpin,
{
    loop {
        let req = match read_request(&mut stream, cfg.idle_timeout, cfg.io_timeout).await {
            Ok(Some(req)) => req,
            Ok(None) => return Ok(()),
            Err(e) => return Err(format!("protocol: {e}")),
        };

        // Verification is CPU-bound: keep it off the async workers.
        let job = spawn_blocking(move || verify(&req.vk_id, &req.public_inputs, &req.proof));
        let verdict = match timeout(cfg.verify_timeout, job).await {
            Err(_) => return Err("verification timed out".into()),
            Ok(Err(join)) => return Err(format!("verifier task failed: {join}")),
            Ok(Ok(Err(e))) => return Err(format!("verifier fault: {e:?}")),
            Ok(Ok(Ok(v))) => v,
        };

        write_response(&mut stream, verdict.status(), &cfg.image_hash, cfg.io_timeout)
            .await
            .map_err(|e| format!("protocol: {e}"))?;
    }
}

/// Parses 64 hex characters into 32 bytes.
pub fn parse_hex32(s: &str) -> Option<[u8; HASH_LEN]> {
    let s = s.trim();
    if s.len() != 2 * HASH_LEN || !s.is_ascii() {
        return None;
    }
    let mut out = [0u8; HASH_LEN];
    for (i, b) in out.iter_mut().enumerate() {
        *b = u8::from_str_radix(&s[2 * i..2 * i + 2], 16).ok()?;
    }
    Some(out)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::protocol::{VERSION, VKID_LEN};
    use tokio::io::{duplex, AsyncReadExt, AsyncWriteExt};

    fn request(proof: &[u8]) -> Vec<u8> {
        let mut pubs = 16u32.to_le_bytes().to_vec();
        pubs.extend_from_slice(&[0u8; 64]);
        let mut v = vec![VERSION];
        v.extend_from_slice(&[1u8; VKID_LEN]);
        v.extend_from_slice(&(pubs.len() as u32).to_le_bytes());
        v.extend_from_slice(&(proof.len() as u32).to_le_bytes());
        v.extend_from_slice(&pubs);
        v.extend_from_slice(proof);
        v
    }

    #[test]
    fn hex_parsing() {
        assert_eq!(parse_hex32(&"ab".repeat(32)), Some([0xab; 32]));
        assert!(parse_hex32("").is_none());
        assert!(parse_hex32(&"zz".repeat(32)).is_none());
        assert!(parse_hex32(&"ab".repeat(31)).is_none());
        assert!(parse_hex32(&"ab".repeat(33)).is_none());
    }

    #[cfg(not(feature = "insecure-accept-all"))]
    #[tokio::test]
    async fn default_build_never_answers() {
        let (mut client, server) = duplex(1 << 16);
        client.write_all(&request(&[9, 9, 9])).await.unwrap();
        let cfg = Config::new([0xAA; 32]);
        let res = handle_connection(server, &cfg).await;
        assert!(res.is_err(), "must fail closed");
        let mut buf = Vec::new();
        client.read_to_end(&mut buf).await.unwrap();
        assert!(buf.is_empty(), "no response byte may be sent on a fault");
    }

    #[cfg(feature = "insecure-accept-all")]
    #[tokio::test]
    async fn mock_answers_and_serves_many_requests() {
        let (mut client, server) = duplex(1 << 16);
        let cfg = Config::new([0xAA; 32]);
        let task = tokio::spawn(async move { handle_connection(server, &cfg).await });

        for _ in 0..3 {
            client.write_all(&request(&[9, 9, 9])).await.unwrap();
            let mut resp = [0u8; 33];
            client.read_exact(&mut resp).await.unwrap();
            assert_eq!(resp[0], crate::protocol::STATUS_VALID);
            assert_eq!(&resp[1..], &[0xAA; 32]);
        }
        drop(client);
        assert!(task.await.unwrap().is_ok(), "clean close is not an error");
    }
}
