//! Sandbox lifecycle operations (`client.sandboxes()`).

use std::sync::Arc;
use std::time::Duration;

use serde_json::{json, Value};

use crate::error::{Error, Result};
use crate::sandbox::Sandbox;
use crate::transport::{Retry, Transport};
use crate::types::{
    CreateSandboxRequest, Grant, GrantRequest, MetricPoint, SandboxInfo, SandboxSummary,
};

/// `client.sandboxes()`.
#[derive(Debug, Clone)]
pub struct Sandboxes {
    pub(crate) t: Arc<Transport>,
}

#[derive(serde::Deserialize)]
struct SandboxList {
    #[serde(default)]
    sandboxes: Vec<SandboxSummary>,
}

const LONG: Duration = Duration::from_secs(120);

impl Sandboxes {
    fn handle(&self, info: SandboxInfo) -> Sandbox {
        Sandbox::new(self.t.clone(), info)
    }

    /// `POST /sandboxes`. Use [`CreateSandboxRequest::base`] for the public base template.
    pub async fn create(&self, req: &CreateSandboxRequest) -> Result<Sandbox> {
        if req.template_name.is_none() && req.template_id.is_none() {
            return Err(Error::Config(
                "create needs template_name or template_id (CreateSandboxRequest::base())".into(),
            ));
        }
        let info: SandboxInfo = self.t.post("sandboxes", req, Retry::Never, LONG).await?;
        Ok(self.handle(info))
    }

    /// `GET /sandboxes?state=…`. `states` empty = all.
    pub async fn list(&self, states: &[&str]) -> Result<Vec<SandboxSummary>> {
        let path = if states.is_empty() {
            "sandboxes".to_owned()
        } else {
            format!("sandboxes?state={}", states.join(","))
        };
        let l: SandboxList = self.t.get(&path).await?;
        Ok(l.sandboxes)
    }

    /// `GET /sandboxes/shared`.
    pub async fn shared(&self) -> Result<Vec<SandboxInfo>> {
        self.t.get("sandboxes/shared").await
    }

    /// `GET /sandboxes/{id}` as a handle.
    pub async fn get(&self, id: &str) -> Result<Sandbox> {
        let info: SandboxInfo = self.t.get(&format!("sandboxes/{id}")).await?;
        Ok(self.handle(info))
    }

    /// Alias of [`Sandboxes::get`].
    pub async fn connect(&self, id: &str) -> Result<Sandbox> {
        self.get(id).await
    }

    /// `DELETE /sandboxes/{id}`.
    pub async fn kill(&self, id: &str) -> Result<()> {
        let _: Value = self.t.delete(&format!("sandboxes/{id}")).await?;
        Ok(())
    }

    /// `POST /sandboxes/{id}/freeze`. With `wait`, a `202` answer is followed by polling
    /// `GET` every 2 s (up to 90 s) until the state is `frozen`.
    pub async fn freeze(&self, id: &str, wait: bool) -> Result<SandboxInfo> {
        let prepared = self
            .t
            .execute(Retry::Idempotent, LONG, || {
                self.t
                    .builder(
                        reqwest::Method::POST,
                        &format!("sandboxes/{id}/freeze"),
                        LONG,
                    )
                    .header(reqwest::header::CONTENT_LENGTH, "0")
            })
            .await?;
        let accepted = prepared.status.as_u16() == 202;
        let body: Value = crate::transport::decode(prepared.response).await?;
        let state = body.get("state").and_then(Value::as_str).unwrap_or("");
        if !accepted && body.get("id").is_some() {
            return serde_json::from_value(body).map_err(Error::transport);
        }
        if !wait && (accepted || state == "frozen") {
            // 202 without wait, or the `already frozen` shape: hand back the current record.
            return self.t.get(&format!("sandboxes/{id}")).await;
        }
        let deadline = tokio::time::Instant::now() + Duration::from_secs(90);
        loop {
            let info: SandboxInfo = self.t.get(&format!("sandboxes/{id}")).await?;
            match info.state.as_str() {
                "frozen" => return Ok(info),
                "freezing" | "running" | "starting" => {}
                other => {
                    return Err(Error::Conflict {
                        body: Box::new(crate::error::ApiErrorBody {
                            status: 409,
                            message: format!("freeze ended in state {other}"),
                            ..Default::default()
                        }),
                    })
                }
            }
            if tokio::time::Instant::now() >= deadline {
                return Err(Error::WaitTimeout {
                    what: format!("sandbox {id} to freeze"),
                });
            }
            tokio::time::sleep(Duration::from_secs(2)).await;
        }
    }

