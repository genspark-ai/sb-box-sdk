//! End-to-end smoke against a real environment.
//!
//! ```sh
//! SBBOX_API_KEY=gsk-... [SBBOX_BASE_URL=...] cargo run --example smoke
//! ```
//! Prints one line per step; never prints the key.

use std::time::Instant;

use futures_util::StreamExt;
use sbbox::{Client, CreateSandboxRequest, ExecEvent, ExecOptions, RunCodeOptions};

fn step(name: &str, t: Instant) {
    println!("[ok] {name:<14} {:>6} ms", t.elapsed().as_millis());
}

#[tokio::main]
async fn main() {
    if let Err(e) = run().await {
        eprintln!("[fail] {}", sbbox::redact(&e.to_string()));
        std::process::exit(1);
    }
}

async fn run() -> sbbox::Result<()> {
    let client = Client::from_env()?;
    println!("base_url {}", client.base_url());

    let t = Instant::now();
    let limits = client.limits().await?;
    println!(
        "plan={:?} running={:?}",
        limits.plan, limits.usage.running_sandboxes
    );
    step("limits", t);

    let t = Instant::now();
    let mut sb = client
        .sandboxes()
        .create(&CreateSandboxRequest::base().timeout(900))
        .await?;
    println!("id={} state={}", sb.id(), sb.state());
    step("create", t);

    let t = Instant::now();
    let out = sb.exec("echo smoke-ok", &ExecOptions::default()).await?;
    assert_eq!(out.stdout.trim(), "smoke-ok", "exec stdout");
    assert_eq!(out.exit_code, 0);
    step("exec", t);

    let t = Instant::now();
    let mut s = sb
        .exec_stream(
            "for i in 1 2 3; do echo line$i; done",
            &ExecOptions::default(),
        )
        .await?;
    let mut lines = 0;
    while let Some(ev) = s.next().await {
        match ev? {
            ExecEvent::Stdout(_) => lines += 1,
            ExecEvent::Exit { exit_code } => assert_eq!(exit_code, 0),
            ExecEvent::Error { message, .. } => panic!("stream error: {message}"),
            _ => {}
        }
    }
    assert!(lines >= 1, "streamed stdout");
    step("exec_stream", t);

    let t = Instant::now();
    let payload: Vec<u8> = (0..=255u8).cycle().take(4096).collect();
    sb.files()
        .write("/home/user/blob.bin", payload.clone(), None)
        .await?;
    let back = sb.files().read("/home/user/blob.bin", None).await?;
    assert_eq!(&back[..], &payload[..], "file round trip");
    let listing = sb.files().list("/home/user", None).await?;
    assert!(listing.iter().any(|f| f.name == "blob.bin"));
    step("files", t);

    let t = Instant::now();
    let r = sb.run_code("1 + 1", &RunCodeOptions::default()).await?;
    assert_eq!(r.text().map(str::trim), Some("2"), "run_code result");
    step("run_code", t);

    let t = Instant::now();
    sb.freeze(true).await?;
    assert_eq!(sb.state(), "frozen");
    step("freeze", t);

    let t = Instant::now();
    sb.resume(None).await?;
    assert_eq!(sb.state(), "running");
    let out = sb
        .exec("cat /home/user/blob.bin | wc -c", &ExecOptions::default())
        .await?;
    assert_eq!(out.stdout.trim(), "4096", "file survives freeze");
    step("resume", t);

    let t = Instant::now();
    sb.kill().await?;
    match client.sandboxes().get(sb.id()).await {
        Err(e) if e.is_not_found() => {}
        other => panic!("expected not-found after kill, got {other:?}"),
    }
    step("kill", t);

    println!("smoke passed");
    Ok(())
}
