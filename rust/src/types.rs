//! Request and response types. Unknown fields are preserved in `extra`.

use std::collections::HashMap;

use serde::{Deserialize, Serialize};
use serde_json::{Map, Value};

/// Map of extra, unmodelled fields.
pub type Extra = Map<String, Value>;

// ---------------------------------------------------------------- account

/// `GET /regions`.
#[derive(Debug, Clone, Deserialize)]
pub struct Regions {
    pub regions: Vec<String>,
    pub default: Option<String>,
    #[serde(flatten)]
    pub extra: Extra,
}

/// Plan limits block of [`Limits`].
#[derive(Debug, Clone, Default, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct PlanLimits {
    pub max_concurrent_sandboxes: Option<i64>,
    pub max_cpu: Option<i64>,
    #[serde(rename = "maxMemoryMB")]
    pub max_memory_mb: Option<i64>,
    #[serde(rename = "maxDiskGB")]
    pub max_disk_gb: Option<i64>,
    pub max_concurrent_builds: Option<i64>,
    #[serde(flatten)]
    pub extra: Extra,
}

/// Usage block of [`Limits`].
#[derive(Debug, Clone, Default, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct LimitsUsage {
    pub running_sandboxes: Option<i64>,
    pub building_templates: Option<i64>,
    #[serde(flatten)]
    pub extra: Extra,
}

/// `GET /limits`.
#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Limits {
    pub plan: Option<String>,
    pub plan_required: Option<bool>,
    #[serde(default)]
    pub limits: PlanLimits,
    #[serde(default)]
    pub usage: LimitsUsage,
    #[serde(flatten)]
    pub extra: Extra,
}

/// Credit totals (camelCase at top level, snake_case in daily rows — both accepted).
#[derive(Debug, Clone, Default, Deserialize)]
pub struct UsageTotals {
    #[serde(rename = "runSec", alias = "run_sec", default)]
    pub run_sec: f64,
    #[serde(rename = "cpuCoreSec", alias = "cpu_core_sec", default)]
    pub cpu_core_sec: f64,
    #[serde(rename = "memGBSec", alias = "mem_gb_sec", default)]
    pub mem_gb_sec: f64,
    #[serde(rename = "diskGBSec", alias = "disk_gb_sec", default)]
    pub disk_gb_sec: f64,
    #[serde(default)]
    pub credits: f64,
    #[serde(flatten)]
    pub extra: Extra,
}

/// One day in [`Usage::daily`].
#[derive(Debug, Clone, Deserialize)]
pub struct UsageDay {
    /// `YYYYMMDD`.
    pub day: String,
    #[serde(default)]
    pub direct: UsageTotals,
    #[serde(flatten)]
    pub extra: Extra,
}

/// `GET /usage`.
#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Usage {
    pub days: Option<i64>,
    pub from: Option<String>,
    pub to: Option<String>,
    pub rate_card: Option<Value>,
    #[serde(default)]
    pub direct: UsageTotals,
    #[serde(default)]
    pub daily: Vec<UsageDay>,
    #[serde(flatten)]
    pub extra: Extra,
}

// ---------------------------------------------------------------- templates

/// Template record.
#[derive(Debug, Clone, Deserialize)]
pub struct Template {
    pub id: String,
    pub name: Option<String>,
    pub description: Option<String>,
    /// `"public"` or `"private"`.
    pub namespace: Option<String>,
    pub base_image: Option<String>,
    /// `"building"`, `"ready"` or `"error"`.
    pub status: Option<String>,
    #[serde(rename = "buildError")]
    pub build_error: Option<String>,
    pub region: Option<String>,
    #[serde(default)]
    pub is_owner: bool,
    #[serde(default)]
    pub can_create: bool,
    #[serde(rename = "cpuCount")]
    pub cpu_count: Option<i64>,
    #[serde(rename = "memoryMB")]
    pub memory_mb: Option<i64>,
    #[serde(rename = "diskSizeGB")]
    pub disk_size_gb: Option<i64>,
    #[serde(rename = "startCmd")]
    pub start_cmd: Option<String>,
    #[serde(rename = "readyCmd")]
    pub ready_cmd: Option<String>,
    pub created_at: Option<String>,
    pub updated_at: Option<String>,
    #[serde(flatten)]
    pub extra: Extra,
}

/// `POST /templates/build` request.
#[derive(Debug, Clone, Default, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct TemplateBuildRequest {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub name: Option<String>,
    /// Base image reference (exclusive with `dockerfile`).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub image: Option<String>,
    /// Dockerfile contents (exclusive with `image`).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub dockerfile: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub cpu_count: Option<i64>,
    #[serde(rename = "memoryMB", skip_serializing_if = "Option::is_none")]
    pub memory_mb: Option<i64>,
    #[serde(rename = "diskSizeGB", skip_serializing_if = "Option::is_none")]
    pub disk_size_gb: Option<i64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub start_cmd: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub ready_cmd: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub region: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub log_paths: Option<Vec<String>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub description: Option<String>,
}

/// `POST /templates/build` response.
#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct TemplateBuild {
    pub template_id: String,
    pub status: Option<String>,
    pub region: Option<String>,
    pub owner: Option<String>,
    #[serde(flatten)]
    pub extra: Extra,
}