    /// `POST /sandboxes/{id}/resume` (`timeout` in seconds).
    pub async fn resume(&self, id: &str, timeout: Option<u64>) -> Result<SandboxInfo> {
        let body = match timeout {
            Some(t) => json!({ "timeout": t }),
            None => json!({}),
        };
        self.t
            .post(
                &format!("sandboxes/{id}/resume"),
                &body,
                Retry::Idempotent,
                LONG,
            )
            .await
    }

    /// `POST /sandboxes/{id}/rollback` (`version` 0 = newest save point).
    pub async fn rollback(
        &self,
        id: &str,
        version: i64,
        timeout: Option<u64>,
    ) -> Result<SandboxInfo> {
        let mut body = json!({ "version": version });
        if let Some(t) = timeout {
            body["timeout"] = json!(t);
        }
        self.t
            .post(
                &format!("sandboxes/{id}/rollback"),
                &body,
                Retry::Never,
                LONG,
            )
            .await
    }

    /// `POST /sandboxes/{id}/clone` — honours `Retry-After` on 503 while the clone starts.
    pub async fn clone_sandbox(
        &self,
        id: &str,
        version: i64,
        metadata: Option<&std::collections::HashMap<String, String>>,
    ) -> Result<Sandbox> {
        let mut body = json!({ "version": version });
        if let Some(m) = metadata {
            body["metadata"] = json!(m);
        }
        let info: SandboxInfo = self
            .t
            .post(
                &format!("sandboxes/{id}/clone"),
                &body,
                Retry::HonourRetryAfter,
                LONG,
            )
            .await?;
        Ok(self.handle(info))
    }

    /// `POST /sandboxes/{id}/timeout` (`0` clears the deadline).
    pub async fn set_timeout(&self, id: &str, seconds: u64) -> Result<()> {
        let _: Value = self
            .t
            .post(
                &format!("sandboxes/{id}/timeout"),
                &json!({ "timeout": seconds }),
                Retry::Idempotent,
                self.t.timeout,
            )
            .await?;
        Ok(())
    }

    /// `POST /sandboxes/{id}/reset` — boots fresh from disk.
    pub async fn reset(&self, id: &str) -> Result<SandboxInfo> {
        self.t
            .post(
                &format!("sandboxes/{id}/reset"),
                &json!({}),
                Retry::HonourRetryAfter,
                LONG,
            )
            .await
    }

    /// `GET /sandboxes/{id}/metrics?start=&end=` (unix seconds).
    pub async fn metrics(
        &self,
        id: &str,
        start: Option<i64>,
        end: Option<i64>,
    ) -> Result<Vec<MetricPoint>> {
        let mut q = Vec::new();
        if let Some(s) = start {
            q.push(format!("start={s}"));
        }
        if let Some(e) = end {
            q.push(format!("end={e}"));
        }
        let path = if q.is_empty() {
            format!("sandboxes/{id}/metrics")
        } else {
            format!("sandboxes/{id}/metrics?{}", q.join("&"))
        };
        self.t.get(&path).await
    }

    /// `PUT /sandboxes/{id}/grants/{user_id}`.
    pub async fn grant(&self, id: &str, user_id: &str, req: &GrantRequest) -> Result<Value> {
        self.t
            .json(
                reqwest::Method::PUT,
                &format!("sandboxes/{id}/grants/{user_id}"),
                Some(req),
                Retry::Idempotent,
                self.t.timeout,
            )
            .await
    }

    /// `GET /sandboxes/{id}/grants`.
    pub async fn grants(&self, id: &str) -> Result<Vec<Grant>> {
        self.t.get(&format!("sandboxes/{id}/grants")).await
    }

    /// `DELETE /sandboxes/{id}/grants/{user_id}`.
    pub async fn revoke(&self, id: &str, user_id: &str) -> Result<()> {
        let _: Value = self
            .t
            .delete(&format!("sandboxes/{id}/grants/{user_id}"))
            .await?;
        Ok(())
    }
}
