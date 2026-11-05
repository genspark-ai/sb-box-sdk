//! The [`Sandbox`] handle: commands, code, files and PTY on one sandbox.

use std::collections::HashMap;
use std::pin::Pin;
use std::sync::Arc;
use std::time::Duration;

use bytes::Bytes;
use futures_util::{Stream, StreamExt, TryStreamExt};
use serde_json::{json, Value};

use crate::error::{Error, Result};
use crate::sandboxes::Sandboxes;
use crate::sse::{events, SseEvent};
use crate::transport::{Retry, Transport};
use crate::types::{
    CodeResult, CommandResult, ExecEvent, ExecOptions, FileEntry, MetricPoint, ProcessInfo,
    PtyOptions, RunCodeOptions, SandboxInfo,
};

/// Boxed event stream returned by streaming calls.
pub type EventStream = Pin<Box<dyn Stream<Item = Result<ExecEvent>> + Send>>;

/// A handle to one sandbox. Obtained from `client.sandboxes().create/get`.
#[derive(Debug, Clone)]
pub struct Sandbox {
    t: Arc<Transport>,
    info: SandboxInfo,
}

/// Argument accepted by [`Sandbox::exec`]: a `&str` runs via `/bin/sh -c`, a slice is argv.
pub enum Command<'a> {
    Shell(&'a str),
    Argv(&'a [&'a str]),
}

impl<'a> From<&'a str> for Command<'a> {
    fn from(s: &'a str) -> Self {
        Command::Shell(s)
    }
}
impl<'a> From<&'a [&'a str]> for Command<'a> {
    fn from(a: &'a [&'a str]) -> Self {
        Command::Argv(a)
    }
}
impl<'a, const N: usize> From<&'a [&'a str; N]> for Command<'a> {
    fn from(a: &'a [&'a str; N]) -> Self {
        Command::Argv(a)
    }
}
impl<'a> From<&'a Vec<&'a str>> for Command<'a> {
    fn from(a: &'a Vec<&'a str>) -> Self {
        Command::Argv(a)
    }
}

impl Command<'_> {
    fn argv(&self) -> Vec<String> {
        match self {
            Command::Shell(s) => vec!["/bin/sh".into(), "-c".into(), (*s).to_owned()],
            Command::Argv(a) => a.iter().map(|s| (*s).to_owned()).collect(),
        }
    }
}

fn exec_body(cmd: &Command<'_>, o: &ExecOptions, with_timeout: bool) -> Value {
    let mut b = json!({ "command": cmd.argv() });
    if let Some(c) = &o.cwd {
        b["cwd"] = json!(c);
    }
    if let Some(e) = &o.env {
        b["envVars"] = json!(e);
    }
    if let Some(u) = &o.user {
        b["user"] = json!(u);
    }
    if with_timeout {
        b["timeout"] = json!(o.timeout.unwrap_or(60));
    }
    b
}

fn to_exec_event(ev: SseEvent) -> Option<ExecEvent> {
    let d: Value = serde_json::from_str(&ev.data).unwrap_or(Value::Null);
    let s = |k: &str| d.get(k).and_then(Value::as_str).map(str::to_owned);
    Some(match ev.event.as_str() {
        "execId" => ExecEvent::Started {
            exec_id: s("execId").unwrap_or_default(),
        },
        "stdout" => ExecEvent::Stdout(s("data").unwrap_or_default()),
        "stderr" => ExecEvent::Stderr(s("data").unwrap_or_default()),
        "data" => ExecEvent::Data(s("data").unwrap_or_default()),
        "exit" => ExecEvent::Exit {
            exit_code: d.get("exitCode").and_then(Value::as_i64).unwrap_or(-1) as i32,
        },
        "error" => ExecEvent::Error {
            message: s("error").unwrap_or_else(|| ev.data.clone()),
            stage: s("stage"),
        },
        _ => return None,
    })
}

fn event_stream(resp: reqwest::Response) -> EventStream {
    Box::pin(
        events(resp.bytes_stream()).try_filter_map(|ev| std::future::ready(Ok(to_exec_event(ev)))),
    )
}