/// A sharing grant (templates and sandboxes).
#[derive(Debug, Clone, Deserialize)]
pub struct Grant {
    pub grantee_id: String,
    pub permission: Option<String>,
    pub granted_at: Option<String>,
    pub expires_at: Option<String>,
    #[serde(flatten)]
    pub extra: Extra,
}

/// Options for `grant`.
#[derive(Debug, Clone, Default, Serialize)]
pub struct GrantRequest {
    /// `"read"` (default) or `"write"`.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub perm: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub expires_at: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub reason: Option<String>,
}

// ---------------------------------------------------------------- sandboxes

/// Network rules for `create`.
#[derive(Debug, Clone, Default, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct NetworkConfig {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub allow_internet_access: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub allow_out: Option<Vec<String>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub deny_out: Option<Vec<String>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub allow_public_traffic: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub mask_request_host: Option<bool>,
}

/// What happens when `timeout` elapses.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "lowercase")]
pub enum OnTimeout {
    Freeze,
    Kill,
}

/// `POST /sandboxes` request.
#[derive(Debug, Clone, Default, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct CreateSandboxRequest {
    /// Template name (`"base"` = newest public base). Exclusive with `template_id`.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub template_name: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub template_id: Option<String>,
    /// Lifetime in seconds; `None`/0 = no deadline.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub timeout: Option<u64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub lifecycle: Option<Lifecycle>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub metadata: Option<HashMap<String, String>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub env_vars: Option<HashMap<String, String>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub network: Option<NetworkConfig>,
}

impl CreateSandboxRequest {
    /// Request for the newest public base template.
    pub fn base() -> Self {
        Self {
            template_name: Some("base".into()),
            ..Default::default()
        }
    }
    /// Request for a template by name.
    pub fn template(name: impl Into<String>) -> Self {
        Self {
            template_name: Some(name.into()),
            ..Default::default()
        }
    }
    /// Request for a template by id.
    pub fn template_id(id: impl Into<String>) -> Self {
        Self {
            template_id: Some(id.into()),
            ..Default::default()
        }
    }
    pub fn timeout(mut self, seconds: u64) -> Self {
        self.timeout = Some(seconds);
        self
    }
    pub fn on_timeout(mut self, on_timeout: OnTimeout) -> Self {
        self.lifecycle = Some(Lifecycle { on_timeout });
        self
    }
    pub fn metadata(mut self, metadata: HashMap<String, String>) -> Self {
        self.metadata = Some(metadata);
        self
    }
    pub fn env_vars(mut self, env: HashMap<String, String>) -> Self {
        self.env_vars = Some(env);
        self
    }
    pub fn network(mut self, network: NetworkConfig) -> Self {
        self.network = Some(network);
        self
    }
}

/// `lifecycle` block of a create request.
#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Lifecycle {
    pub on_timeout: OnTimeout,
}

/// A saved version (freeze point) of a sandbox.
#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SandboxVersion {
    pub version: i64,
    pub frozen_at: Option<String>,
    pub label: Option<String>,
    #[serde(flatten)]
    pub extra: Extra,
}

/// Full sandbox record.
#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SandboxInfo {
    pub id: String,
    #[serde(alias = "template_id")]
    pub template_id: Option<String>,
    pub state: String,
    pub region: Option<String>,
    /// Contains the literal `{port}` placeholder; see [`SandboxInfo::port_url`].
    pub port_url: Option<String>,
    pub domain: Option<String>,
    #[serde(alias = "created_at")]
    pub created_at: Option<String>,
    pub started_at: Option<String>,
    pub frozen_at: Option<String>,
    pub timeout_at: Option<String>,
    pub cpu_count: Option<i64>,
    #[serde(rename = "memoryMB")]
    pub memory_mb: Option<i64>,
    #[serde(rename = "diskSizeGB")]
    pub disk_size_gb: Option<i64>,
    pub idle_timeout: Option<i64>,
    #[serde(default)]
    pub versions: Vec<SandboxVersion>,
    #[serde(default)]
    pub metadata: Map<String, Value>,
    pub traffic_access_token: Option<String>,
    #[serde(default)]
    pub is_owner: Option<bool>,
    #[serde(default)]
    pub has_write: Option<bool>,
    #[serde(flatten)]
    pub extra: Extra,
}

impl SandboxInfo {
    /// Public URL for `port`, built from `port_url`.
    pub fn port_url(&self, port: u16) -> Option<String> {
        self.port_url
            .as_ref()
            .map(|u| u.replace("{port}", &port.to_string()))
    }
}

/// Compact row from `GET /sandboxes`.
#[derive(Debug, Clone, Deserialize)]
pub struct SandboxSummary {
    pub id: String,
    #[serde(alias = "templateId")]
    pub template_id: Option<String>,
    pub state: String,
    pub region: Option<String>,
    #[serde(rename = "portUrl")]
    pub port_url: Option<String>,
    pub domain: Option<String>,
    #[serde(alias = "createdAt")]
    pub created_at: Option<String>,
    #[serde(alias = "updatedAt")]
    pub updated_at: Option<String>,
    #[serde(flatten)]
    pub extra: Extra,
}

