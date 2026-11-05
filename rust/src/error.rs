//! Error types. Every API failure maps to one [`Error`] variant by HTTP status
//! and the server's error `code`; the raw parsed body is always available in
//! [`ApiErrorBody::detail`].

use std::fmt;
use std::time::Duration;

use serde_json::Value;

/// Result alias used across the crate.
pub type Result<T> = std::result::Result<T, Error>;

/// Shared payload carried by every API-originated error variant.
#[derive(Debug, Clone, Default)]
pub struct ApiErrorBody {
    /// HTTP status code.
    pub status: u16,
    /// Machine-readable error code (`sb_box_plan_required`, `memory_expired`, …), if any.
    pub code: Option<String>,
    /// Human-readable message (API key material redacted).
    pub message: String,
    /// Raw parsed JSON body (or `{"raw": "<text>"}` when the body was not JSON).
    pub detail: Value,
    /// `Retry-After` header, when present.
    pub retry_after: Option<Duration>,
    /// `x-request-id` header, when present.
    pub request_id: Option<String>,
}

impl fmt::Display for ApiErrorBody {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "HTTP {}", self.status)?;
        if let Some(code) = &self.code {
            write!(f, " [{code}]")?;
        }
        if !self.message.is_empty() {
            write!(f, ": {}", redact(&self.message))?;
        }
        if let Some(id) = &self.request_id {
            write!(f, " (request {id})")?;
        }
        Ok(())
    }
}

/// Details of a plan-limit rejection ([`Error::Limit`]).
#[derive(Debug, Clone, Default)]
pub struct LimitInfo {
    /// `sb_box_concurrency_limit`, `sb_box_spec_limit` or `sb_box_build_concurrency_limit`.
    pub code: String,
    pub plan: Option<String>,
    pub limit: Option<Value>,
    pub current: Option<Value>,
    pub requested: Option<Value>,
}

/// All errors produced by this crate.
#[derive(Debug, thiserror::Error)]
#[non_exhaustive]
pub enum Error {
    /// 400 / 422 — the request was rejected as invalid. `errors` lists field problems on 422.
    #[error("invalid request: {body}")]
    Validation {
        body: Box<ApiErrorBody>,
        errors: Vec<Value>,
    },
    /// 401 — missing or invalid API key.
    #[error("authentication failed: {body}")]
    Authentication { body: Box<ApiErrorBody> },
    /// 402 — the account has no credits left.
    #[error("insufficient credits: {body}")]
    InsufficientCredits {
        body: Box<ApiErrorBody>,
        balance: Option<f64>,
    },
    /// 403 with `sb_box_plan_required` — the feature needs a higher plan.
    #[error("plan required: {body}")]
    PlanRequired {
        body: Box<ApiErrorBody>,
        plan: Option<String>,
        upgrade: Option<String>,
    },
    /// 403 — permission denied for another reason.
    #[error("forbidden: {body}")]
    Forbidden { body: Box<ApiErrorBody> },
    /// 404 — unknown (or killed) sandbox / template / file.
    #[error("not found: {body}")]
    NotFound { body: Box<ApiErrorBody> },
    /// 404 with `sb_box_region_retired` — the record lives in a region that is no longer served.
    #[error("region retired: {body}")]
    RegionRetired {
        body: Box<ApiErrorBody>,
        region: Option<String>,
    },
    /// 408 — `exec` hit its timeout; `killed` says whether the process was terminated.
    #[error("command timed out: {body}")]
    CommandTimeout {
        body: Box<ApiErrorBody>,
        killed: bool,
    },
    /// 409 with a `*_limit` code — a plan limit was reached; see [`LimitInfo`].
    #[error("limit reached: {body}")]
    Limit {
        body: Box<ApiErrorBody>,
        info: Box<LimitInfo>,
    },
    /// 409 with `sb_box_interpreter_unavailable` — `run_code` is not available on this template.
    #[error("interpreter unavailable: {body}")]
    InterpreterUnavailable { body: Box<ApiErrorBody> },
    /// 409 — the operation conflicts with the sandbox's current state.
    #[error("conflict: {body}")]
    Conflict { body: Box<ApiErrorBody> },
    /// 410 — the sandbox's disk is gone. `recovery` lists the accepted next steps
    /// (`rollback` when the sandbox has save points); otherwise create a new sandbox.
    #[error("disk lost: {body}")]
    DiskLost {
        body: Box<ApiErrorBody>,
        recovery: Vec<String>,
    },
    /// 428 — the memory snapshot expired; `recovery` lists the accepted next steps (e.g. `reset`).
    #[error("memory expired: {body}")]
    MemoryExpired {
        body: Box<ApiErrorBody>,
        recovery: Vec<String>,
    },
    /// 499 — the server saw the client disconnect.
    #[error("client disconnected: {body}")]
    ClientDisconnected { body: Box<ApiErrorBody> },
    /// 502 with `sb_box_interpreter_start_failed`.
    #[error("interpreter failed to start: {body}")]
    InterpreterStartFailed { body: Box<ApiErrorBody> },
    /// Any other 5xx.
    #[error("server error: {body}")]
    Server { body: Box<ApiErrorBody> },
    /// Transport-level failure (DNS, TLS, timeout, connection reset) or a malformed response.
    #[error("transport error: {0}")]
    Transport(String),
    /// A template build finished with status `error`.
    #[error("template build failed: {build_error}")]
    TemplateBuild {
        template_id: String,
        build_error: String,
    },
    /// A `wait`-style call exceeded its deadline.
    #[error("timed out waiting for {what}")]
    WaitTimeout { what: String },
    /// Client-side configuration problem (missing key, bad URL, non-string metadata…).
    #[error("configuration error: {0}")]
    Config(String),
}