impl Sandbox {
    pub(crate) fn new(t: Arc<Transport>, info: SandboxInfo) -> Self {
        Self { t, info }
    }

    fn svc(&self) -> Sandboxes {
        Sandboxes { t: self.t.clone() }
    }
    fn p(&self, suffix: &str) -> String {
        format!("sandboxes/{}/{suffix}", self.info.id)
    }

    // ------------------------------------------------------------ record

    /// Sandbox id.
    pub fn id(&self) -> &str {
        &self.info.id
    }
    /// Last known state (call [`Sandbox::refresh`] to update).
    pub fn state(&self) -> &str {
        &self.info.state
    }
    /// The full record as last fetched.
    pub fn info(&self) -> &SandboxInfo {
        &self.info
    }
    /// Public URL for `port`.
    pub fn port_url(&self, port: u16) -> Option<String> {
        self.info.port_url(port)
    }

    /// Re-fetch the record.
    pub async fn refresh(&mut self) -> Result<&SandboxInfo> {
        self.info = self.t.get(&format!("sandboxes/{}", self.info.id)).await?;
        Ok(&self.info)
    }
    /// Kill the sandbox; every later call answers not-found.
    pub async fn kill(&self) -> Result<()> {
        self.svc().kill(&self.info.id).await
    }
    /// Freeze (see [`Sandboxes::freeze`]).
    pub async fn freeze(&mut self, wait: bool) -> Result<&SandboxInfo> {
        self.info = self.svc().freeze(&self.info.id, wait).await?;
        Ok(&self.info)
    }
    /// Resume a frozen sandbox.
    pub async fn resume(&mut self, timeout: Option<u64>) -> Result<&SandboxInfo> {
        self.info = self.svc().resume(&self.info.id, timeout).await?;
        Ok(&self.info)
    }
    /// Roll back to a saved version (`0` = newest).
    pub async fn rollback(&mut self, version: i64, timeout: Option<u64>) -> Result<&SandboxInfo> {
        self.info = self.svc().rollback(&self.info.id, version, timeout).await?;
        Ok(&self.info)
    }
    /// Clone into a new sandbox.
    pub async fn clone_sandbox(
        &self,
        version: i64,
        metadata: Option<&HashMap<String, String>>,
    ) -> Result<Sandbox> {
        self.svc()
            .clone_sandbox(&self.info.id, version, metadata)
            .await
    }
    /// Set / clear (`0`) the lifetime deadline, in seconds.
    pub async fn set_timeout(&self, seconds: u64) -> Result<()> {
        self.svc().set_timeout(&self.info.id, seconds).await
    }
    /// Reset (fresh boot from disk).
    pub async fn reset(&mut self) -> Result<&SandboxInfo> {
        self.info = self.svc().reset(&self.info.id).await?;
        Ok(&self.info)
    }
    /// Resource metrics (unix seconds range).
    pub async fn metrics(&self, start: Option<i64>, end: Option<i64>) -> Result<Vec<MetricPoint>> {
        self.svc().metrics(&self.info.id, start, end).await
    }

    // ------------------------------------------------------------ commands

