//! Create a sandbox, run a command, write/read a file, freeze, resume, kill.
//!
//! ```sh
//! SBBOX_API_KEY=gsk-... cargo run --example quickstart
//! ```

use sbbox::{Client, CreateSandboxRequest, ExecOptions};

#[tokio::main]
async fn main() -> sbbox::Result<()> {
    let client = Client::from_env()?;

    let mut sb = client
        .sandboxes()
        .create(&CreateSandboxRequest::base().timeout(600))
        .await?;
    println!("sandbox {} is {}", sb.id(), sb.state());

    let out = sb
        .exec("uname -a && whoami", &ExecOptions::default())
        .await?;
    print!("{}", out.stdout);

    sb.files()
        .write_text("/home/user/hello.txt", "hello from rust\n", None)
        .await?;
    print!(
        "{}",
        sb.files().read_text("/home/user/hello.txt", None).await?
    );

    sb.freeze(true).await?;
    println!("frozen");
    sb.resume(None).await?;
    println!("resumed: {}", sb.state());

    sb.kill().await?;
    println!("killed");
    Ok(())
}