impl Error {
    /// The API body for API-originated variants.
    pub fn body(&self) -> Option<&ApiErrorBody> {
        match self {
            Error::Validation { body, .. }
            | Error::Authentication { body }
            | Error::InsufficientCredits { body, .. }
            | Error::PlanRequired { body, .. }
            | Error::Forbidden { body }
            | Error::NotFound { body }
            | Error::RegionRetired { body, .. }
            | Error::CommandTimeout { body, .. }
            | Error::Limit { body, .. }
            | Error::InterpreterUnavailable { body }
            | Error::Conflict { body }
            | Error::DiskLost { body, .. }
            | Error::MemoryExpired { body, .. }
            | Error::ClientDisconnected { body }
            | Error::InterpreterStartFailed { body }
            | Error::Server { body } => Some(body.as_ref()),
            _ => None,
        }
    }

    /// HTTP status, when the error came from the API.
    pub fn status(&self) -> Option<u16> {
        self.body().map(|b| b.status)
    }

    /// Server error code, when present.
    pub fn code(&self) -> Option<&str> {
        self.body().and_then(|b| b.code.as_deref())
    }

    /// `Retry-After` hint, when the server sent one.
    pub fn retry_after(&self) -> Option<Duration> {
        self.body().and_then(|b| b.retry_after)
    }

    /// True for 404 (including [`Error::RegionRetired`]).
    pub fn is_not_found(&self) -> bool {
        matches!(self, Error::NotFound { .. } | Error::RegionRetired { .. })
    }

    pub(crate) fn transport(e: impl fmt::Display) -> Self {
        Error::Transport(redact(&e.to_string()))
    }