/// One point from `GET /sandboxes/{id}/metrics`.
#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct MetricPoint {
    pub timestamp: Option<String>,
    pub timestamp_unix: Option<i64>,
    pub cpu_count: Option<f64>,
    pub cpu_used_pct: Option<f64>,
    pub mem_used: Option<f64>,
    pub mem_total: Option<f64>,
    pub mem_cache: Option<f64>,
    pub disk_used: Option<f64>,
    pub disk_total: Option<f64>,
    #[serde(flatten)]
    pub extra: Extra,
}

// ---------------------------------------------------------------- commands

/// Options for `exec` / `exec_stream` / `commands.start`.
#[derive(Debug, Clone, Default)]
pub struct ExecOptions {
    pub cwd: Option<String>,
    pub env: Option<HashMap<String, String>>,
    pub user: Option<String>,
    /// Seconds (≤ 600); default 60. Ignored by `commands.start`.
    pub timeout: Option<u64>,
}

/// Result of `exec`. A non-zero `exit_code` is data, not an error.
#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct CommandResult {
    #[serde(default)]
    pub stdout: String,
    #[serde(default)]
    pub stderr: String,
    #[serde(default)]
    pub exit_code: i32,
    #[serde(flatten)]
    pub extra: Extra,
}

impl CommandResult {
    pub fn success(&self) -> bool {
        self.exit_code == 0
    }
}

/// One event from a streaming exec / background command / PTY.
#[derive(Debug, Clone, PartialEq)]
pub enum ExecEvent {
    /// First event: the execution id.
    Started {
        exec_id: String,
    },
    Stdout(String),
    Stderr(String),
    /// Raw PTY output.
    Data(String),
    Exit {
        exit_code: i32,
    },
    /// Server-side failure (`stage` names where it happened).
    Error {
        message: String,
        stage: Option<String>,
    },
}

/// A background command started with `commands.start`.
#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ProcessInfo {
    pub exec_id: String,
    pub pid: Option<i64>,
    pub sandbox_id: Option<String>,
    #[serde(flatten)]
    pub extra: Extra,
}

// ---------------------------------------------------------------- run-code

/// Options for `run_code`.
#[derive(Debug, Clone, Default)]
pub struct RunCodeOptions {
    /// Default `"python"`.
    pub language: Option<String>,
    /// Interpreter context to reuse state across calls.
    pub context_id: Option<String>,
    pub env: Option<HashMap<String, String>>,
    /// Seconds (≤ 540); default 60.
    pub timeout: Option<u64>,
    /// `"base64"` (default) or `"omit"`.
    pub images: Option<String>,
}

/// Interpreter exception.
#[derive(Debug, Clone, Deserialize)]
pub struct CodeError {
    pub name: Option<String>,
    pub value: Option<String>,
    pub traceback: Option<String>,
    #[serde(flatten)]
    pub extra: Extra,
}

/// One MIME bundle produced by the interpreter.
#[derive(Debug, Clone, Deserialize)]
pub struct CodeResultItem {
    pub text: Option<String>,
    pub html: Option<String>,
    pub markdown: Option<String>,
    pub svg: Option<String>,
    pub png: Option<String>,
    pub jpeg: Option<String>,
    pub pdf: Option<String>,
    pub latex: Option<String>,
    pub json: Option<Value>,
    pub javascript: Option<String>,
    pub data: Option<Value>,
    pub chart: Option<Value>,
    #[serde(default)]
    pub omitted: Option<Value>,
    #[serde(rename = "isMainResult", default)]
    pub is_main_result: bool,
    #[serde(flatten)]
    pub extra: Extra,
}

/// `POST /sandboxes/{id}/run-code` response.
#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct CodeResult {
    #[serde(default)]
    pub stdout: String,
    #[serde(default)]
    pub stderr: String,
    pub execution_count: Option<i64>,
    #[serde(default)]
    pub complete: bool,
    pub error: Option<CodeError>,
    #[serde(default)]
    pub results: Vec<CodeResultItem>,
    #[serde(flatten)]
    pub extra: Extra,
}

impl CodeResult {
    /// Text of the main result, if any.
    pub fn text(&self) -> Option<&str> {
        self.results
            .iter()
            .find(|r| r.is_main_result)
            .or_else(|| self.results.first())
            .and_then(|r| r.text.as_deref())
    }
}

// ---------------------------------------------------------------- files

/// Directory entry from `files.list`.
#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct FileEntry {
    pub name: String,
    pub path: Option<String>,
    #[serde(default)]
    pub is_dir: bool,
    pub size: Option<u64>,
    #[serde(flatten)]
    pub extra: Extra,
}

// ---------------------------------------------------------------- pty

/// Options for `pty.open`.
#[derive(Debug, Clone, Default)]
pub struct PtyOptions {
    /// Default `["/bin/bash"]`.
    pub command: Option<Vec<String>>,
    pub rows: Option<u16>,
    pub cols: Option<u16>,
    pub cwd: Option<String>,
    pub env: Option<HashMap<String, String>>,
}
