//! Offline contract tests against a wiremock server implementing `API.md`.

use std::collections::HashMap;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;
use std::time::Duration;

use futures_util::StreamExt;
use sbbox::types::*;
use sbbox::{Client, Error, ExecEvent};
use serde_json::{json, Value};
use wiremock::matchers::{body_json, body_partial_json, header, method, path, query_param};
use wiremock::{Mock, MockServer, Request, Respond, ResponseTemplate};

const KEY: &str = "gsk-test.KEY_123";

async fn client(server: &MockServer) -> Client {
    Client::builder()
        .api_key(KEY)
        .base_url(format!("{}/v1", server.uri()))
        .timeout(Duration::from_secs(5))
        .build()
        .unwrap()
}

fn record(id: &str, state: &str) -> Value {
    json!({
        "id": id, "templateId": "tpl-1", "state": state, "region": "westus2",
        "portUrl": format!("https://{{port}}-{id}.sandbox.example"), "domain": "sandbox.example",
        "createdAt": "2026-11-04T00:00:00Z", "cpuCount": 1, "memoryMB": 2048, "diskSizeGB": 20,
        "idleTimeout": 600, "versions": [{"version": 1, "frozenAt": "2026-11-04T01:00:00Z", "label": "v1"}],
        "metadata": {"cogenId": "u1", "k": "v"}, "unknownField": 7
    })
}

fn sse(body: &str) -> ResponseTemplate {
    ResponseTemplate::new(200)
        .insert_header("content-type", "text/event-stream")
        .set_body_raw(body.as_bytes().to_vec(), "text/event-stream")
}