    /// Run a command and wait. A non-zero exit code is returned, not raised;
    /// only a timeout raises ([`Error::CommandTimeout`]).
    pub async fn exec<'a>(
        &self,
        cmd: impl Into<Command<'a>>,
        opts: &ExecOptions,
    ) -> Result<CommandResult> {
        let body = exec_body(&cmd.into(), opts, true);
        let timeout = Duration::from_secs(opts.timeout.unwrap_or(60) + 30);
        self.t
            .post(&self.p("exec"), &body, Retry::Never, timeout)
            .await
    }

    /// Run a command and stream its output as [`ExecEvent`]s.
    pub async fn exec_stream<'a>(
        &self,
        cmd: impl Into<Command<'a>>,
        opts: &ExecOptions,
    ) -> Result<EventStream> {
        let body =
            serde_json::to_vec(&exec_body(&cmd.into(), opts, true)).map_err(Error::transport)?;
        let timeout = Duration::from_secs(opts.timeout.unwrap_or(60) + 30);
        let prepared = self
            .t
            .execute(Retry::Never, timeout, || {
                self.t
                    .builder(reqwest::Method::POST, &self.p("sse/exec"), timeout)
                    .header(reqwest::header::CONTENT_TYPE, "application/json")
                    .header(reqwest::header::ACCEPT, "text/event-stream")
                    .body(body.clone())
            })
            .await?;
        Ok(event_stream(prepared.response))
    }

    /// Run code in the stateful interpreter (public base template).
    pub async fn run_code(&self, code: &str, opts: &RunCodeOptions) -> Result<CodeResult> {
        let mut b = json!({
            "code": code,
            "language": opts.language.as_deref().unwrap_or("python"),
            "timeout": opts.timeout.unwrap_or(60),
        });
        if let Some(c) = &opts.context_id {
            b["contextId"] = json!(c);
        }
        if let Some(e) = &opts.env {
            b["envVars"] = json!(e);
        }
        if let Some(i) = &opts.images {
            b["images"] = json!(i);
        }
        let timeout = Duration::from_secs(opts.timeout.unwrap_or(60) + 30);
        self.t
            .post(&self.p("run-code"), &b, Retry::Never, timeout)
            .await
    }

    /// Background commands.
    pub fn commands(&self) -> Commands<'_> {
        Commands { sb: self }
    }
    /// Files.
    pub fn files(&self) -> Files<'_> {
        Files { sb: self }
    }
    /// Pseudo-terminals.
    pub fn pty(&self) -> Pty<'_> {
        Pty { sb: self }
    }

    async fn ok_post<T: serde::de::DeserializeOwned>(
        &self,
        suffix: &str,
        body: &Value,
    ) -> Result<T> {
        self.t
            .post(&self.p(suffix), body, Retry::Never, self.t.timeout)
            .await
    }

    async fn sse_get(&self, suffix: &str) -> Result<EventStream> {
        let timeout = Duration::from_secs(24 * 3600);
        let prepared = self
            .t
            .execute(Retry::Never, timeout, || {
                self.t
                    .builder(reqwest::Method::GET, &self.p(suffix), timeout)
                    .header(reqwest::header::ACCEPT, "text/event-stream")
            })
            .await?;
        Ok(event_stream(prepared.response))
    }
}

/// `sandbox.commands()`.
pub struct Commands<'s> {
    sb: &'s Sandbox,
}

impl Commands<'_> {
    /// `POST commands/start`.
    pub async fn start<'a>(
        &self,
        cmd: impl Into<Command<'a>>,
        opts: &ExecOptions,
    ) -> Result<BackgroundCommand> {
        let body = exec_body(&cmd.into(), opts, false);
        let info: ProcessInfo = self.sb.ok_post("commands/start", &body).await?;
        Ok(BackgroundCommand {
            sb: self.sb.clone(),
            info,
        })
    }
    /// `GET commands`.
    pub async fn list(&self) -> Result<Vec<ProcessInfo>> {
        self.sb.t.get(&self.sb.p("commands")).await
    }
    /// `POST commands/kill`.
    pub async fn kill(&self, pid: i64, signal: Option<i32>) -> Result<()> {
        let _: Value = self
            .sb
            .ok_post(
                "commands/kill",
                &json!({ "pid": pid, "signal": signal.unwrap_or(9) }),
            )
            .await?;
        Ok(())
    }
    /// `POST commands/send-stdin`.
    pub async fn send_stdin(&self, pid: i64, data: &str) -> Result<()> {
        let _: Value = self
            .sb
            .ok_post("commands/send-stdin", &json!({ "pid": pid, "data": data }))
            .await?;
        Ok(())
    }
}

/// A running background command.
#[derive(Debug, Clone)]
pub struct BackgroundCommand {
    sb: Sandbox,
    info: ProcessInfo,
}

