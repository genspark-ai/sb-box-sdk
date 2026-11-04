//! Template operations.

use std::sync::Arc;
use std::time::Duration;

use serde_json::Value;

use crate::error::{Error, Result};
use crate::transport::{Retry, Transport};
use crate::types::{Grant, GrantRequest, Template, TemplateBuild, TemplateBuildRequest};

/// `client.templates()`.
#[derive(Debug, Clone)]
pub struct Templates {
    pub(crate) t: Arc<Transport>,
}

#[derive(serde::Deserialize)]
struct TemplateList {
    #[serde(default)]
    templates: Vec<Template>,
}

impl Templates {
    /// `POST /templates/build` — asynchronous; follow with [`Templates::wait_ready`].
    pub async fn build(&self, req: &TemplateBuildRequest) -> Result<TemplateBuild> {
        if req.image.is_none() && req.dockerfile.is_none() {
            return Err(Error::Config(
                "template build needs `image` or `dockerfile`".into(),
            ));
        }
        self.t
            .post(
                "templates/build",
                req,
                Retry::Never,
                Duration::from_secs(120),
            )
            .await
    }

    /// Poll until the template is `ready`. Fails with [`Error::TemplateBuild`] on `error`
    /// and [`Error::WaitTimeout`] after `timeout` (default 600 s, interval 5 s).
    pub async fn wait_ready(
        &self,
        id: &str,
        timeout: Option<Duration>,
        interval: Option<Duration>,
    ) -> Result<Template> {
        let deadline = tokio::time::Instant::now() + timeout.unwrap_or(Duration::from_secs(600));
        let interval = interval.unwrap_or(Duration::from_secs(5));
        loop {
            let tpl = self.get(id).await?;
            match tpl.status.as_deref() {
                Some("ready") => return Ok(tpl),
                Some("error") => {
                    return Err(Error::TemplateBuild {
                        template_id: id.to_owned(),
                        build_error: tpl.build_error.unwrap_or_else(|| "build failed".into()),
                    })
                }
                _ => {}
            }
            if tokio::time::Instant::now() >= deadline {
                return Err(Error::WaitTimeout {
                    what: format!("template {id} to become ready"),
                });
            }
            tokio::time::sleep(interval).await;
        }
    }

    /// `GET /templates` — your own templates.
    pub async fn list(&self) -> Result<Vec<Template>> {
        let l: TemplateList = self.t.get("templates").await?;
        Ok(l.templates)
    }

    /// `GET /templates/shared` — templates shared with you.
    pub async fn shared(&self) -> Result<Vec<Template>> {
        self.t.get("templates/shared").await
    }

    /// `GET /templates/{id}`.
    pub async fn get(&self, id: &str) -> Result<Template> {
        self.t.get(&format!("templates/{id}")).await
    }

    /// `DELETE /templates/{id}`.
    pub async fn delete(&self, id: &str) -> Result<()> {
        let _: Value = self.t.delete(&format!("templates/{id}")).await?;
        Ok(())
    }

    /// `PUT /templates/{id}/grants/{user_id}`.
    pub async fn grant(&self, id: &str, user_id: &str, req: &GrantRequest) -> Result<Value> {
        self.t
            .json(
                reqwest::Method::PUT,
                &format!("templates/{id}/grants/{user_id}"),
                Some(req),
                Retry::Idempotent,
                self.t.timeout,
            )
            .await
    }

    /// `GET /templates/{id}/grants`.
    pub async fn grants(&self, id: &str) -> Result<Vec<Grant>> {
        self.t.get(&format!("templates/{id}/grants")).await
    }

    /// `DELETE /templates/{id}/grants/{user_id}`.
    pub async fn revoke(&self, id: &str, user_id: &str) -> Result<()> {
        let _: Value = self
            .t
            .delete(&format!("templates/{id}/grants/{user_id}"))
            .await?;
        Ok(())
    }
}
