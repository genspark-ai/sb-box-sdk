//! # genspark-sbbox
//!
//! Rust SDK for [SB-Box](https://www.genspark.ai/sb-box) — cloud sandboxes that start in
//! about a second, keep their memory when frozen, and expose commands, files, a code
//! interpreter and terminals over one API key.
//!
//! ```no_run
//! use sbbox::{Client, CreateSandboxRequest, ExecOptions};
//!
//! # async fn demo() -> sbbox::Result<()> {
//! let client = Client::from_env()?; // SBBOX_API_KEY
//! let mut sb = client.sandboxes().create(&CreateSandboxRequest::base()).await?;
//! let out = sb.exec("echo hello", &ExecOptions::default()).await?;
//! println!("{}", out.stdout);
//! sb.files().write_text("/home/user/note.txt", "hi", None).await?;
//! sb.freeze(true).await?;
//! sb.resume(None).await?;
//! sb.kill().await?;
//! # Ok(()) }
//! ```
//!
//! Features: `blocking` (sync [`blocking::Client`]), `ws` (WebSocket PTY).

#![forbid(unsafe_code)]
#![warn(missing_docs, rust_2018_idioms)]
#![allow(missing_docs)] // field-level docs live in the API reference; keep the crate light

mod client;
pub mod error;
mod sandbox;
mod sandboxes;
pub mod sse;
mod templates;
mod transport;
pub mod types;

#[cfg(feature = "blocking")]
pub mod blocking;
#[cfg(feature = "ws")]
pub mod ws;

pub use client::{Client, ClientBuilder};
pub use error::{redact, ApiErrorBody, Error, LimitInfo, Result};
pub use sandbox::{
    BackgroundCommand, Command, Commands, EventStream, Files, Pty, PtySession, Sandbox,
    SeqEventStream,
};
pub use sandboxes::Sandboxes;
pub use templates::Templates;
pub use types::*;

/// Crate version, also sent in the `User-Agent`.
pub const VERSION: &str = env!("CARGO_PKG_VERSION");