impl BackgroundCommand {
    pub fn exec_id(&self) -> &str {
        &self.info.exec_id
    }
    pub fn pid(&self) -> Option<i64> {
        self.info.pid
    }
    /// Write to stdin. Returns bytes written.
    pub async fn input(&self, data: &str) -> Result<u64> {
        let v: Value = self
            .sb
            .ok_post(
                &format!("commands/{}/input", self.info.exec_id),
                &json!({ "data": data }),
            )
            .await?;
        Ok(v.get("written").and_then(Value::as_u64).unwrap_or(0))
    }
    /// Close stdin.
    pub async fn close(&self) -> Result<()> {
        let _: Value = self
            .sb
            .ok_post(&format!("commands/{}/close", self.info.exec_id), &json!({}))
            .await?;
        Ok(())
    }
    /// Block until exit; returns the exit code.
    pub async fn wait(&self) -> Result<i32> {
        let timeout = Duration::from_secs(24 * 3600);
        let v: Value = self
            .sb
            .t
            .post(
                &self.sb.p(&format!("commands/{}/wait", self.info.exec_id)),
                &json!({}),
                Retry::Never,
                timeout,
            )
            .await?;
        Ok(v.get("exitCode").and_then(Value::as_i64).unwrap_or(-1) as i32)
    }
    /// Kill by pid (`signal` default 9).
    pub async fn kill(&self, signal: Option<i32>) -> Result<()> {
        match self.info.pid {
            Some(pid) => self.sb.commands().kill(pid, signal).await,
            None => Err(Error::Config("background command has no pid".into())),
        }
    }
    /// Experimental: attach to the command's output stream (SSE).
    ///
    /// Not part of the documented SB-Box contract (the user guide states background-command
    /// output is not streamed); the endpoint may change or disappear without notice.
    pub async fn connect(&self) -> Result<EventStream> {
        self.sb
            .sse_get(&format!("commands/{}/connect", self.info.exec_id))
            .await
    }
}

/// `sandbox.files()`.
pub struct Files<'s> {
    sb: &'s Sandbox,
}

fn q(path: &str, user: Option<&str>) -> String {
    let mut s = format!("path={}", urlenc(path));
    if let Some(u) = user {
        s.push_str(&format!("&user={}", urlenc(u)));
    }
    s
}

fn urlenc(s: &str) -> String {
    url::form_urlencoded::byte_serialize(s.as_bytes()).collect()
}

impl Files<'_> {
    /// Write raw bytes to `path`. Returns bytes written.
    pub async fn write(
        &self,
        path: &str,
        data: impl Into<Bytes>,
        user: Option<&str>,
    ) -> Result<u64> {
        let data: Bytes = data.into();
        let timeout = Duration::from_secs(600);
        let suffix = format!("files/stream?{}", q(path, user));
        let prepared = self
            .sb
            .t
            .execute(Retry::Never, timeout, || {
                self.sb
                    .t
                    .builder(reqwest::Method::POST, &self.sb.p(&suffix), timeout)
                    .header(reqwest::header::CONTENT_TYPE, "application/octet-stream")
                    .body(data.clone())
            })
            .await?;
        let v: Value = crate::transport::decode(prepared.response).await?;
        Ok(v.get("written")
            .and_then(Value::as_u64)
            .unwrap_or(data.len() as u64))
    }

    /// Write a UTF-8 string.
    pub async fn write_text(&self, path: &str, text: &str, user: Option<&str>) -> Result<u64> {
        self.write(path, Bytes::copy_from_slice(text.as_bytes()), user)
            .await
    }

    /// Read `path` as bytes.
    pub async fn read(&self, path: &str, user: Option<&str>) -> Result<Bytes> {
        let timeout = Duration::from_secs(600);
        let suffix = format!("files/stream?{}", q(path, user));
        let prepared = self
            .sb
            .t
            .execute(Retry::Idempotent, timeout, || {
                self.sb
                    .t
                    .builder(reqwest::Method::GET, &self.sb.p(&suffix), timeout)
            })
            .await?;
        prepared.response.bytes().await.map_err(Error::transport)
    }

    /// Read `path` as UTF-8 text (lossy).
    pub async fn read_text(&self, path: &str, user: Option<&str>) -> Result<String> {
        Ok(String::from_utf8_lossy(&self.read(path, user).await?).into_owned())
    }

    /// Stream `path` as a byte stream.
    pub async fn read_stream(
        &self,
        path: &str,
        user: Option<&str>,
    ) -> Result<Pin<Box<dyn Stream<Item = Result<Bytes>> + Send>>> {
        let timeout = Duration::from_secs(24 * 3600);
        let suffix = format!("files/stream?{}", q(path, user));
        let prepared = self
            .sb
            .t
            .execute(Retry::Idempotent, timeout, || {
                self.sb
                    .t
                    .builder(reqwest::Method::GET, &self.sb.p(&suffix), timeout)
            })
            .await?;
        Ok(Box::pin(
            prepared
                .response
                .bytes_stream()
                .map(|r| r.map_err(Error::transport)),
        ))
    }

    /// List a directory (`[]` for a missing one).
    pub async fn list(&self, path: &str, user: Option<&str>) -> Result<Vec<FileEntry>> {
        self.sb
            .t
            .get(&self.sb.p(&format!("files/list?{}", q(path, user))))
            .await
    }
}

