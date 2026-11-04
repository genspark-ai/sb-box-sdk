//! Synchronous wrapper (`feature = "blocking"`). Each call runs the async client on a
//! private Tokio runtime. Do not use from inside an async context.

use std::sync::Arc;
use std::time::Duration;

use bytes::Bytes;
use futures_util::StreamExt;

use crate::error::{Error, Result};
use crate::types::*;

/// Sync client.
#[derive(Clone)]
pub struct Client {
    inner: crate::Client,
    rt: Arc<tokio::runtime::Runtime>,
}

impl Client {
    /// Wrap an async client.
    pub fn from_async(inner: crate::Client) -> Result<Self> {
        let rt = tokio::runtime::Builder::new_multi_thread()
            .worker_threads(2)
            .enable_all()
            .build()
            .map_err(|e| Error::Config(format!("runtime: {e}")))?;
        Ok(Self {
            inner,
            rt: Arc::new(rt),
        })
    }
    /// Build from a [`crate::ClientBuilder`].
    pub fn build(b: crate::ClientBuilder) -> Result<Self> {
        Self::from_async(b.build()?)
    }
    /// From the environment.
    pub fn from_env() -> Result<Self> {
        Self::from_async(crate::Client::from_env()?)
    }
    /// With an explicit key.
    pub fn new(api_key: impl Into<String>) -> Result<Self> {
        Self::from_async(crate::Client::new(api_key)?)
    }

    fn run<F: std::future::Future>(&self, f: F) -> F::Output {
        self.rt.block_on(f)
    }

    pub fn regions(&self) -> Result<Regions> {
        self.run(self.inner.regions())
    }
    pub fn limits(&self) -> Result<Limits> {
        self.run(self.inner.limits())
    }
    pub fn usage(&self, days: u32) -> Result<Usage> {
        self.run(self.inner.usage(days))
    }

    // templates
    pub fn build_template(&self, req: &TemplateBuildRequest) -> Result<TemplateBuild> {
        self.run(self.inner.templates().build(req))
    }
    pub fn wait_template_ready(&self, id: &str, timeout: Option<Duration>) -> Result<Template> {
        self.run(self.inner.templates().wait_ready(id, timeout, None))
    }
    pub fn list_templates(&self) -> Result<Vec<Template>> {
        self.run(self.inner.templates().list())
    }
    pub fn shared_templates(&self) -> Result<Vec<Template>> {
        self.run(self.inner.templates().shared())
    }
    pub fn get_template(&self, id: &str) -> Result<Template> {
        self.run(self.inner.templates().get(id))
    }
    pub fn delete_template(&self, id: &str) -> Result<()> {
        self.run(self.inner.templates().delete(id))
    }

    // sandboxes
    pub fn create_sandbox(&self, req: &CreateSandboxRequest) -> Result<Sandbox> {
        let sb = self.run(self.inner.sandboxes().create(req))?;
        Ok(Sandbox {
            inner: sb,
            rt: self.rt.clone(),
        })
    }
    pub fn get_sandbox(&self, id: &str) -> Result<Sandbox> {
        let sb = self.run(self.inner.sandboxes().get(id))?;
        Ok(Sandbox {
            inner: sb,
            rt: self.rt.clone(),
        })
    }
    pub fn list_sandboxes(&self, states: &[&str]) -> Result<Vec<SandboxSummary>> {
        self.run(self.inner.sandboxes().list(states))
    }
    pub fn shared_sandboxes(&self) -> Result<Vec<SandboxInfo>> {
        self.run(self.inner.sandboxes().shared())
    }
    pub fn kill_sandbox(&self, id: &str) -> Result<()> {
        self.run(self.inner.sandboxes().kill(id))
    }
}

/// Sync sandbox handle.
#[derive(Clone)]
pub struct Sandbox {
    inner: crate::Sandbox,
    rt: Arc<tokio::runtime::Runtime>,
}

impl Sandbox {
    fn run<F: std::future::Future>(&self, f: F) -> F::Output {
        self.rt.block_on(f)
    }
    pub fn id(&self) -> &str {
        self.inner.id()
    }
    pub fn state(&self) -> &str {
        self.inner.state()
    }
    pub fn info(&self) -> &SandboxInfo {
        self.inner.info()
    }
    pub fn port_url(&self, port: u16) -> Option<String> {
        self.inner.port_url(port)
    }
    pub fn refresh(&mut self) -> Result<SandboxInfo> {
        let rt = self.rt.clone();
        rt.block_on(self.inner.refresh()).cloned()
    }
    pub fn kill(&self) -> Result<()> {
        self.run(self.inner.kill())
    }
    pub fn freeze(&mut self, wait: bool) -> Result<SandboxInfo> {
        let rt = self.rt.clone();
        rt.block_on(self.inner.freeze(wait)).cloned()
    }
    pub fn resume(&mut self, timeout: Option<u64>) -> Result<SandboxInfo> {
        let rt = self.rt.clone();
        rt.block_on(self.inner.resume(timeout)).cloned()
    }
    pub fn rollback(&mut self, version: i64) -> Result<SandboxInfo> {
        let rt = self.rt.clone();
        rt.block_on(self.inner.rollback(version, None)).cloned()
    }
    pub fn reset(&mut self) -> Result<SandboxInfo> {
        let rt = self.rt.clone();
        rt.block_on(self.inner.reset()).cloned()
    }
    pub fn set_timeout(&self, seconds: u64) -> Result<()> {
        self.run(self.inner.set_timeout(seconds))
    }
    pub fn metrics(&self, start: Option<i64>, end: Option<i64>) -> Result<Vec<MetricPoint>> {
        self.run(self.inner.metrics(start, end))
    }
    /// Run a shell command string.
    pub fn exec(&self, cmd: &str, opts: &ExecOptions) -> Result<CommandResult> {
        self.run(self.inner.exec(cmd, opts))
    }
    /// Run an argv command.
    pub fn exec_argv(&self, argv: &[&str], opts: &ExecOptions) -> Result<CommandResult> {
        self.run(self.inner.exec(argv, opts))
    }
    /// Stream a command's output into `on_event`.
    pub fn exec_stream(
        &self,
        cmd: &str,
        opts: &ExecOptions,
        mut on_event: impl FnMut(ExecEvent),
    ) -> Result<()> {
        self.run(async {
            let mut s = self.inner.exec_stream(cmd, opts).await?;
            while let Some(ev) = s.next().await {
                on_event(ev?);
            }
            Ok(())
        })
    }
    pub fn run_code(&self, code: &str, opts: &RunCodeOptions) -> Result<CodeResult> {
        self.run(self.inner.run_code(code, opts))
    }
    pub fn write_file(&self, path: &str, data: impl Into<Bytes>) -> Result<u64> {
        self.run(self.inner.files().write(path, data, None))
    }
    pub fn read_file(&self, path: &str) -> Result<Bytes> {
        self.run(self.inner.files().read(path, None))
    }
    pub fn read_text(&self, path: &str) -> Result<String> {
        self.run(self.inner.files().read_text(path, None))
    }
    pub fn list_files(&self, path: &str) -> Result<Vec<FileEntry>> {
        self.run(self.inner.files().list(path, None))
    }
    /// The underlying async handle, for anything not wrapped here.
    pub fn as_async(&self) -> &crate::Sandbox {
        &self.inner
    }
}
