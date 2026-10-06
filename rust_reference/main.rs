use std::{process, sync::Arc, time::Duration};

use pq_verifier_guest::server::{handle_connection, parse_hex32, Config};
use tokio::sync::Semaphore;
use tokio_vsock::{VsockAddr, VsockListener, VMADDR_CID_ANY, VMADDR_CID_HOST};

const PORT: u32 = 5001;
/// Bounds concurrent connections, and so concurrent verifications and buffers.
const MAX_CONNECTIONS: usize = 16;

fn main() {
    // Injected at image build time from the reproducible build's output.
    let image_hash = match std::env::var("VERIFIER_IMAGE_HASH").ok().as_deref().and_then(parse_hex32) {
        Some(h) => h,
        None => {
            eprintln!("VERIFIER_IMAGE_HASH must be 64 hex characters; refusing to start");
            process::exit(1);
        }
    };

    let rt = tokio::runtime::Builder::new_multi_thread()
        .worker_threads(2)
        .max_blocking_threads(MAX_CONNECTIONS)
        .enable_all()
        .build()
        .expect("runtime");

    if let Err(e) = rt.block_on(run(Config::new(image_hash))) {
        eprintln!("fatal: {e}");
        process::exit(1);
    }
}

async fn run(cfg: Config) -> Result<(), Box<dyn std::error::Error>> {
    let mut listener = VsockListener::bind(VsockAddr::new(VMADDR_CID_ANY, PORT))?;
    eprintln!("pq-verifier-guest listening on vsock port {PORT}");

    let slots = Arc::new(Semaphore::new(MAX_CONNECTIONS));
    let cfg = Arc::new(cfg);

    loop {
        // Wait for a free slot BEFORE accepting, so excess load queues in the
        // kernel backlog instead of piling up tasks and buffers here.
        let permit = slots.clone().acquire_owned().await?;
        let (stream, peer) = match listener.accept().await {
            Ok(x) => x,
            Err(e) => {
                eprintln!("accept failed: {e}");
                tokio::time::sleep(Duration::from_millis(100)).await;
                continue;
            }
        };
        // Only the host may talk to this service.
        if peer.cid() != VMADDR_CID_HOST {
            eprintln!("rejected connection from cid {}", peer.cid());
            continue;
        }

        let cfg = cfg.clone();
        tokio::spawn(async move {
            if let Err(e) = handle_connection(stream, &cfg).await {
                // Never log request contents, only the reason.
                eprintln!("connection closed: {e}");
            }
            drop(permit);
        });
    }
}
