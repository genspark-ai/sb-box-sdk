import httpx
import pytest

import sbbox
from sbbox import errors


def test_create_defaults_to_base(client, api):
    sb = client.sandboxes.create()
    assert api.body() == {"templateName": "base"}
    assert sb.id == "sbx-abc123" and sb.state == "running"
    assert sb.port_url(3000) == "https://3000-sbx-abc123.sandbox.example.test"
    assert sb.versions[0].version == 1 and sb.metadata["k"] == "v"


def test_create_full_body(client, api):
    client.sandboxes.create(
        "py312",
        timeout=3600,
        on_timeout="kill",
        metadata={"a": "b"},
        env_vars={"X": "1"},
        network=sbbox.NetworkConfig(allow_out=["example.com"], allow_public_traffic=False),
    )
    assert api.body() == {
        "templateName": "py312",
        "timeout": 3600,
        "lifecycle": {"onTimeout": "kill"},
        "metadata": {"a": "b"},
        "envVars": {"X": "1"},
        "network": {"allowOut": ["example.com"], "allowPublicTraffic": False},
    }
    client.sandboxes.create(template_id="tpl-x")
    assert api.body() == {"templateId": "tpl-x"}


def test_create_validation(client):
    with pytest.raises(ValueError, match="metadata"):
        client.sandboxes.create(metadata={"n": 1})
    with pytest.raises(ValueError):
        client.sandboxes.create("a", template_id="b")
    with pytest.raises(ValueError):
        client.sandboxes.create(on_timeout="pause")


def test_list_get_kill(client, api):
    rows = client.sandboxes.list()
    assert rows[0].id == "sbx-abc123" and "state" not in api.last.url.params
    client.sandboxes.list(state=["running", "frozen"])
    assert api.last.url.params["state"] == "running,frozen"
    client.sandboxes.list(state="all")
    with pytest.raises(ValueError):
        client.sandboxes.list(state="killed")
    sb = client.sandboxes.get("sbx-abc123")
    assert sb.template_id == "tpl-base"
    sb.kill()
    assert api.last.method == "DELETE" and api.last.url.path.endswith("/sandboxes/sbx-abc123")
    assert sb.state == "killed"
    with pytest.raises(errors.NotFoundError):
        client.sandboxes.get("sbx-gone")
    assert client.sandboxes.shared()[0].id == "sbx-abc123"


def test_freeze_200(client, api):
    sb = client.sandboxes.get("sbx-abc123")
    sb.freeze()
    assert sb.state == "frozen" and sb.frozen_at
    assert len(api.calls("GET", "/sandboxes/sbx-abc123")) == 1  # no polling needed


def test_freeze_202_then_poll(client, api, monkeypatch):
    monkeypatch.setattr("sbbox.sandboxes.time.sleep", lambda s: None)
    sb = client.sandboxes.freeze("sbx-202")
    assert sb.state == "frozen"
    assert len(api.calls("GET", "/sandboxes/sbx-202")) == 3
    api.freeze_polls = 1
    sb2 = client.sandboxes.freeze("sbx-202", wait=False)
    assert sb2.state == "freezing"


def test_freeze_wait_timeout(client, api, monkeypatch):
    monkeypatch.setattr("sbbox.sandboxes.time.sleep", lambda s: None)
    api.routes[("POST", "/sandboxes/sbx-abc123/freeze")] = httpx.Response(202, json={"state": "freezing"})
    api.routes[("GET", "/sandboxes/sbx-abc123")] = httpx.Response(
        200, json={"id": "sbx-abc123", "state": "freezing"}
    )
    with pytest.raises(errors.WaitTimeout):
        client.sandboxes.get("sbx-abc123").freeze(timeout=0)


def test_resume_rollback_timeout_reset(client, api):
    sb = client.sandboxes.get("sbx-abc123")
    sb.resume(timeout=600)
    assert api.body() == {"timeout": 600}
    sb.resume()
    assert api.body() == {}
    sb.rollback(version=2)
    assert api.body() == {"version": 2}
    sb.set_timeout(0)
    assert api.body() == {"timeout": 0} and api.last.url.path.endswith("/timeout")
    sb.reset()
    assert api.last.url.path.endswith("/reset")
    assert sb.state == "running"


def test_resume_428_and_410(client, api):
    api.routes[("POST", "/sandboxes/sbx-abc123/resume")] = httpx.Response(
        428, json={"reason": "memory_expired", "recovery": ["reset"]}
    )
    with pytest.raises(errors.MemoryExpiredError) as ei:
        client.sandboxes.resume("sbx-abc123")
    assert ei.value.recovery == ["reset"]
    api.routes[("POST", "/sandboxes/sbx-abc123/reset")] = httpx.Response(
        410, json={"error": "disk lost", "reason": "disk_lost"}
    )
    with pytest.raises(errors.DiskLostError):
        client.sandboxes.reset("sbx-abc123")