/// `sandbox.pty()`.
pub struct Pty<'s> {
    sb: &'s Sandbox,
}

impl Pty<'_> {
    /// Open a PTY.
    pub async fn open(&self, opts: &PtyOptions) -> Result<PtySession> {
        let mut b = json!({
            "command": opts.command.clone().unwrap_or_else(|| vec!["/bin/bash".into()]),
            "rows": opts.rows.unwrap_or(24),
            "cols": opts.cols.unwrap_or(80),
        });
        if let Some(c) = &opts.cwd {
            b["cwd"] = json!(c);
        }
        if let Some(e) = &opts.env {
            b["envVars"] = json!(e);
        }
        let info: ProcessInfo = self.sb.ok_post("pty", &b).await?;
        Ok(PtySession {
            sb: self.sb.clone(),
            info,
        })
    }

    /// WebSocket URL for `GET /sandboxes/{id}/ws/pty` (auth via `X-Api-Key` header or `?api_key=`).
    pub fn ws_url(&self) -> String {
        let http = self.sb.t.url(&self.sb.p("ws/pty"));
        http.replacen("https://", "wss://", 1)
            .replacen("http://", "ws://", 1)
    }

    /// Open the WebSocket PTY (feature `ws`).
    #[cfg(feature = "ws")]
    pub async fn connect_ws(&self, opts: &PtyOptions) -> Result<crate::ws::WsPty> {
        crate::ws::WsPty::connect(self.sb.t.clone(), &self.ws_url(), opts).await
    }
}

/// An open PTY.
#[derive(Debug, Clone)]
pub struct PtySession {
    sb: Sandbox,
    info: ProcessInfo,
}

impl PtySession {
    pub fn exec_id(&self) -> &str {
        &self.info.exec_id
    }
    pub fn pid(&self) -> Option<i64> {
        self.info.pid
    }
    /// Send raw input bytes.
    pub async fn input(&self, data: impl Into<Bytes>) -> Result<()> {
        let data: Bytes = data.into();
        let suffix = format!("pty/{}/input", self.info.exec_id);
        let t = self.sb.t.timeout;
        let prepared = self
            .sb
            .t
            .execute(Retry::Never, t, || {
                self.sb
                    .t
                    .builder(reqwest::Method::POST, &self.sb.p(&suffix), t)
                    .header(reqwest::header::CONTENT_TYPE, "application/octet-stream")
                    .body(data.clone())
            })
            .await?;
        let _: Value = crate::transport::decode(prepared.response).await?;
        Ok(())
    }
    /// Resize.
    pub async fn resize(&self, rows: u16, cols: u16) -> Result<()> {
        let _: Value = self
            .sb
            .ok_post(
                &format!("pty/{}/resize", self.info.exec_id),
                &json!({ "rows": rows, "cols": cols }),
            )
            .await?;
        Ok(())
    }
    /// Kill.
    pub async fn kill(&self) -> Result<()> {
        let _: Value = self
            .sb
            .ok_post(&format!("pty/{}/kill", self.info.exec_id), &json!({}))
            .await?;
        Ok(())
    }
    /// Attach to output ([`ExecEvent::Data`] / [`ExecEvent::Exit`]).
    pub async fn connect(&self) -> Result<EventStream> {
        self.sb
            .sse_get(&format!("pty/{}/connect", self.info.exec_id))
            .await
    }
}
