"""In-process mock of the SB-Box API implementing the contract in ../../API.md."""

from __future__ import annotations

import json
from typing import Any, Dict, List, Optional
from urllib.parse import parse_qs

import httpx
import pytest

import sbbox

SBX = {
    "id": "sbx-abc123",
    "templateId": "tpl-base",
    "state": "running",
    "region": "westus2",
    "portUrl": "https://{port}-sbx-abc123.sandbox.example.test",
    "domain": "sandbox.example.test",
    "createdAt": "2026-11-04T00:00:00Z",
    "cpuCount": 1,
    "memoryMB": 2048,
    "diskSizeGB": 20,
    "idleTimeout": 600,
    "versions": [{"version": 1, "frozenAt": "2026-11-04T01:00:00Z", "label": "v1"}],
    "metadata": {"cogenId": "u1", "k": "v"},
}
TPL = {
    "id": "tpl-base",
    "name": "base",
    "namespace": "public",
    "status": "ready",
    "region": "westus2",
    "is_owner": False,
    "can_create": True,
    "cpuCount": 1,
    "memoryMB": 2048,
}


def _json(status: int, body: Any, headers: Optional[Dict[str, str]] = None) -> httpx.Response:
    return httpx.Response(status, json=body, headers=headers)


def _sse(events: List[tuple]) -> httpx.Response:
    def gen():
        seq = 0
        for name, payload in events:
            # Like the server: only output chunks carry an ``id:`` (their sequence number).
            idline = ""
            if name in ("stdout", "stderr", "data"):
                seq += 1
                idline = f"id: {seq}\n"
            yield f"{idline}event: {name}\ndata: {json.dumps(payload)}\n\n".encode()

    return httpx.Response(
        200, headers={"content-type": "text/event-stream"}, stream=httpx.ByteStream(b"".join(gen()))
    )


