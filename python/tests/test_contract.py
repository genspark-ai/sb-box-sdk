"""Every endpoint in API.md: method + path + body shape as sent on the wire."""

import pytest


def _p(req):
    return req.method, req.url.path.replace("/api/sb-box/v1", ""), req.url.params


CASES = [
    (lambda c: c.regions(), "GET", "/regions"),
    (lambda c: c.limits(), "GET", "/limits"),
    (lambda c: c.usage(), "GET", "/usage"),
    (lambda c: c.templates.build(image="i"), "POST", "/templates/build"),
    (lambda c: c.templates.list(), "GET", "/templates"),
    (lambda c: c.templates.shared(), "GET", "/templates/shared"),
    (lambda c: c.templates.get("tpl-base"), "GET", "/templates/tpl-base"),
    (lambda c: c.templates.delete("tpl-base"), "DELETE", "/templates/tpl-base"),
    (lambda c: c.templates.grant("tpl-base", "u"), "PUT", "/templates/tpl-base/grants/u"),
    (lambda c: c.templates.grants("tpl-base"), "GET", "/templates/tpl-base/grants"),
    (lambda c: c.templates.revoke("tpl-base", "u"), "DELETE", "/templates/tpl-base/grants/u"),
    (lambda c: c.sandboxes.create(), "POST", "/sandboxes"),
    (lambda c: c.sandboxes.list(), "GET", "/sandboxes"),
    (lambda c: c.sandboxes.shared(), "GET", "/sandboxes/shared"),
    (lambda c: c.sandboxes.get("sbx-abc123"), "GET", "/sandboxes/sbx-abc123"),
    (lambda c: c.sandboxes.kill("sbx-abc123"), "DELETE", "/sandboxes/sbx-abc123"),
    (lambda c: c.sandboxes.freeze("sbx-abc123"), "POST", "/sandboxes/sbx-abc123/freeze"),
    (lambda c: c.sandboxes.resume("sbx-abc123"), "POST", "/sandboxes/sbx-abc123/resume"),
    (lambda c: c.sandboxes.rollback("sbx-abc123"), "POST", "/sandboxes/sbx-abc123/rollback"),
    (lambda c: c.sandboxes.set_timeout("sbx-abc123", 1), "POST", "/sandboxes/sbx-abc123/timeout"),
    (lambda c: c.sandboxes.reset("sbx-abc123"), "POST", "/sandboxes/sbx-abc123/reset"),
    (lambda c: c.sandboxes.metrics("sbx-abc123"), "GET", "/sandboxes/sbx-abc123/metrics"),
    (lambda c: c.sandboxes.grant("sbx-abc123", "u"), "PUT", "/sandboxes/sbx-abc123/grants/u"),
    (lambda c: c.sandboxes.grants("sbx-abc123"), "GET", "/sandboxes/sbx-abc123/grants"),
    (lambda c: c.sandboxes.revoke("sbx-abc123", "u"), "DELETE", "/sandboxes/sbx-abc123/grants/u"),
    (lambda c: c.sandboxes.get("sbx-abc123").exec("x"), "POST", "/sandboxes/sbx-abc123/exec"),
    (
        lambda c: list(c.sandboxes.get("sbx-abc123").exec_stream("x")),
        "POST",
        "/sandboxes/sbx-abc123/sse/exec",
    ),
    (lambda c: c.sandboxes.get("sbx-abc123").run_code("x"), "POST", "/sandboxes/sbx-abc123/run-code"),
    (
        lambda c: c.sandboxes.get("sbx-abc123").commands.start("x"),
        "POST",
        "/sandboxes/sbx-abc123/commands/start",
    ),
    (lambda c: c.sandboxes.get("sbx-abc123").commands.list(), "GET", "/sandboxes/sbx-abc123/commands"),
    (lambda c: c.sandboxes.get("sbx-abc123").commands.kill(1), "POST", "/sandboxes/sbx-abc123/commands/kill"),
    (
        lambda c: c.sandboxes.get("sbx-abc123").commands.send_stdin(1, "x"),
        "POST",
        "/sandboxes/sbx-abc123/commands/send-stdin",
    ),
    (
        lambda c: c.sandboxes.get("sbx-abc123").files.write("/p", b"x"),
        "POST",
        "/sandboxes/sbx-abc123/files/stream",
    ),
    (lambda c: c.sandboxes.get("sbx-abc123").files.list("/"), "GET", "/sandboxes/sbx-abc123/files/list"),
    (lambda c: c.sandboxes.get("sbx-abc123").pty.open(), "POST", "/sandboxes/sbx-abc123/pty"),
]


@pytest.mark.parametrize("call,method,path", CASES, ids=[f"{m} {p}" for _, m, p in CASES])
def test_endpoint(client, api, call, method, path, monkeypatch):
    monkeypatch.setattr("sbbox._client.time.sleep", lambda s: None)
    call(client)
    m, p, _ = _p(api.last)
    assert (m, p) == (method, path)
    assert api.last.headers["x-api-key"] == "gsk-test-key"


def test_background_and_pty_subroutes(client, api):
    sb = client.sandboxes.get("sbx-abc123")
    cmd = sb.commands.start("x")
    for fn, m, suffix in [
        (lambda: cmd.input("a"), "POST", "input"),
        (cmd.close, "POST", "close"),
        (cmd.wait, "POST", "wait"),
        (lambda: list(cmd.connect()), "GET", "connect"),
    ]:
        fn()
        assert _p(api.last)[:2] == (m, f"/sandboxes/sbx-abc123/commands/bg1/{suffix}")
    pty = sb.pty.open()
    for fn, suffix in [
        (lambda: pty.input("a"), "input"),
        (lambda: pty.resize(1, 1), "resize"),
        (lambda: list(pty.connect()), "connect"),
        (pty.kill, "kill"),
    ]:
        fn()
        assert _p(api.last)[1] == f"/sandboxes/sbx-abc123/pty/pty1/{suffix}"
    sb.files.write("/f", b"1")
    sb.files.read("/f")
    assert _p(api.last)[:2] == ("GET", "/sandboxes/sbx-abc123/files/stream")


def test_no_deprecated_fields_ever_sent(client, api, monkeypatch):
    monkeypatch.setattr("sbbox._client.time.sleep", lambda s: None)
    sb = client.sandboxes.create(timeout=5)
    sb.resume(timeout=5)
    sb.exec("x", timeout=5)
    for req in api.requests:
        if req.content and req.headers.get("content-type") == "application/json":
            body = req.content.decode()
            for bad in ("timeoutMs", "idleTimeoutMs", "namespace", "templateNamespace", "pause", "version"):
                assert f'"{bad}"' not in body, (req.url.path, bad)