def test_clone_honours_retry_after(client, api, monkeypatch):
    monkeypatch.setattr("sbbox._client.time.sleep", lambda s: None)
    sb = client.sandboxes.get("sbx-abc123")
    clone = sb.clone(metadata={"from": sb.id})
    assert clone.id == "sbx-clone" and api.clone_calls == 3
    assert api.body() == {"version": 0, "metadata": {"from": "sbx-abc123"}}
    assert clone.commands._id == "sbx-clone"  # the clone is a bound handle


def test_metrics(client, api):
    pts = client.sandboxes.metrics("sbx-abc123", start=1, end=2)
    assert api.last.url.params["start"] == "1" and api.last.url.params["end"] == "2"
    assert pts[0].cpu_used_pct == 2.5


def test_exec(client, api):
    sb = client.sandboxes.get("sbx-abc123")
    r = sb.exec(["echo", "hi"], cwd="/tmp", env={"A": "1"}, user="root", timeout=5)
    assert api.body() == {
        "command": ["echo", "hi"],
        "cwd": "/tmp",
        "envVars": {"A": "1"},
        "user": "root",
        "timeout": 5,
    }
    assert r.stdout == "echo hi" and r.ok
    r = sb.exec("false")
    assert api.body()["command"] == ["/bin/sh", "-c", "false"]
    assert r.exit_code == 1 and not r.ok  # non-zero exit is data, not an error
    with pytest.raises(errors.CommandTimeoutError) as ei:
        sb.exec(["sleep"])
    assert ei.value.killed
    with pytest.raises(ValueError):
        sb.exec([])


def test_exec_stream(client):
    sb = client.sandboxes.get("sbx-abc123")
    evs = list(sb.exec_stream("echo hi"))
    assert [e.kind for e in evs] == ["execId", "stdout", "stderr", "exit"]
    assert evs[0].exec_id == "e1" and evs[1].data == "hi\n" and evs[3].exit_code == 3
    assert evs[1].seq == "1"


def test_exec_stream_error_status(client, api):
    api.routes[("POST", "/sandboxes/sbx-abc123/sse/exec")] = httpx.Response(
        404, json={"detail": "sandbox not found"}
    )
    with pytest.raises(errors.NotFoundError):
        list(client.sandboxes.get("sbx-abc123").exec_stream("x"))


def test_run_code(client, api):
    sb = client.sandboxes.get("sbx-abc123")
    r = sb.run_code("1+1", context_id="ctx", images="omit")
    assert api.body() == {
        "code": "1+1",
        "language": "python",
        "contextId": "ctx",
        "timeout": 60,
        "images": "omit",
    }
    assert r.text == "2" and r.error is None and r.results[0].is_main_result
    r = sb.run_code("1/0")
    assert r.error.name == "ZeroDivisionError"
    with pytest.raises(errors.InterpreterUnavailableError):
        sb.run_code("nointerp")
    with pytest.raises(ValueError):
        sb.run_code("x", images="png")


def test_background_commands(client, api):
    sb = client.sandboxes.get("sbx-abc123")
    cmd = sb.commands.start("cat", cwd="/tmp")
    assert api.body() == {"command": ["/bin/sh", "-c", "cat"], "cwd": "/tmp"}
    assert cmd.exec_id == "bg1" and cmd.pid == 42
    assert cmd.input("hello") == 5
    assert api.body() == {"data": "hello"}
    cmd.close()
    assert cmd.wait() == 7
    evs = list(cmd.connect())
    assert [e.kind for e in evs] == ["stdout", "stderr", "gap", "done"]
    assert evs[0].seq == "1" and evs[1].seq == "2" and evs[1].data == "warn"
    assert evs[2].error.startswith("output gap") and evs[3].data == "disconnected"
    assert api.connect_from_seq is None
    assert [e.kind for e in cmd.connect(from_seq=2)] and api.connect_from_seq == "2"
    gone = type(cmd)(cmd._http, cmd.sandbox_id, "gone", cmd.pid)
    assert list(gone.connect()) == []
    cmd.kill()
    assert api.body() == {"pid": 42, "signal": 9}
    assert sb.commands.list()[0].pid == 42
    sb.commands.send_stdin(42, "x")
    assert api.body() == {"pid": 42, "data": "x"}
    sb.commands.kill(42, signal=15)
    assert api.body() == {"pid": 42, "signal": 15}