/// Clears the SDK's environment variables for one test and restores the developer's
/// values afterwards, so the suite does not depend on (or disturb) the shell it runs in.
/// Tests touching the environment serialise on the mutex because `std::env` is process-wide.
struct EnvGuard {
    saved: Vec<(&'static str, Option<String>)>,
    _lock: std::sync::MutexGuard<'static, ()>,
}

impl EnvGuard {
    const VARS: [&'static str; 3] = ["SBBOX_API_KEY", "GSK_API_KEY", "SBBOX_BASE_URL"];

    fn clear() -> Self {
        static LOCK: std::sync::Mutex<()> = std::sync::Mutex::new(());
        let lock = LOCK.lock().unwrap_or_else(|e| e.into_inner());
        let saved = Self::VARS
            .iter()
            .map(|v| {
                let old = std::env::var(v).ok();
                std::env::remove_var(v);
                (*v, old)
            })
            .collect();
        Self { saved, _lock: lock }
    }
}

impl Drop for EnvGuard {
    fn drop(&mut self) {
        for (var, old) in self.saved.drain(..) {
            match old {
                Some(value) => std::env::set_var(var, value),
                None => std::env::remove_var(var),
            }
        }
    }
}

#[test]
fn key_resolution_and_config() {
    let _env = EnvGuard::clear();
    let e = Client::builder().build().unwrap_err();
    assert!(matches!(e, Error::Config(_)));
    assert!(e.to_string().contains("SBBOX_API_KEY") && e.to_string().contains("GSK_API_KEY"));

    std::env::set_var("GSK_API_KEY", "gsk-env");
    let c = Client::builder().build().unwrap();
    assert_eq!(c.base_url(), "https://www.genspark.ai/api/sb-box/v1");
    std::env::set_var("SBBOX_BASE_URL", "https://example.test/api/sb-box/v1/");
    let c = Client::builder().build().unwrap();
    assert_eq!(c.base_url(), "https://example.test/api/sb-box/v1");
    std::env::remove_var("SBBOX_BASE_URL");
    std::env::remove_var("GSK_API_KEY");

    let e = Client::builder()
        .api_key("k")
        .base_url("ftp://x")
        .build()
        .unwrap_err();
    assert!(matches!(e, Error::Config(_)));
}

#[tokio::test]
async fn auth_header_user_agent_and_account_calls() {
    let server = MockServer::start().await;
    Mock::given(method("GET"))
        .and(path("/v1/regions"))
        .and(header("x-api-key", KEY))
        .respond_with(
            ResponseTemplate::new(200)
                .set_body_json(json!({"regions":["westus2"],"default":"westus2"})),
        )
        .expect(1)
        .mount(&server)
        .await;
    Mock::given(method("GET"))
        .and(path("/v1/limits"))
        .respond_with(ResponseTemplate::new(200).set_body_json(json!({
            "plan":"free","planRequired":false,
            "limits":{"maxConcurrentSandboxes":2,"maxCpu":2,"maxMemoryMB":4096,"maxDiskGB":20,"maxConcurrentBuilds":1},
            "usage":{"runningSandboxes":1,"buildingTemplates":0}})))
        .mount(&server)
        .await;
    Mock::given(method("GET"))
        .and(path("/v1/usage"))
        .and(query_param("days", "7"))
        .respond_with(ResponseTemplate::new(200).set_body_json(json!({
            "days":7,"from":"20261028","to":"20261104","rateCard":{"x":1},
            "direct":{"runSec":61,"cpuCoreSec":62,"memGBSec":123,"diskGBSec":1204,"credits":1.5},
            "daily":[{"day":"20261104","direct":{"run_sec":31,"cpu_core_sec":32,"mem_gb_sec":63,"disk_gb_sec":604,"credits":0.75}}]})))
        .mount(&server)
        .await;

    let c = Client::builder()
        .api_key(KEY)
        .base_url(format!("{}/v1/", server.uri()))
        .user_agent_suffix("myapp/2.0")
        .build()
        .unwrap();
    let r = c.regions().await.unwrap();
    assert_eq!(r.default.as_deref(), Some("westus2"));
    let l = c.limits().await.unwrap();
    assert_eq!(l.limits.max_memory_mb, Some(4096));
    assert_eq!(l.usage.running_sandboxes, Some(1));
    let u = c.usage(7).await.unwrap();
    // Account-level totals arrive camelCase, daily rows snake_case: every field of both.
    assert_eq!(u.direct.run_sec, 61.0);
    assert_eq!(u.direct.cpu_core_sec, 62.0);
    assert_eq!(u.direct.mem_gb_sec, 123.0);
    assert_eq!(u.direct.disk_gb_sec, 1204.0);
    assert_eq!(u.direct.credits, 1.5);
    let d = &u.daily[0].direct;
    assert_eq!(d.run_sec, 31.0);
    assert_eq!(d.cpu_core_sec, 32.0);
    assert_eq!(d.mem_gb_sec, 63.0);
    assert_eq!(d.disk_gb_sec, 604.0);
    assert_eq!(d.credits, 0.75);

    let req = &server.received_requests().await.unwrap()[0];
    let ua = req.headers.get("user-agent").unwrap().to_str().unwrap();
    assert!(
        ua.starts_with(concat!("sbbox-rust-sdk/", env!("CARGO_PKG_VERSION"))),
        "{ua}"
    );
    assert!(ua.ends_with("myapp/2.0"), "{ua}");
}

#[tokio::test]
async fn create_get_list_kill() {
    let server = MockServer::start().await;
    Mock::given(method("POST"))
        .and(path("/v1/sandboxes"))
        .and(body_json(json!({"templateName":"base","timeout":300,"lifecycle":{"onTimeout":"kill"},"metadata":{"a":"b"}})))
        .respond_with(ResponseTemplate::new(200).set_body_json(record("sbx-1", "running")))
        .expect(1)
        .mount(&server)
        .await;
    Mock::given(method("GET"))
        .and(path("/v1/sandboxes/sbx-1"))
        .respond_with(ResponseTemplate::new(200).set_body_json(record("sbx-1", "running")))
        .mount(&server)
        .await;
    Mock::given(method("GET"))
        .and(path("/v1/sandboxes"))
        .and(query_param("state", "running,frozen"))
        .respond_with(ResponseTemplate::new(200).set_body_json(json!({"sandboxes":[
            {"id":"sbx-1","template_id":"tpl-1","state":"running","region":"westus2","portUrl":"x","domain":"d","cogen_id":"u","created_at":"t","updated_at":"t"}],"hasMore":false})))
        .mount(&server)
        .await;
    Mock::given(method("DELETE"))
        .and(path("/v1/sandboxes/sbx-1"))
        .respond_with(ResponseTemplate::new(200).set_body_json(json!({"status":"killed"})))
        .expect(1)
        .mount(&server)
        .await;

    let c = client(&server).await;
    let mut m = HashMap::new();
    m.insert("a".to_string(), "b".to_string());
    let req = CreateSandboxRequest::base()
        .timeout(300)
        .on_timeout(OnTimeout::Kill)
        .metadata(m);
    let mut sb = c.sandboxes().create(&req).await.unwrap();
    assert_eq!(sb.id(), "sbx-1");
    assert_eq!(
        sb.port_url(8080).unwrap(),
        "https://8080-sbx-1.sandbox.example"
    );
    assert_eq!(sb.info().versions[0].version, 1);
    assert_eq!(sb.info().extra["unknownField"], 7);
    sb.refresh().await.unwrap();

    let list = c.sandboxes().list(&["running", "frozen"]).await.unwrap();
    assert_eq!(list[0].template_id.as_deref(), Some("tpl-1"));
    sb.kill().await.unwrap();

    // Missing template → client-side config error, no request.
    let e = c
        .sandboxes()
        .create(&CreateSandboxRequest::default())
        .await
        .unwrap_err();
    assert!(matches!(e, Error::Config(_)));
}

struct Seq {
    n: Arc<AtomicUsize>,
    responses: Vec<ResponseTemplate>,
}
impl Respond for Seq {
    fn respond(&self, _: &Request) -> ResponseTemplate {
        let i = self.n.fetch_add(1, Ordering::SeqCst);
        self.responses[i.min(self.responses.len() - 1)].clone()
    }
}

#[tokio::test]
async fn freeze_200_202_poll_and_resume() {
    let server = MockServer::start().await;
    // First freeze: 200 record. Second: 202 → poll (freezing, then frozen).
    Mock::given(method("POST"))
        .and(path("/v1/sandboxes/sbx-1/freeze"))
        .respond_with(Seq {
            n: Arc::new(AtomicUsize::new(0)),
            responses: vec![
                ResponseTemplate::new(200).set_body_json(record("sbx-1", "frozen")),
                ResponseTemplate::new(202)
                    .set_body_json(json!({"state":"freezing","status":"freezing"})),
            ],
        })
        .mount(&server)
        .await;
    Mock::given(method("GET"))
        .and(path("/v1/sandboxes/sbx-1"))
        .respond_with(Seq {
            n: Arc::new(AtomicUsize::new(0)),
            responses: vec![
                ResponseTemplate::new(200).set_body_json(record("sbx-1", "freezing")),
                ResponseTemplate::new(200).set_body_json(record("sbx-1", "frozen")),
            ],
        })
        .mount(&server)
        .await;
    Mock::given(method("POST"))
        .and(path("/v1/sandboxes/sbx-1/resume"))
        .and(body_json(json!({"timeout": 120})))
        .respond_with(ResponseTemplate::new(200).set_body_json(record("sbx-1", "running")))
        .mount(&server)
        .await;

    let c = client(&server).await;
    let info = c.sandboxes().freeze("sbx-1", true).await.unwrap();
    assert_eq!(info.state, "frozen");
    let t = std::time::Instant::now();
    let info = c.sandboxes().freeze("sbx-1", true).await.unwrap();
    assert_eq!(info.state, "frozen");
    assert!(
        t.elapsed() >= Duration::from_secs(2),
        "polled with a 2 s interval"
    );
    assert_eq!(
        server
            .received_requests()
            .await
            .unwrap()
            .iter()
            .filter(|r| r.method == "GET")
            .count(),
        2
    );
    let info = c.sandboxes().resume("sbx-1", Some(120)).await.unwrap();
    assert_eq!(info.state, "running");
}

#[tokio::test]
async fn rollback_clone_retry_after_timeout_reset_metrics() {
    let server = MockServer::start().await;
    Mock::given(method("POST"))
        .and(path("/v1/sandboxes/sbx-1/rollback"))
        .and(body_json(json!({"version": 0})))
        .respond_with(ResponseTemplate::new(200).set_body_json(record("sbx-1", "running")))
        .mount(&server)
        .await;
    Mock::given(method("POST"))
        .and(path("/v1/sandboxes/sbx-1/clone"))
        .and(body_json(json!({"version": 2, "metadata": {"m": "1"}})))
        .respond_with(Seq {
            n: Arc::new(AtomicUsize::new(0)),
            responses: vec![
                ResponseTemplate::new(503)
                    .insert_header("retry-after", "0.2")
                    .set_body_json(json!({"detail":"clone still starting"})),
                ResponseTemplate::new(200).set_body_json(record("sbx-2", "running")),
            ],
        })
        .expect(2)
        .mount(&server)
        .await;
    Mock::given(method("POST"))
        .and(path("/v1/sandboxes/sbx-1/timeout"))
        .and(body_json(json!({"timeout": 0})))
        .respond_with(ResponseTemplate::new(200).set_body_json(json!({"status":"ok"})))
        .expect(1)
        .mount(&server)
        .await;
    Mock::given(method("POST"))
        .and(path("/v1/sandboxes/sbx-1/reset"))
        .respond_with(ResponseTemplate::new(200).set_body_json(record("sbx-1", "running")))
        .mount(&server)
        .await;
    Mock::given(method("GET"))
        .and(path("/v1/sandboxes/sbx-1/metrics"))
        .and(query_param("start", "1"))
        .and(query_param("end", "2"))
        .respond_with(ResponseTemplate::new(200).set_body_json(json!([
            {"timestamp":"t","timestampUnix":1,"cpuCount":1,"cpuUsedPct":12.5,"memUsed":1,"memTotal":2,"memCache":0,"diskUsed":3,"diskTotal":4}])))
        .mount(&server)
        .await;

    let c = client(&server).await;
    let sb = c.sandboxes().get("sbx-1").await;
    assert!(sb.is_err()); // no GET mock for the record itself here; use the service API
    c.sandboxes().rollback("sbx-1", 0, None).await.unwrap();
    let mut m = HashMap::new();
    m.insert("m".to_string(), "1".to_string());
    let cl = c
        .sandboxes()
        .clone_sandbox("sbx-1", 2, Some(&m))
        .await
        .unwrap();
    assert_eq!(cl.id(), "sbx-2");
    c.sandboxes().set_timeout("sbx-1", 0).await.unwrap();
    assert_eq!(c.sandboxes().reset("sbx-1").await.unwrap().state, "running");
    let pts = c
        .sandboxes()
        .metrics("sbx-1", Some(1), Some(2))
        .await
        .unwrap();
    assert_eq!(pts[0].cpu_used_pct, Some(12.5));
}

#[tokio::test]
async fn exec_nonzero_exit_and_timeout() {
    let server = MockServer::start().await;
    Mock::given(method("GET"))
        .and(path("/v1/sandboxes/sbx-1"))
        .respond_with(ResponseTemplate::new(200).set_body_json(record("sbx-1", "running")))
        .mount(&server)
        .await;
    Mock::given(method("POST"))
        .and(path("/v1/sandboxes/sbx-1/exec"))
        .and(body_json(
            json!({"command":["/bin/sh","-c","exit 3"],"timeout":60}),
        ))
        .respond_with(
            ResponseTemplate::new(200)
                .set_body_json(json!({"stdout":"","stderr":"boom","exitCode":3})),
        )
        .mount(&server)
        .await;
    Mock::given(method("POST"))
        .and(path("/v1/sandboxes/sbx-1/exec"))
        .and(body_json(json!({"command":["sleep","100"],"cwd":"/tmp","envVars":{"A":"1"},"user":"root","timeout":1})))
        .respond_with(ResponseTemplate::new(408).set_body_json(json!({"error":"command timed out after 1s","stage":"timeout","killed":true})))
        .mount(&server)
        .await;

    let c = client(&server).await;
    let sb = c.sandboxes().get("sbx-1").await.unwrap();
    let r = sb.exec("exit 3", &ExecOptions::default()).await.unwrap();
    assert_eq!(r.exit_code, 3);
    assert!(!r.success());
    let mut env = HashMap::new();
    env.insert("A".to_string(), "1".to_string());
    let opts = ExecOptions {
        cwd: Some("/tmp".into()),
        env: Some(env),
        user: Some("root".into()),
        timeout: Some(1),
    };
    let e = sb.exec(&["sleep", "100"], &opts).await.unwrap_err();
    match e {
        Error::CommandTimeout { killed, body } => {
            assert!(killed);
            assert_eq!(body.status, 408);
            assert!(body.message.contains("timed out"));
        }
        other => panic!("{other:?}"),
    }
}

#[tokio::test]
async fn exec_stream_and_background_commands() {
    let server = MockServer::start().await;
    Mock::given(method("GET"))
        .and(path("/v1/sandboxes/sbx-1"))
        .respond_with(ResponseTemplate::new(200).set_body_json(record("sbx-1", "running")))
        .mount(&server)
        .await;
    Mock::given(method("POST"))
        .and(path("/v1/sandboxes/sbx-1/sse/exec"))
        .and(header("accept", "text/event-stream"))
        .respond_with(sse(
            "event: execId\ndata: {\"execId\":\"e1\"}\n\n: keep-alive\n\nid: 1\nevent: stdout\ndata: {\"data\":\"hello\\n\"}\n\nid: 2\nevent: stderr\ndata: {\"data\":\"warn\"}\n\nevent: exit\ndata: {\"exitCode\":0}\n\n",
        ))
        .mount(&server)
        .await;
    Mock::given(method("POST"))
        .and(path("/v1/sandboxes/sbx-1/commands/start"))
        .and(body_json(json!({"command":["cat"]})))
        .respond_with(ResponseTemplate::new(200).set_body_json(json!({"execId":"bg1","pid":42})))
        .mount(&server)
        .await;
    Mock::given(method("POST"))
        .and(path("/v1/sandboxes/sbx-1/commands/bg1/input"))
        .and(body_json(json!({"data":"abc"})))
        .respond_with(ResponseTemplate::new(200).set_body_json(json!({"written":3})))
        .mount(&server)
        .await;
    Mock::given(method("POST"))
        .and(path("/v1/sandboxes/sbx-1/commands/bg1/close"))
        .respond_with(ResponseTemplate::new(200).set_body_json(json!({"status":"ok"})))
        .mount(&server)
        .await;
    Mock::given(method("POST"))
        .and(path("/v1/sandboxes/sbx-1/commands/bg1/wait"))
        .respond_with(ResponseTemplate::new(200).set_body_json(json!({"exitCode":0})))
        .mount(&server)
        .await;
    Mock::given(method("GET"))
        .and(path("/v1/sandboxes/sbx-1/commands"))
        .respond_with(
            ResponseTemplate::new(200)
                .set_body_json(json!([{"execId":"bg1","pid":42,"sandboxId":"sbx-1"}])),
        )
        .mount(&server)
        .await;
    Mock::given(method("POST"))
        .and(path("/v1/sandboxes/sbx-1/commands/kill"))
        .and(body_json(json!({"pid":42,"signal":9})))
        .respond_with(ResponseTemplate::new(200).set_body_json(json!({"status":"ok"})))
        .mount(&server)
        .await;
    Mock::given(method("GET"))
        .and(path("/v1/sandboxes/sbx-1/commands/bg1/connect"))
        .respond_with(sse(
            "id: 1\nevent: stdout\ndata: {\"data\":\"abc\"}\n\nid: 2\nevent: stderr\ndata: {\"data\":\"warn\"}\n\nevent: gap\ndata: {\"error\":\"output gap — ring evicted unread bytes\"}\n\nevent: done\ndata: {\"status\":\"disconnected\"}\n\n",
        ))
        .mount(&server)
        .await;

    let c = client(&server).await;
    let sb = c.sandboxes().get("sbx-1").await.unwrap();
    let evs: Vec<ExecEvent> = sb
        .exec_stream("echo hello", &ExecOptions::default())
        .await
        .unwrap()
        .map(|e| e.unwrap())
        .collect()
        .await;
    assert_eq!(
        evs,
        vec![
            ExecEvent::Started {
                exec_id: "e1".into()
            },
            ExecEvent::Stdout("hello\n".into()),
            ExecEvent::Stderr("warn".into()),
            ExecEvent::Exit { exit_code: 0 },
        ]
    );

    let bg = sb
        .commands()
        .start(&["cat"], &ExecOptions::default())
        .await
        .unwrap();
    assert_eq!(bg.exec_id(), "bg1");
    assert_eq!(bg.input("abc").await.unwrap(), 3);
    bg.close().await.unwrap();
    let evs: Vec<_> = bg
        .connect()
        .await
        .unwrap()
        .map(|e| e.unwrap())
        .collect()
        .await;
    assert_eq!(
        evs,
        vec![
            ExecEvent::Stdout("abc".into()),
            ExecEvent::Stderr("warn".into()),
            ExecEvent::Gap {
                message: "output gap — ring evicted unread bytes".into()
            },
            ExecEvent::Done,
        ]
    );
    let seqs: Vec<_> = bg
        .connect_with_seq(0)
        .await
        .unwrap()
        .map(|e| e.unwrap().0)
        .collect()
        .await;
    assert_eq!(seqs, vec![Some(1), Some(2), None, None]);
    let n = bg.connect_from(2).await.unwrap().count().await;
    assert_eq!(
        n, 4,
        "mock ignores from_seq; the request must still succeed"
    );
    let reqs = server.received_requests().await.unwrap();
    assert!(
        reqs.iter()
            .any(|r| r.url.path().ends_with("/connect") && r.url.query() == Some("from_seq=2")),
        "connect_from(2) sends ?from_seq=2"
    );
    assert_eq!(bg.wait().await.unwrap(), 0);
    assert_eq!(sb.commands().list().await.unwrap()[0].pid, Some(42));
    bg.kill(None).await.unwrap();
}

#[tokio::test]
async fn run_code_results_and_errors() {
    let server = MockServer::start().await;
    Mock::given(method("GET"))
        .and(path("/v1/sandboxes/sbx-1"))
        .respond_with(ResponseTemplate::new(200).set_body_json(record("sbx-1", "running")))
        .mount(&server)
        .await;
    Mock::given(method("POST"))
        .and(path("/v1/sandboxes/sbx-1/run-code"))
        .and(body_json(
            json!({"code":"1+1","language":"python","timeout":60}),
        ))
        .respond_with(ResponseTemplate::new(200).set_body_json(json!({
            "stdout":"","stderr":"","executionCount":1,"complete":true,"error":null,
            "results":[{"text":"2","isMainResult":true}]})))
        .mount(&server)
        .await;
    Mock::given(method("POST"))
        .and(path("/v1/sandboxes/sbx-1/run-code"))
        .and(body_json(json!({"code":"raise ValueError('x')","language":"python","timeout":10,"contextId":"ctx1","images":"omit"})))
        .respond_with(ResponseTemplate::new(200).set_body_json(json!({
            "stdout":"","stderr":"","executionCount":2,"complete":true,
            "error":{"name":"ValueError","value":"x","traceback":"..."},"results":[]})))
        .mount(&server)
        .await;
    Mock::given(method("POST"))
        .and(path("/v1/sandboxes/sbx-1/run-code"))
        .and(body_partial_json(json!({"code":"nope"})))
        .respond_with(ResponseTemplate::new(409).set_body_json(
            json!({"detail":{"code":"sb_box_interpreter_unavailable","message":"no interpreter"}}),
        ))
        .mount(&server)
        .await;

    let c = client(&server).await;
    let sb = c.sandboxes().get("sbx-1").await.unwrap();
    let r = sb
        .run_code("1+1", &RunCodeOptions::default())
        .await
        .unwrap();
    assert_eq!(r.text(), Some("2"));
    assert!(r.error.is_none());
    let opts = RunCodeOptions {
        context_id: Some("ctx1".into()),
        timeout: Some(10),
        images: Some("omit".into()),
        ..Default::default()
    };
    let r = sb.run_code("raise ValueError('x')", &opts).await.unwrap();
    assert_eq!(r.error.unwrap().name.as_deref(), Some("ValueError"));
    let e = sb
        .run_code("nope", &RunCodeOptions::default())
        .await
        .unwrap_err();
    assert!(matches!(e, Error::InterpreterUnavailable { .. }), "{e:?}");
}

#[tokio::test]
async fn files_bytes_round_trip_and_list() {
    let server = MockServer::start().await;
    let payload: Vec<u8> = (0..=255u8).collect();
    Mock::given(method("GET"))
        .and(path("/v1/sandboxes/sbx-1"))
        .respond_with(ResponseTemplate::new(200).set_body_json(record("sbx-1", "running")))
        .mount(&server)
        .await;
    Mock::given(method("POST"))
        .and(path("/v1/sandboxes/sbx-1/files/stream"))
        .and(query_param("path", "/home/user/a b.bin"))
        .and(query_param("user", "root"))
        .and(header("content-type", "application/octet-stream"))
        .and(wiremock::matchers::body_bytes(payload.clone()))
        .respond_with(ResponseTemplate::new(200).set_body_json(json!({"written":256})))
        .expect(1)
        .mount(&server)
        .await;
    Mock::given(method("GET"))
        .and(path("/v1/sandboxes/sbx-1/files/stream"))
        .and(query_param("path", "/home/user/a b.bin"))
        .respond_with(ResponseTemplate::new(200).set_body_bytes(payload.clone()))
        .mount(&server)
        .await;
    Mock::given(method("GET"))
        .and(path("/v1/sandboxes/sbx-1/files/stream"))
        .and(query_param("path", "/missing"))
        .respond_with(ResponseTemplate::new(404).set_body_json(json!({"detail":"file not found"})))
        .mount(&server)
        .await;
    Mock::given(method("GET"))
        .and(path("/v1/sandboxes/sbx-1/files/list"))
        .and(query_param("path", "/home/user"))
        .respond_with(ResponseTemplate::new(200).set_body_json(
            json!([{"name":"a b.bin","path":"/home/user/a b.bin","isDir":false,"size":256}]),
        ))
        .mount(&server)
        .await;

    let c = client(&server).await;
    let sb = c.sandboxes().get("sbx-1").await.unwrap();
    assert_eq!(
        sb.files()
            .write("/home/user/a b.bin", payload.clone(), Some("root"))
            .await
            .unwrap(),
        256
    );
    let back = sb.files().read("/home/user/a b.bin", None).await.unwrap();
    assert_eq!(&back[..], &payload[..]);
    let e = sb.files().read("/missing", None).await.unwrap_err();
    assert!(e.is_not_found());
    let l = sb.files().list("/home/user", None).await.unwrap();
    assert_eq!(l[0].size, Some(256));
    assert!(!l[0].is_dir);
}

#[tokio::test]
async fn pty_http_endpoints() {
    let server = MockServer::start().await;
    Mock::given(method("GET"))
        .and(path("/v1/sandboxes/sbx-1"))
        .respond_with(ResponseTemplate::new(200).set_body_json(record("sbx-1", "running")))
        .mount(&server)
        .await;
    Mock::given(method("POST"))
        .and(path("/v1/sandboxes/sbx-1/pty"))
        .and(body_json(
            json!({"command":["/bin/bash"],"rows":24,"cols":80}),
        ))
        .respond_with(ResponseTemplate::new(200).set_body_json(json!({"execId":"p1","pid":7})))
        .mount(&server)
        .await;
    Mock::given(method("POST"))
        .and(path("/v1/sandboxes/sbx-1/pty/p1/input"))
        .and(wiremock::matchers::body_bytes(b"ls\n".to_vec()))
        .respond_with(ResponseTemplate::new(200).set_body_json(json!({"status":"ok"})))
        .mount(&server)
        .await;
    Mock::given(method("POST"))
        .and(path("/v1/sandboxes/sbx-1/pty/p1/resize"))
        .and(body_json(json!({"rows":40,"cols":120})))
        .respond_with(ResponseTemplate::new(200).set_body_json(json!({"status":"ok"})))
        .mount(&server)
        .await;
    Mock::given(method("GET"))
        .and(path("/v1/sandboxes/sbx-1/pty/p1/connect"))
        .respond_with(sse("event: data\ndata: {\"data\":\"$ \"}\n\nevent: exit\ndata: {\"reason\":\"eof\",\"exitCode\":0}\n\n"))
        .mount(&server)
        .await;
    Mock::given(method("POST"))
        .and(path("/v1/sandboxes/sbx-1/pty/p1/kill"))
        .respond_with(ResponseTemplate::new(200).set_body_json(json!({"status":"ok"})))
        .mount(&server)
        .await;

    let c = client(&server).await;
    let sb = c.sandboxes().get("sbx-1").await.unwrap();
    let p = sb.pty().open(&PtyOptions::default()).await.unwrap();
    assert_eq!(p.pid(), Some(7));
    p.input("ls\n").await.unwrap();
    p.resize(40, 120).await.unwrap();
    let evs: Vec<_> = p
        .connect()
        .await
        .unwrap()
        .map(|e| e.unwrap())
        .collect()
        .await;
    assert_eq!(
        evs,
        vec![
            ExecEvent::Data("$ ".into()),
            ExecEvent::Exit { exit_code: 0 }
        ]
    );
    p.kill().await.unwrap();
    assert!(sb.pty().ws_url().starts_with("ws://"));
    assert!(sb.pty().ws_url().ends_with("/v1/sandboxes/sbx-1/ws/pty"));
}

#[tokio::test]
async fn templates_build_wait_ready_and_grants() {
    let server = MockServer::start().await;
    Mock::given(method("POST"))
        .and(path("/v1/templates/build"))
        .and(body_json(
            json!({"name":"py","image":"python:3.12","cpuCount":2,"memoryMB":4096}),
        ))
        .respond_with(ResponseTemplate::new(200).set_body_json(
            json!({"templateId":"tpl-9","status":"building","region":"westus2","owner":"me"}),
        ))
        .mount(&server)
        .await;
    Mock::given(method("GET"))
        .and(path("/v1/templates/tpl-9"))
        .respond_with(Seq {
            n: Arc::new(AtomicUsize::new(0)),
            responses: vec![
                ResponseTemplate::new(200).set_body_json(json!({"id":"tpl-9","name":"py","namespace":"private","status":"building","is_owner":true,"can_create":false})),
                ResponseTemplate::new(200).set_body_json(json!({"id":"tpl-9","name":"py","namespace":"private","status":"ready","is_owner":true,"can_create":true,"cpuCount":2,"memoryMB":4096})),
            ],
        })
        .mount(&server)
        .await;
    Mock::given(method("GET"))
        .and(path("/v1/templates/tpl-bad"))
        .respond_with(ResponseTemplate::new(200).set_body_json(
            json!({"id":"tpl-bad","status":"error","buildError":"image pull failed"}),
        ))
        .mount(&server)
        .await;
    Mock::given(method("GET"))
        .and(path("/v1/templates"))
        .respond_with(
            ResponseTemplate::new(200)
                .set_body_json(json!({"templates":[{"id":"tpl-9","status":"ready"}]})),
        )
        .mount(&server)
        .await;
    Mock::given(method("GET"))
        .and(path("/v1/templates/shared"))
        .respond_with(
            ResponseTemplate::new(200)
                .set_body_json(json!([{"id":"tpl-s","status":"ready","namespace":"public"}])),
        )
        .mount(&server)
        .await;
    Mock::given(method("PUT"))
        .and(path("/v1/templates/tpl-9/grants/user-2"))
        .and(body_json(json!({"perm":"write"})))
        .respond_with(ResponseTemplate::new(200).set_body_json(
            json!({"template_id":"tpl-9","grantee_id":"user-2","permission":"write"}),
        ))
        .mount(&server)
        .await;
    Mock::given(method("GET"))
        .and(path("/v1/templates/tpl-9/grants"))
        .respond_with(ResponseTemplate::new(200).set_body_json(json!([{"grantee_id":"user-2","permission":"write","granted_at":"t","expires_at":null}])))
        .mount(&server)
        .await;
    Mock::given(method("DELETE"))
        .and(path("/v1/templates/tpl-9/grants/user-2"))
        .respond_with(ResponseTemplate::new(200).set_body_json(json!({"status":"revoked"})))
        .mount(&server)
        .await;
    Mock::given(method("DELETE"))
        .and(path("/v1/templates/tpl-9"))
        .respond_with(ResponseTemplate::new(200).set_body_json(json!({"status":"deleted"})))
        .mount(&server)
        .await;

    let c = client(&server).await;
    let t = c.templates();
    let req = TemplateBuildRequest {
        name: Some("py".into()),
        image: Some("python:3.12".into()),
        cpu_count: Some(2),
        memory_mb: Some(4096),
        ..Default::default()
    };
    let b = t.build(&req).await.unwrap();
    assert_eq!(b.template_id, "tpl-9");
    let ready = t
        .wait_ready(
            "tpl-9",
            Some(Duration::from_secs(10)),
            Some(Duration::from_millis(50)),
        )
        .await
        .unwrap();
    assert_eq!(ready.status.as_deref(), Some("ready"));
    assert_eq!(ready.memory_mb, Some(4096));
    let e = t
        .wait_ready("tpl-bad", None, Some(Duration::from_millis(10)))
        .await
        .unwrap_err();
    match e {
        Error::TemplateBuild {
            template_id,
            build_error,
        } => {
            assert_eq!(template_id, "tpl-bad");
            assert_eq!(build_error, "image pull failed");
        }
        other => panic!("{other:?}"),
    }
    assert_eq!(t.list().await.unwrap().len(), 1);
    assert_eq!(
        t.shared().await.unwrap()[0].namespace.as_deref(),
        Some("public")
    );
    let g = GrantRequest {
        perm: Some("write".into()),
        ..Default::default()
    };
    t.grant("tpl-9", "user-2", &g).await.unwrap();
    assert_eq!(
        t.grants("tpl-9").await.unwrap()[0].permission.as_deref(),
        Some("write")
    );
    t.revoke("tpl-9", "user-2").await.unwrap();
    t.delete("tpl-9").await.unwrap();
    let e = t.build(&TemplateBuildRequest::default()).await.unwrap_err();
    assert!(matches!(e, Error::Config(_)));
}

#[tokio::test]
async fn error_mapping_table() {
    let server = MockServer::start().await;
    let cases: Vec<(u16, Value)> = vec![
        (400, json!({"detail":"bad"})),
        (401, json!({"detail":"invalid api key"})),
        (
            402,
            json!({"detail":{"code":"sb_box_insufficient_credits","balance":-1.5,"message":"no credits"}}),
        ),
        (
            403,
            json!({"detail":{"code":"sb_box_plan_required","plan":"plus","upgrade":"https://x","message":"upgrade"}}),
        ),
        (403, json!({"detail":"permission denied"})),
        (404, json!({"detail":"sandbox not found"})),
        (
            404,
            json!({"detail":{"code":"sb_box_region_retired","region":"eastus","message":"retired"}}),
        ),
        (
            409,
            json!({"detail":{"code":"sb_box_spec_limit","plan":"free","limit":{"cpu":2},"requested":{"cpu":4},"message":"too big"}}),
        ),
        (409, json!({"detail":"sandbox is freezing"})),
        (
            410,
            json!({"error":"disk lost","reason":"disk_lost","recovery":["rollback"],"resumable":false}),
        ),
        (
            422,
            json!({"status":-2,"message":"validation","errors":[{"loc":["body","timeout"],"msg":"bad"}]}),
        ),
        (428, json!({"reason":"memory_expired","recovery":["reset"]})),
        (499, json!({"detail":"client closed"})),
        (
            502,
            json!({"detail":{"code":"sb_box_interpreter_start_failed","message":"boom"}}),
        ),
        (500, json!({"detail":"internal"})),
    ];
    for (i, (status, body)) in cases.iter().enumerate() {
        Mock::given(method("GET"))
            .and(path(format!("/v1/sandboxes/case-{i}")))
            .respond_with(
                ResponseTemplate::new(*status)
                    .insert_header("x-request-id", "req-1")
                    .set_body_json(body.clone()),
            )
            .mount(&server)
            .await;
    }
    let c = Client::builder()
        .api_key(KEY)
        .base_url(format!("{}/v1", server.uri()))
        .retries(0)
        .build()
        .unwrap();
    let mut got = Vec::new();
    for i in 0..cases.len() {
        got.push(c.sandboxes().get(&format!("case-{i}")).await.unwrap_err());
    }
    assert!(matches!(got[0], Error::Validation { .. }));
    assert!(matches!(got[1], Error::Authentication { .. }));
    assert!(matches!(&got[2], Error::InsufficientCredits { balance: Some(b), .. } if *b == -1.5));
    assert!(
        matches!(&got[3], Error::PlanRequired { plan: Some(p), upgrade: Some(_), .. } if p == "plus")
    );
    assert!(matches!(got[4], Error::Forbidden { .. }));
    assert!(matches!(got[5], Error::NotFound { .. }) && got[5].is_not_found());
    assert!(
        matches!(&got[6], Error::RegionRetired { region: Some(r), .. } if r == "eastus")
            && got[6].is_not_found()
    );
    match &got[7] {
        Error::Limit { info, .. } => {
            assert_eq!(info.code, "sb_box_spec_limit");
            assert_eq!(info.plan.as_deref(), Some("free"));
            assert_eq!(info.limit, Some(json!({"cpu":2})));
            assert_eq!(info.requested, Some(json!({"cpu":4})));
        }
        other => panic!("{other:?}"),
    }
    assert!(matches!(got[8], Error::Conflict { .. }));
    assert!(
        matches!(&got[9], Error::DiskLost { recovery, .. } if recovery == &vec!["rollback".to_string()])
    );
    assert_eq!(got[9].code(), Some("disk_lost"));
    assert!(matches!(&got[10], Error::Validation { errors, .. } if errors.len() == 1));
    assert!(
        matches!(&got[11], Error::MemoryExpired { recovery, .. } if recovery == &vec!["reset".to_string()])
    );
    assert!(matches!(got[12], Error::ClientDisconnected { .. }));
    assert!(matches!(got[13], Error::InterpreterStartFailed { .. }));
    assert!(matches!(got[14], Error::Server { .. }));
    assert_eq!(got[14].status(), Some(500));
    assert_eq!(got[14].body().unwrap().request_id.as_deref(), Some("req-1"));
    assert!(got[3].to_string().contains("sb_box_plan_required"));
}

#[tokio::test]
async fn idempotent_retry_on_503_and_never_on_create() {
    let server = MockServer::start().await;
    Mock::given(method("GET"))
        .and(path("/v1/sandboxes/sbx-1"))
        .respond_with(Seq {
            n: Arc::new(AtomicUsize::new(0)),
            responses: vec![
                ResponseTemplate::new(503).set_body_json(json!({"detail":"busy"})),
                ResponseTemplate::new(200).set_body_json(record("sbx-1", "running")),
            ],
        })
        .expect(2)
        .mount(&server)
        .await;
    Mock::given(method("POST"))
        .and(path("/v1/sandboxes"))
        .respond_with(ResponseTemplate::new(503).set_body_json(json!({"detail":"busy"})))
        .expect(1)
        .mount(&server)
        .await;
    let c = client(&server).await;
    assert_eq!(c.sandboxes().get("sbx-1").await.unwrap().state(), "running");
    let e = c
        .sandboxes()
        .create(&CreateSandboxRequest::base())
        .await
        .unwrap_err();
    assert!(matches!(e, Error::Server { .. }));
    assert_eq!(e.status(), Some(503));
}

#[tokio::test]
async fn redacts_key_in_errors() {
    let server = MockServer::start().await;
    Mock::given(method("GET"))
        .and(path("/v1/sandboxes/sbx-1"))
        .respond_with(
            ResponseTemplate::new(401)
                .set_body_json(json!({"detail": format!("key {KEY} rejected")})),
        )
        .mount(&server)
        .await;
    let c = client(&server).await;
    let e = c.sandboxes().get("sbx-1").await.unwrap_err();
    let s = e.to_string();
    assert!(!s.contains(KEY), "{s}");
    assert!(s.contains("gsk-REDACTED"), "{s}");
    assert!(!e.body().unwrap().message.contains(KEY));
    // Transport errors are redacted too.
    let c = Client::builder()
        .api_key(KEY)
        .base_url(format!("http://127.0.0.1:1/{KEY}"))
        .retries(0)
        .build()
        .unwrap();
    let e = c.regions().await.unwrap_err();
    assert!(matches!(e, Error::Transport(_)));
    assert!(!e.to_string().contains(KEY), "{e}");
}

#[cfg(feature = "blocking")]
#[test]
fn blocking_client_round_trip() {
    let rt = tokio::runtime::Runtime::new().unwrap();
    let server = rt.block_on(MockServer::start());
    rt.block_on(async {
        Mock::given(method("POST"))
            .and(path("/v1/sandboxes"))
            .respond_with(ResponseTemplate::new(200).set_body_json(record("sbx-1", "running")))
            .mount(&server)
            .await;
        Mock::given(method("POST"))
            .and(path("/v1/sandboxes/sbx-1/exec"))
            .respond_with(
                ResponseTemplate::new(200)
                    .set_body_json(json!({"stdout":"hi\n","stderr":"","exitCode":0})),
            )
            .mount(&server)
            .await;
        Mock::given(method("DELETE"))
            .and(path("/v1/sandboxes/sbx-1"))
            .respond_with(ResponseTemplate::new(200).set_body_json(json!({"status":"killed"})))
            .mount(&server)
            .await;
    });
    let c = sbbox::blocking::Client::build(
        Client::builder()
            .api_key(KEY)
            .base_url(format!("{}/v1", server.uri())),
    )
    .unwrap();
    let sb = c.create_sandbox(&CreateSandboxRequest::base()).unwrap();
    assert_eq!(
        sb.exec("echo hi", &ExecOptions::default()).unwrap().stdout,
        "hi\n"
    );
    sb.kill().unwrap();
}
