//! TEST HARNESS: serves the vsock request loop over TCP so the Go VsockTransport
//! can be exercised end to end without a VM. Usage: tcp_serve <addr> <image_hash_hex>
use pq_verifier_guest::server::{handle_connection, parse_hex32, Config};
#[tokio::main(flavor = "multi_thread", worker_threads = 2)]
async fn main() {
    let addr = std::env::args().nth(1).unwrap();
    let hash = parse_hex32(&std::env::args().nth(2).unwrap()).unwrap();
    let l = tokio::net::TcpListener::bind(&addr).await.unwrap();
    eprintln!("listening {addr}");
    loop {
        let (s, _) = l.accept().await.unwrap();
        let cfg = Config::new(hash);
        tokio::spawn(async move {
            if let Err(e) = handle_connection(s, &cfg).await { eprintln!("closed: {e}"); }
        });
    }
}