class MockAPI:
    """Records every request; answers by (method, path). Tests may override responses via ``self.routes``."""

    def __init__(self) -> None:
        self.requests: List[httpx.Request] = []
        self.routes: Dict[tuple, Any] = {}
        self.files: Dict[str, bytes] = {}
        self.freeze_polls = 0
        self.build_polls = 0
        self.clone_calls = 0
        self.fail_first = 0  # transient 503s before success on GET
        self.connect_from_seq: Optional[str] = None

    # helpers for assertions
    @property
    def last(self) -> httpx.Request:
        return self.requests[-1]

    def body(self, i: int = -1) -> Any:
        return json.loads(self.requests[i].content or b"null")

    def calls(self, method: str, path: str) -> List[httpx.Request]:
        return [r for r in self.requests if r.method == method and r.url.path.endswith(path)]

    def handler(self, req: httpx.Request) -> httpx.Response:
        self.requests.append(req)
        if req.headers.get("x-api-key") != "gsk-test-key":
            return _json(401, {"detail": "invalid api key"})
        p = req.url.path.replace("/api/sb-box/v1", "")
        m = req.method
        q = {k: v[0] for k, v in parse_qs(req.url.query.decode()).items()}
        r = self.routes.get((m, p))
        if r is not None:
            return r(req) if callable(r) else r
        if m == "GET" and self.fail_first > 0:
            self.fail_first -= 1
            return _json(503, {"detail": "try again"})
        if p == "/regions":
            return _json(200, {"regions": ["westus2"], "default": "westus2"})
        if p == "/limits":
            return _json(
                200,
                {"plan": "plus", "limits": {"maxConcurrentSandboxes": 20}, "usage": {"runningSandboxes": 1}},
            )
        if p == "/usage":
            return _json(200, {"days": int(q.get("days", 30)), "direct": {"credits": 12.5}, "daily": []})
        if p == "/templates/build":
            return _json(
                200, {"templateId": "tpl-new", "status": "building", "region": "westus2", "owner": "u1"}
            )
        if p == "/templates" and m == "GET":
            return _json(200, {"templates": [TPL]})
        if p == "/templates/shared":
            return _json(200, [TPL])
        if p == "/templates/tpl-new":
            self.build_polls += 1
            st = "building" if self.build_polls < 3 else "ready"
            return _json(200, dict(TPL, id="tpl-new", status=st))
        if p == "/templates/tpl-bad":
            return _json(200, dict(TPL, id="tpl-bad", status="error", buildError="boom"))
        if p == "/templates/tpl-base" and m == "GET":
            return _json(200, TPL)
        if p == "/templates/tpl-base" and m == "DELETE":
            return _json(200, {"status": "deleted"})
        if p.startswith("/templates/tpl-base/grants"):
            return self._grants(m, p, "template_id")
        if p == "/sandboxes" and m == "POST":
            return _json(200, SBX)
        if p == "/sandboxes" and m == "GET":
            return _json(
                200,
                {
                    "sandboxes": [{"id": "sbx-abc123", "template_id": "tpl-base", "state": "running"}],
                    "hasMore": False,
                },
            )
        if p == "/sandboxes/shared":
            return _json(200, [SBX])
        if p == "/sandboxes/sbx-abc123" and m == "GET":
            if self.freeze_polls:
                self.freeze_polls -= 1
                return _json(200, dict(SBX, state="freezing"))
            return _json(200, SBX)
        if p == "/sandboxes/sbx-gone":
            return _json(404, {"detail": "sandbox not found"})
        if p == "/sandboxes/sbx-abc123" and m == "DELETE":
            return _json(200, {"status": "killed"})
        if p == "/sandboxes/sbx-abc123/freeze":
            return _json(200, dict(SBX, state="frozen", frozenAt="2026-11-04T02:00:00Z"))
        if p == "/sandboxes/sbx-202/freeze":
            self.freeze_polls = 2
            self.routes[("GET", "/sandboxes/sbx-202")] = lambda _r: self._poll_frozen()
            return _json(202, {"state": "freezing", "id": "sbx-202"})
        if p == "/sandboxes/sbx-abc123/resume":
            return _json(200, SBX)
        if p == "/sandboxes/sbx-abc123/rollback":
            return _json(200, SBX)
        if p == "/sandboxes/sbx-abc123/clone":
            self.clone_calls += 1
            if self.clone_calls < 3:
                return _json(503, {"detail": "clone still starting"}, {"Retry-After": "0"})
            return _json(200, dict(SBX, id="sbx-clone"))
        if p == "/sandboxes/sbx-abc123/timeout":
            return _json(200, {"status": "ok"})
        if p == "/sandboxes/sbx-abc123/reset":
            return _json(200, SBX)
        if p == "/sandboxes/sbx-abc123/metrics":
            return _json(
                200,
                [
                    {
                        "timestamp": "t",
                        "timestampUnix": 1,
                        "cpuCount": 1,
                        "cpuUsedPct": 2.5,
                        "memUsed": 3,
                        "memTotal": 4,
                    }
                ],
            )
        if p.startswith("/sandboxes/sbx-abc123/grants"):
            return self._grants(m, p, "sandbox_id")
        if p == "/sandboxes/sbx-abc123/exec":
            b = json.loads(req.content)
            if b["command"][-1] == "sleep":
                return _json(408, {"error": "command timed out", "stage": "timeout", "killed": True})
            return _json(
                200,
                {
                    "stdout": " ".join(b["command"]),
                    "stderr": "",
                    "exitCode": 0 if "false" not in b["command"] else 1,
                },
            )
        if p == "/sandboxes/sbx-abc123/sse/exec":
            return _sse(
                [
                    ("execId", {"execId": "e1"}),
                    ("stdout", {"data": "hi\n"}),
                    ("stderr", {"data": "warn"}),
                    ("exit", {"exitCode": 3}),
                ]
            )
        if p == "/sandboxes/sbx-abc123/run-code":
            b = json.loads(req.content)
            if b["code"] == "1/0":
                return _json(
                    200,
                    {
                        "stdout": "",
                        "stderr": "",
                        "complete": True,
                        "error": {
                            "name": "ZeroDivisionError",
                            "value": "division by zero",
                            "traceback": ["tb"],
                        },
                        "results": [],
                    },
                )
            if b["code"] == "nointerp":
                return _json(
                    409, {"detail": {"code": "sb_box_interpreter_unavailable", "message": "no interpreter"}}
                )
            return _json(
                200,
                {
                    "stdout": "x\n",
                    "stderr": "",
                    "executionCount": 1,
                    "complete": True,
                    "error": None,
                    "results": [{"text": "2", "isMainResult": True}],
                },
            )
        if p == "/sandboxes/sbx-abc123/commands/start":
            return _json(200, {"execId": "bg1", "pid": 42})
        if p == "/sandboxes/sbx-abc123/commands" and m == "GET":
            return _json(200, [{"execId": "bg1", "pid": 42, "sandboxId": "sbx-abc123"}])
        if p == "/sandboxes/sbx-abc123/commands/bg1/input":
            return _json(200, {"written": len(json.loads(req.content)["data"])})
        if p == "/sandboxes/sbx-abc123/commands/bg1/close":
            return _json(200, {"status": "ok"})
        if p == "/sandboxes/sbx-abc123/commands/bg1/wait":
            return _json(200, {"exitCode": 7})
        if p == "/sandboxes/sbx-abc123/commands/bg1/connect":
            self.connect_from_seq = q.get("from_seq")
            return _sse(
                [
                    ("stdout", {"data": "bg"}),
                    ("stderr", {"data": "warn"}),
                    ("gap", {"error": "output gap — ring evicted unread bytes"}),
                    ("done", {"status": "disconnected"}),
                ]
            )
        if p == "/sandboxes/sbx-abc123/commands/gone/connect":
            return _json(404, {"detail": "command not found"})
        if p in ("/sandboxes/sbx-abc123/commands/kill", "/sandboxes/sbx-abc123/commands/send-stdin"):
            return _json(200, {"status": "ok"})
        if p == "/sandboxes/sbx-abc123/files/stream" and m == "POST":
            self.files[q["path"]] = req.content
            return _json(200, {"written": len(req.content)})
        if p == "/sandboxes/sbx-abc123/files/stream" and m == "GET":
            if q["path"] not in self.files:
                return _json(404, {"detail": "file not found"})
            return httpx.Response(
                200, content=self.files[q["path"]], headers={"content-type": "application/octet-stream"}
            )
        if p == "/sandboxes/sbx-abc123/files/list":
            return _json(200, [{"name": "a.txt", "path": "/tmp/a.txt", "isDir": False, "size": 5}])
        if p == "/sandboxes/sbx-abc123/pty" and m == "POST":
            return _json(200, {"execId": "pty1", "pid": 99})
        if p == "/sandboxes/sbx-abc123/pty/pty1/input":
            self.files["__pty_input__"] = req.content
            return _json(200, {"status": "ok"})
        if p in ("/sandboxes/sbx-abc123/pty/pty1/resize", "/sandboxes/sbx-abc123/pty/pty1/kill"):
            return _json(200, {"status": "ok"})
        if p == "/sandboxes/sbx-abc123/pty/pty1/connect":
            return _sse([("data", {"data": "$ "}), ("exit", {"reason": "eof", "exitCode": 0})])
        return _json(404, {"detail": f"no route {m} {p}"})

    def _poll_frozen(self) -> httpx.Response:
        if self.freeze_polls > 0:
            self.freeze_polls -= 1
            return _json(200, dict(SBX, id="sbx-202", state="freezing"))
        return _json(200, dict(SBX, id="sbx-202", state="frozen"))

    def _grants(self, m: str, p: str, key: str) -> httpx.Response:
        if m == "PUT":
            return _json(
                200,
                {
                    key: "x",
                    "grantee_id": p.rsplit("/", 1)[-1],
                    "permission": "write",
                    "granted_at": "t",
                    "expires_at": None,
                },
            )
        if m == "GET":
            return _json(200, [{"grantee_id": "bob", "permission": "read", "granted_at": "t"}])
        return _json(200, {"status": "revoked"})


@pytest.fixture
def api() -> MockAPI:
    return MockAPI()


@pytest.fixture
def client(api: MockAPI) -> sbbox.Client:
    c = sbbox.Client(
        "gsk-test-key", base_url="https://api.test/api/sb-box/v1", transport=httpx.MockTransport(api.handler)
    )
    c._core.backoff = lambda attempt: 0  # type: ignore[assignment]
    return c


@pytest.fixture
def aclient(api: MockAPI) -> sbbox.AsyncClient:
    async def h(req: httpx.Request) -> httpx.Response:
        return api.handler(req)

    c = sbbox.AsyncClient(
        "gsk-test-key", base_url="https://api.test/api/sb-box/v1", transport=httpx.MockTransport(h)
    )
    c._core.backoff = lambda attempt: 0  # type: ignore[assignment]
    return c