def test_files_roundtrip(client, api):
    sb = client.sandboxes.get("sbx-abc123")
    assert sb.files.write("/tmp/a.bin", b"\x00\xff") == 2
    req = api.last
    assert req.headers["content-type"] == "application/octet-stream"
    assert req.url.params["path"] == "/tmp/a.bin" and "user" not in req.url.params
    assert sb.files.read("/tmp/a.bin") == b"\x00\xff"
    sb.files.write("/tmp/t.txt", "héllo", user="root")
    assert api.last.url.params["user"] == "root"
    assert sb.files.read_text("/tmp/t.txt") == "héllo"
    with pytest.raises(errors.NotFoundError):
        sb.files.read("/nope")
    entries = sb.files.list("/tmp")
    assert entries[0].name == "a.txt" and not entries[0].is_dir and entries[0].size == 5


def test_pty(client, api):
    sb = client.sandboxes.get("sbx-abc123")
    pty = sb.pty.open(rows=40, cols=120)
    assert api.body() == {"rows": 40, "cols": 120}
    assert pty.exec_id == "pty1"
    pty.input("ls\n")
    assert (
        api.files["__pty_input__"] == b"ls\n"
        and api.last.headers["content-type"] == "application/octet-stream"
    )
    pty.resize(50, 100)
    assert api.body() == {"rows": 50, "cols": 100}
    evs = list(pty.connect())
    assert evs[0].data == "$ " and evs[-1].kind == "exit" and evs[-1].reason == "eof"
    pty.kill()
    sb.pty.open(["/bin/zsh"], cwd="/home")
    assert api.body()["command"] == ["/bin/zsh"] and api.body()["cwd"] == "/home"


def test_grants(client, api):
    sb = client.sandboxes.get("sbx-abc123")
    g = sb.grant("bob", perm="write", reason="pair")
    assert api.last.method == "PUT" and api.body() == {"perm": "write", "reason": "pair"}
    assert g.permission == "write"
    assert sb.grants()[0].grantee_id == "bob"
    sb.revoke("bob")
    assert api.last.method == "DELETE"
    with pytest.raises(ValueError):
        sb.grant("bob", perm="admin")


def test_sandbox_context_manager_kills(client, api):
    with client.sandboxes.create() as sb:
        sb.exec("true")
    assert api.last.method == "DELETE"


def test_limit_errors_on_create(client, api):
    api.routes[("POST", "/sandboxes")] = httpx.Response(
        409, json={"detail": {"code": "sb_box_concurrency_limit", "plan": "free", "limit": 1, "current": 1}}
    )
    with pytest.raises(errors.LimitError) as ei:
        client.sandboxes.create()
    assert ei.value.limit == 1
    api.routes[("POST", "/sandboxes")] = httpx.Response(
        402, json={"detail": {"code": "sb_box_insufficient_credits", "balance": 0}}
    )
    with pytest.raises(errors.InsufficientCreditsError):
        client.sandboxes.create()
    api.routes[("POST", "/sandboxes")] = httpx.Response(
        403, json={"detail": {"code": "sb_box_plan_required", "plan": "plus"}}
    )
    with pytest.raises(errors.PlanRequiredError):
        client.sandboxes.create()


def test_wait_and_connect_have_no_read_deadline(client, api):
    sb = client.sandboxes.get("sbx-abc123")
    cmd = sb.commands.start(["sleep", "5"])
    cmd.wait()
    t = api.calls("POST", "/commands/bg1/wait")[-1].extensions["timeout"]
    assert t["read"] is None and t["connect"] == 10.0
    list(cmd.connect())
    assert api.calls("GET", "/commands/bg1/connect")[-1].extensions["timeout"]["read"] is None
    cmd.wait(timeout=7)
    assert api.calls("POST", "/commands/bg1/wait")[-1].extensions["timeout"]["read"] == 7
    pty = sb.pty.open()
    list(pty.connect())
    assert api.calls("GET", "/pty/pty1/connect")[-1].extensions["timeout"]["read"] is None


def test_freeze_wait_raises_on_failed_state(client, api, monkeypatch):
    monkeypatch.setattr("sbbox.sandboxes.time.sleep", lambda s: None)
    sb = client.sandboxes.get("sbx-abc123")
    api.routes[("POST", "/sandboxes/sbx-abc123/freeze")] = httpx.Response(202, json={"state": "freezing"})
    polls = iter(["running", "freezing", "failed"])
    api.routes[("GET", "/sandboxes/sbx-abc123")] = lambda _r: httpx.Response(
        200, json={"id": "sbx-abc123", "state": next(polls)}
    )
    with pytest.raises(errors.ConflictError, match="failed"):
        sb.freeze()
    assert len(api.calls("GET", "/sandboxes/sbx-abc123")) == 4  # initial get + running + freezing + failed


def test_file_read_retries_transient_503(client, api):
    sb = client.sandboxes.get("sbx-abc123")
    sb.files.write("/tmp/a", b"xyz")
    api.fail_first = 1
    assert sb.files.read("/tmp/a") == b"xyz"
    assert len(api.calls("GET", "/files/stream")) == 2
