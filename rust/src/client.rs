//! The entry point: [`Client`] and [`ClientBuilder`].

use std::sync::Arc;
use std::time::Duration;

use crate::error::{Error, Result};
use crate::sandboxes::Sandboxes;
use crate::templates::Templates;
use crate::transport::{Transport, DEFAULT_BASE_URL, USER_AGENT};
use crate::types::{Limits, Regions, Usage};

/// Async SB-Box client. Cheap to clone (shares one connection pool).
#[derive(Debug, Clone)]
pub struct Client {
    pub(crate) t: Arc<Transport>,
}

/// Builder for [`Client`].
#[derive(Debug, Default)]
pub struct ClientBuilder {
    api_key: Option<String>,
    base_url: Option<String>,
    timeout: Option<Duration>,
    retries: Option<u32>,
    user_agent_suffix: Option<String>,
    http: Option<reqwest::Client>,
}

impl ClientBuilder {
    /// API key (`gsk-…`). Falls back to `SBBOX_API_KEY`, then `GSK_API_KEY`.
    pub fn api_key(mut self, key: impl Into<String>) -> Self {
        self.api_key = Some(key.into());
        self
    }
    /// Base URL. Falls back to `SBBOX_BASE_URL`, then `https://www.genspark.ai/api/sb-box/v1`.
    pub fn base_url(mut self, url: impl Into<String>) -> Self {
        self.base_url = Some(url.into());
        self
    }
    /// Default per-request timeout (60 s).
    pub fn timeout(mut self, timeout: Duration) -> Self {
        self.timeout = Some(timeout);
        self
    }
    /// Retry attempts for idempotent calls (3). `0` disables retries.
    pub fn retries(mut self, retries: u32) -> Self {
        self.retries = Some(retries);
        self
    }
    /// Appended to the `User-Agent`, e.g. your app name/version.
    pub fn user_agent_suffix(mut self, suffix: impl Into<String>) -> Self {
        self.user_agent_suffix = Some(suffix.into());
        self
    }
    /// Bring your own `reqwest::Client` (proxies, TLS roots …).
    pub fn http_client(mut self, http: reqwest::Client) -> Self {
        self.http = Some(http);
        self
    }

    pub fn build(self) -> Result<Client> {
        let api_key = self
            .api_key
            .or_else(|| std::env::var("SBBOX_API_KEY").ok())
            .or_else(|| std::env::var("GSK_API_KEY").ok())
            .map(|k| k.trim().to_owned())
            .filter(|k| !k.is_empty())
            .ok_or_else(|| {
                Error::Config(
                    "no API key: pass ClientBuilder::api_key or set SBBOX_API_KEY (or GSK_API_KEY)"
                        .into(),
                )
            })?;
        let base_url = self
            .base_url
            .or_else(|| std::env::var("SBBOX_BASE_URL").ok())
            .filter(|u| !u.trim().is_empty())
            .unwrap_or_else(|| DEFAULT_BASE_URL.to_owned());
        let parsed = url::Url::parse(&base_url)
            .map_err(|e| Error::Config(format!("invalid base_url {base_url:?}: {e}")))?;
        if !matches!(parsed.scheme(), "http" | "https") {
            return Err(Error::Config(format!(
                "base_url must be http(s): {base_url:?}"
            )));
        }
        let user_agent = match self.user_agent_suffix {
            Some(s) if !s.trim().is_empty() => format!("{USER_AGENT} {}", s.trim()),
            _ => USER_AGENT.to_owned(),
        };
        let http = match self.http {
            Some(h) => h,
            None => reqwest::Client::builder()
                .build()
                .map_err(|e| Error::Config(format!("http client: {e}")))?,
        };
        Ok(Client {
            t: crate::transport::shared(Transport {
                http,
                base_url: base_url.trim_end_matches('/').to_owned(),
                api_key,
                timeout: self.timeout.unwrap_or(Duration::from_secs(60)),
                retries: self.retries.unwrap_or(3),
                user_agent,
            }),
        })
    }
}

impl Client {
    /// Start configuring a client.
    pub fn builder() -> ClientBuilder {
        ClientBuilder::default()
    }

    /// Client from the environment (`SBBOX_API_KEY` / `GSK_API_KEY`, `SBBOX_BASE_URL`).
    pub fn from_env() -> Result<Self> {
        Self::builder().build()
    }

    /// Client with an explicit key and default settings.
    pub fn new(api_key: impl Into<String>) -> Result<Self> {
        Self::builder().api_key(api_key).build()
    }

    /// The base URL in use.
    pub fn base_url(&self) -> &str {
        &self.t.base_url
    }

    /// Template operations.
    pub fn templates(&self) -> Templates {
        Templates { t: self.t.clone() }
    }

    /// Sandbox operations.
    pub fn sandboxes(&self) -> Sandboxes {
        Sandboxes { t: self.t.clone() }
    }

    /// `GET /regions`.
    pub async fn regions(&self) -> Result<Regions> {
        self.t.get("regions").await
    }

    /// `GET /limits`.
    pub async fn limits(&self) -> Result<Limits> {
        self.t.get("limits").await
    }

    /// `GET /usage?days=` (1–366).
    pub async fn usage(&self, days: u32) -> Result<Usage> {
        self.t.get(&format!("usage?days={days}")).await
    }
}