    /// Build the right variant from a parsed response.
    pub(crate) fn from_response(
        status: u16,
        body_text: &str,
        retry_after: Option<Duration>,
        request_id: Option<String>,
    ) -> Self {
        let detail: Value = serde_json::from_str(body_text)
            .unwrap_or_else(|_| serde_json::json!({ "raw": body_text }));
        // `{"detail": {...}}`, `{"detail": "..."}`, `{"error": "..."}`, or a flat object.
        let inner: &Value = match detail.get("detail") {
            Some(d @ Value::Object(_)) => d,
            _ => &detail,
        };
        let code = inner
            .get("code")
            .or_else(|| detail.get("code"))
            .or_else(|| detail.get("reason"))
            .and_then(Value::as_str)
            .map(str::to_owned);
        let message = [
            inner.get("message"),
            detail.get("detail").filter(|v| v.is_string()),
            detail.get("error"),
            detail.get("message"),
            detail.get("raw"),
        ]
        .into_iter()
        .flatten()
        .find_map(|v| v.as_str().map(str::to_owned))
        .unwrap_or_else(|| body_text.chars().take(200).collect());
        let body = Box::new(ApiErrorBody {
            status,
            code: code.clone(),
            message: redact(&message),
            detail: detail.clone(),
            retry_after,
            request_id,
        });
        let s = |k: &str| inner.get(k).and_then(Value::as_str).map(str::to_owned);
        let v = |k: &str| inner.get(k).cloned();
        let code_str = code.as_deref().unwrap_or("");
        match status {
            400 => Error::Validation {
                body,
                errors: vec![],
            },
            401 => Error::Authentication { body },
            402 => Error::InsufficientCredits {
                balance: inner.get("balance").and_then(Value::as_f64),
                body,
            },
            403 if code_str == "sb_box_plan_required" => Error::PlanRequired {
                plan: s("plan"),
                upgrade: s("upgrade"),
                body,
            },
            403 => Error::Forbidden { body },
            404 if code_str == "sb_box_region_retired" => Error::RegionRetired {
                region: s("region"),
                body,
            },
            404 => Error::NotFound { body },
            408 => Error::CommandTimeout {
                killed: detail
                    .get("killed")
                    .and_then(Value::as_bool)
                    .unwrap_or(false),
                body,
            },
            409 if code_str.ends_with("_limit") => Error::Limit {
                info: Box::new(LimitInfo {
                    code: code_str.to_owned(),
                    plan: s("plan"),
                    limit: v("limit"),
                    current: v("current"),
                    requested: v("requested"),
                }),
                body,
            },
            409 if code_str == "sb_box_interpreter_unavailable" => {
                Error::InterpreterUnavailable { body }
            }
            409 => Error::Conflict { body },
            410 => Error::DiskLost {
                recovery: string_list(detail.get("recovery")),
                body,
            },
            422 => Error::Validation {
                errors: detail
                    .get("errors")
                    .and_then(Value::as_array)
                    .cloned()
                    .unwrap_or_default(),
                body,
            },
            428 => Error::MemoryExpired {
                recovery: string_list(detail.get("recovery")),
                body,
            },
            499 => Error::ClientDisconnected { body },
            502 if code_str == "sb_box_interpreter_start_failed" => {
                Error::InterpreterStartFailed { body }
            }
            _ => Error::Server { body },
        }
    }
}

/// Replace any `gsk-…` key material in `s` with `gsk-REDACTED`.
pub fn redact(s: &str) -> String {
    let mut out = String::with_capacity(s.len());
    let mut rest = s;
    while let Some(i) = rest.find("gsk-") {
        out.push_str(&rest[..i]);
        out.push_str("gsk-REDACTED");
        let tail = &rest[i + 4..];
        let n = tail
            .char_indices()
            .find(|(_, c)| !(c.is_ascii_alphanumeric() || matches!(c, '.' | '_' | '-')))
            .map(|(j, _)| j)
            .unwrap_or(tail.len());
        rest = &tail[n..];
    }
    out.push_str(rest);
    out
}

fn string_list(v: Option<&Value>) -> Vec<String> {
    v.and_then(Value::as_array)
        .map(|a| {
            a.iter()
                .filter_map(|x| x.as_str().map(str::to_owned))
                .collect()
        })
        .unwrap_or_default()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn redacts_keys() {
        assert_eq!(redact("key gsk-abc.DEF_9 here"), "key gsk-REDACTED here");
        assert_eq!(redact("none"), "none");
        assert_eq!(redact("gsk-x"), "gsk-REDACTED");
    }

    #[test]
    fn error_stays_small() {
        // Users return `sbbox::Result<()>` from `main`; keep under clippy's 128-byte threshold.
        assert!(
            std::mem::size_of::<Error>() <= 128,
            "{}",
            std::mem::size_of::<Error>()
        );
    }

    #[test]
    fn maps_limit() {
        let e = Error::from_response(
            409,
            r#"{"detail":{"code":"sb_box_concurrency_limit","limit":2,"current":2,"plan":"free","message":"too many"}}"#,
            None,
            None,
        );
        match e {
            Error::Limit { info, .. } => {
                assert_eq!(info.code, "sb_box_concurrency_limit");
                assert_eq!(info.limit, Some(serde_json::json!(2)));
            }
            other => panic!("{other:?}"),
        }
    }
}
