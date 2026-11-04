//! HTTP plumbing: auth, user agent, timeouts, retries, error mapping.

use std::sync::Arc;
use std::time::Duration;

use rand::Rng;
use reqwest::header::{HeaderValue, RETRY_AFTER};
use reqwest::{Method, RequestBuilder, Response, StatusCode};
use serde::de::DeserializeOwned;
use serde::Serialize;

use crate::error::{redact, Error, Result};

pub(crate) const DEFAULT_BASE_URL: &str = "https://www.genspark.ai/api/sb-box/v1";
pub(crate) const USER_AGENT: &str = concat!(
    "sbbox-rust-sdk/",
    env!("CARGO_PKG_VERSION"),
    " (+https://www.genspark.ai/sb-box/docs)"
);

/// Retry behaviour of one request.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(crate) enum Retry {
    /// Never retried (create, exec, writes …).
    Never,
    /// Up to `retries` attempts on connection errors and 502/503/504 with jittered backoff.
    Idempotent,
    /// 503 honours `Retry-After` (default 2 s) until the call deadline.
    HonourRetryAfter,
}

#[derive(Debug)]
pub(crate) struct Transport {
    pub http: reqwest::Client,
    pub base_url: String,
    pub api_key: String,
    pub timeout: Duration,
    pub retries: u32,
    pub user_agent: String,
}

pub(crate) struct Prepared {
    pub status: StatusCode,
    pub response: Response,
}

impl Transport {
    pub fn url(&self, path: &str) -> String {
        format!(
            "{}/{}",
            self.base_url.trim_end_matches('/'),
            path.trim_start_matches('/')
        )
    }

    pub fn builder(&self, method: Method, path: &str, timeout: Duration) -> RequestBuilder {
        self.http
            .request(method, self.url(path))
            .header("X-Api-Key", &self.api_key)
            .header(reqwest::header::USER_AGENT, &self.user_agent)
            .timeout(timeout)
    }

    /// Execute with the retry policy; the closure rebuilds the request for each attempt.
    pub async fn execute<F>(
        &self,
        retry: Retry,
        deadline: Duration,
        mut build: F,
    ) -> Result<Prepared>
    where
        F: FnMut() -> RequestBuilder,
    {
        let start = tokio::time::Instant::now();
        let mut attempt: u32 = 0;
        loop {
            attempt += 1;
            let sent = build().send().await;
            match sent {
                Ok(resp) => {
                    let status = resp.status();
                    let retry_after = parse_retry_after(resp.headers().get(RETRY_AFTER));
                    let request_id = resp
                        .headers()
                        .get("x-request-id")
                        .and_then(|v| v.to_str().ok())
                        .map(str::to_owned);
                    let transient = matches!(status.as_u16(), 502..=504);
                    let again = match retry {
                        Retry::Never => false,
                        Retry::Idempotent => transient && attempt <= self.retries,
                        Retry::HonourRetryAfter => {
                            status.as_u16() == 503 && start.elapsed() < deadline
                        }
                    };
                    if again {
                        let wait = match retry {
                            Retry::HonourRetryAfter => {
                                retry_after.unwrap_or(Duration::from_secs(2))
                            }
                            _ => backoff(attempt),
                        };
                        drop(resp);
                        tokio::time::sleep(wait).await;
                        continue;
                    }
                    if status.is_success() {
                        return Ok(Prepared {
                            status,
                            response: resp,
                        });
                    }
                    let text = resp.text().await.unwrap_or_default();
                    return Err(Error::from_response(
                        status.as_u16(),
                        &text,
                        retry_after,
                        request_id,
                    ));
                }
                Err(e) => {
                    let connection = e.is_connect() || e.is_timeout() || e.is_request();
                    let again =
                        matches!(retry, Retry::Idempotent) && connection && attempt <= self.retries;
                    if again {
                        tokio::time::sleep(backoff(attempt)).await;
                        continue;
                    }
                    return Err(Error::Transport(redact(&e.to_string())));
                }
            }
        }
    }

    pub async fn json<T: DeserializeOwned>(
        &self,
        method: Method,
        path: &str,
        body: Option<&impl Serialize>,
        retry: Retry,
        timeout: Duration,
    ) -> Result<T> {
        let body = match body {
            Some(b) => Some(serde_json::to_vec(b).map_err(Error::transport)?),
            None => None,
        };
        let prepared = self
            .execute(retry, timeout, || {
                let mut rb = self.builder(method.clone(), path, timeout);
                if let Some(b) = &body {
                    rb = rb
                        .header(reqwest::header::CONTENT_TYPE, "application/json")
                        .body(b.clone());
                }
                rb
            })
            .await?;
        decode(prepared.response).await
    }

    pub async fn get<T: DeserializeOwned>(&self, path: &str) -> Result<T> {
        self.json::<T>(
            Method::GET,
            path,
            None::<&()>,
            Retry::Idempotent,
            self.timeout,
        )
        .await
    }

    pub async fn post<T: DeserializeOwned>(
        &self,
        path: &str,
        body: &impl Serialize,
        retry: Retry,
        timeout: Duration,
    ) -> Result<T> {
        self.json(Method::POST, path, Some(body), retry, timeout)
            .await
    }

    pub async fn delete<T: DeserializeOwned>(&self, path: &str) -> Result<T> {
        self.json::<T>(
            Method::DELETE,
            path,
            None::<&()>,
            Retry::Idempotent,
            self.timeout,
        )
        .await
    }
}

pub(crate) async fn decode<T: DeserializeOwned>(resp: Response) -> Result<T> {
    let bytes = resp.bytes().await.map_err(Error::transport)?;
    if bytes.is_empty() {
        // Allow unit-ish responses (`()` / Value::Null).
        return serde_json::from_slice(b"null").map_err(Error::transport);
    }
    serde_json::from_slice(&bytes).map_err(|e| {
        Error::Transport(format!(
            "malformed response: {e} (body: {})",
            redact(&String::from_utf8_lossy(&bytes[..bytes.len().min(200)]))
        ))
    })
}

fn backoff(attempt: u32) -> Duration {
    let base_ms: u64 = match attempt {
        1 => 500,
        2 => 1000,
        _ => 2000,
    };
    let jitter = rand::thread_rng().gen_range(0..=base_ms / 4);
    Duration::from_millis(base_ms + jitter)
}

fn parse_retry_after(v: Option<&HeaderValue>) -> Option<Duration> {
    v?.to_str()
        .ok()?
        .trim()
        .parse::<f64>()
        .ok()
        .map(Duration::from_secs_f64)
}

pub(crate) fn shared(t: Transport) -> Arc<Transport> {
    Arc::new(t)
}
