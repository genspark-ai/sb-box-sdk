"""Sandbox resources and the ``Sandbox`` handle (sync + async)."""

from __future__ import annotations

import asyncio
import time
from collections.abc import AsyncIterator, Iterator, Mapping, Sequence
from typing import Any, Dict, List, Optional, Union

from ._client import NO_DEADLINE, _AsyncHTTP, _SyncHTTP, check_metadata, compact, normalize_command
from ._sse import SSEEvent
from .errors import ConflictError, NotFoundError, WaitTimeout
from .templates import _grant_body
from .types import (
    CodeResult,
    CommandResult,
    ExecEvent,
    FileEntry,
    Grant,
    MetricPoint,
    NetworkConfig,
    ProcessInfo,
    PtyEvent,
    SandboxInfo,
    SandboxSummary,
)

Command = Union[str, Sequence[str]]
_LIST_STATES = {"starting", "running", "freezing", "frozen", "exited", "failed", "unknown", "all"}


# ---------- body builders shared by sync / async ----------


def _create_body(
    template: Optional[str],
    template_id: Optional[str],
    timeout: Optional[int],
    on_timeout: Optional[str],
    metadata: Optional[Mapping[str, Any]],
    env_vars: Optional[Mapping[str, str]],
    network: Optional[Union[NetworkConfig, Mapping[str, Any]]],
) -> Dict[str, Any]:
    if template and template_id:
        raise ValueError("pass template= (a name) or template_id=, not both")
    if on_timeout not in (None, "freeze", "kill"):
        raise ValueError("on_timeout must be 'freeze' or 'kill'")
    body: Dict[str, Any] = {}
    if template_id:
        body["templateId"] = template_id
    else:
        body["templateName"] = template or "base"
    if timeout is not None:
        body["timeout"] = int(timeout)
    if on_timeout:
        body["lifecycle"] = {"onTimeout": on_timeout}
    md = check_metadata(metadata)
    if md:
        body["metadata"] = md
    if env_vars:
        body["envVars"] = dict(env_vars)
    if network is not None:
        net = network.to_dict() if isinstance(network, NetworkConfig) else dict(network)
        if net:
            body["network"] = net
    return body


def _state_param(state: Union[None, str, Sequence[str]]) -> Optional[str]:
    if state is None:
        return None
    states = [state] if isinstance(state, str) else list(state)
    flat = [s.strip() for s in ",".join(states).split(",") if s.strip()]
    bad = [s for s in flat if s not in _LIST_STATES]
    if bad:
        raise ValueError(f"unknown sandbox state(s) {bad}; allowed: {sorted(_LIST_STATES)}")
    return ",".join(flat) or None


def _exec_body(
    command: Command,
    cwd: Optional[str],
    env: Optional[Mapping[str, str]],
    user: Optional[str],
    timeout: Optional[int],
) -> Dict[str, Any]:
    return compact(
        {
            "command": normalize_command(command),
            "cwd": cwd,
            "envVars": dict(env) if env else None,
            "user": user,
            "timeout": int(timeout) if timeout is not None else None,
        }
    )


def _run_code_body(
    code: str,
    language: str,
    context_id: Optional[str],
    env: Optional[Mapping[str, str]],
    timeout: Optional[int],
    images: str,
) -> Dict[str, Any]:
    if images not in ("base64", "omit"):
        raise ValueError("images must be 'base64' or 'omit'")
    return compact(
        {
            "code": code,
            "language": language,
            "contextId": context_id,
            "envVars": dict(env) if env else None,
            "timeout": int(timeout) if timeout is not None else None,
            "images": images,
        }
    )


def _pty_body(
    command: Optional[Command],
    rows: int,
    cols: int,
    cwd: Optional[str],
    env: Optional[Mapping[str, str]],
) -> Dict[str, Any]:
    return compact(
        {
            "command": normalize_command(command) if command is not None else None,
            "rows": rows,
            "cols": cols,
            "cwd": cwd,
            "envVars": dict(env) if env else None,
        }
    )


def _as_bytes(data: Union[bytes, bytearray, memoryview, str]) -> bytes:
    if isinstance(data, str):
        return data.encode("utf-8")
    return bytes(data)


def _exec_event(ev: SSEEvent) -> ExecEvent:
    try:
        payload = ev.json()
    except ValueError:
        payload = {"data": ev.data}
    return ExecEvent.from_sse(ev.event, payload, ev.id)


def _pty_event(ev: SSEEvent) -> PtyEvent:
    try:
        payload = ev.json()
    except ValueError:
        payload = {"data": ev.data}
    p = payload if isinstance(payload, dict) else {}
    if ev.event == "exit":
        code = p.get("exitCode")
        return PtyEvent(
            kind="exit", exit_code=int(code) if code is not None else None, reason=p.get("reason")
        )
    return PtyEvent(kind=ev.event or "data", data=p.get("data", "") or "")


def _metrics_params(start: Optional[int], end: Optional[int]) -> Dict[str, Any]:
    return compact(
        {"start": int(start) if start is not None else None, "end": int(end) if end is not None else None}
    )


def _list_rows(data: Any) -> List[SandboxSummary]:
    rows = data.get("sandboxes", []) if isinstance(data, dict) else (data or [])
    return [SandboxSummary.from_dict(r) for r in rows]


# ---------- sync ----------


class BackgroundCommand:
    """A command started with ``sandbox.commands.start``."""

    def __init__(self, http: _SyncHTTP, sandbox_id: str, exec_id: str, pid: int) -> None:
        self._http = http
        self.sandbox_id = sandbox_id
        self.exec_id = exec_id
        self.pid = pid

    def _p(self, suffix: str) -> str:
        return f"/sandboxes/{self.sandbox_id}/commands/{self.exec_id}/{suffix}"

    def input(self, data: Union[str, bytes]) -> int:
        text = data.decode("utf-8") if isinstance(data, (bytes, bytearray)) else data
        res = self._http.json("POST", self._p("input"), body={"data": text}) or {}
        return int(res.get("written", len(text)))

    def close(self) -> None:
        self._http.json("POST", self._p("close"))

    def wait(self, *, timeout: Optional[float] = None) -> int:
        res = (
            self._http.json("POST", self._p("wait"), timeout=NO_DEADLINE if timeout is None else timeout)
            or {}
        )
        return int(res.get("exitCode", 0))

    def kill(self, signal: int = 9) -> None:
        self._http.json(
            "POST", f"/sandboxes/{self.sandbox_id}/commands/kill", body={"pid": self.pid, "signal": signal}
        )

    def connect(self, *, from_seq: Optional[int] = None) -> Iterator[ExecEvent]:
        """Attach to the command's output stream (SSE; user guide §8.3 / §15.4).

        Yields ``stdout`` / ``stderr`` events carrying ``seq``, a ``gap`` event when the server had
        to drop unread output, ``error`` on a server-side failure, and a final ``done`` event when
        the command has exited (the stream then ends). Detaching never kills the command. Pass
        ``from_seq`` (the last ``seq`` you processed) to re-attach without replay. A command the
        server has already reaped answers either nothing (404) or an ``error`` event followed by
        ``done``.
        """
        params = {"from_seq": from_seq} if from_seq else None
        try:
            for ev in self._http.sse("GET", self._p("connect"), params=params, timeout=NO_DEADLINE):
                yield _exec_event(ev)
        except NotFoundError:
            return


class Commands:
    def __init__(self, http: _SyncHTTP, sandbox_id: str) -> None:
        self._http = http
        self._id = sandbox_id

    def start(
        self,
        command: Command,
        *,
        cwd: Optional[str] = None,
        env: Optional[Mapping[str, str]] = None,
        user: Optional[str] = None,
    ) -> BackgroundCommand:
        body = compact(
            {
                "command": normalize_command(command),
                "cwd": cwd,
                "envVars": dict(env) if env else None,
                "user": user,
            }
        )
        res = self._http.json("POST", f"/sandboxes/{self._id}/commands/start", body=body, timeout=120) or {}
        return BackgroundCommand(self._http, self._id, res.get("execId", ""), int(res.get("pid") or 0))

    def list(self) -> List[ProcessInfo]:
        return [
            ProcessInfo.from_dict(p)
            for p in (self._http.json("GET", f"/sandboxes/{self._id}/commands", retry=True) or [])
        ]

    def kill(self, pid: int, signal: int = 9) -> None:
        self._http.json("POST", f"/sandboxes/{self._id}/commands/kill", body={"pid": pid, "signal": signal})

    def send_stdin(self, pid: int, data: str) -> None:
        self._http.json("POST", f"/sandboxes/{self._id}/commands/send-stdin", body={"pid": pid, "data": data})


class Files:
    def __init__(self, http: _SyncHTTP, sandbox_id: str) -> None:
        self._http = http
        self._id = sandbox_id

    def write(
        self, path: str, data: Union[bytes, bytearray, memoryview, str], *, user: Optional[str] = None
    ) -> int:
        res = self._http.json(
            "POST",
            f"/sandboxes/{self._id}/files/stream",
            params=compact({"path": path, "user": user}),
            body=_as_bytes(data),
            timeout=600,
        )
        return int((res or {}).get("written", 0))

    def read(self, path: str, *, user: Optional[str] = None) -> bytes:
        resp = self._http.request(
            "GET",
            f"/sandboxes/{self._id}/files/stream",
            params=compact({"path": path, "user": user}),
            timeout=600,
            retry=True,
        )
        return resp.content

    def read_text(self, path: str, *, encoding: str = "utf-8", user: Optional[str] = None) -> str:
        return self.read(path, user=user).decode(encoding)

    def list(self, path: str = "/", *, user: Optional[str] = None) -> List[FileEntry]:
        rows = self._http.json(
            "GET",
            f"/sandboxes/{self._id}/files/list",
            params=compact({"path": path, "user": user}),
            retry=True,
        )
        return [FileEntry.from_dict(r) for r in (rows or [])]


class Pty:
    def __init__(self, http: _SyncHTTP, sandbox_id: str, exec_id: str, pid: int) -> None:
        self._http = http
        self.sandbox_id = sandbox_id
        self.exec_id = exec_id
        self.pid = pid

    def _p(self, suffix: str) -> str:
        return f"/sandboxes/{self.sandbox_id}/pty/{self.exec_id}/{suffix}"

    def input(self, data: Union[str, bytes]) -> None:
        self._http.request("POST", self._p("input"), body=_as_bytes(data))

    def resize(self, rows: int, cols: int) -> None:
        self._http.json("POST", self._p("resize"), body={"rows": rows, "cols": cols})

    def kill(self) -> None:
        self._http.json("POST", self._p("kill"))

    def connect(self) -> Iterator[PtyEvent]:
        for ev in self._http.sse("GET", self._p("connect"), timeout=NO_DEADLINE):
            yield _pty_event(ev)


class Ptys:
    def __init__(self, http: _SyncHTTP, sandbox_id: str) -> None:
        self._http = http
        self._id = sandbox_id

    def open(
        self,
        command: Optional[Command] = None,
        *,
        rows: int = 24,
        cols: int = 80,
        cwd: Optional[str] = None,
        env: Optional[Mapping[str, str]] = None,
    ) -> Pty:
        res = (
            self._http.json(
                "POST",
                f"/sandboxes/{self._id}/pty",
                body=_pty_body(command, rows, cols, cwd, env),
                timeout=120,
            )
            or {}
        )
        return Pty(self._http, self._id, res.get("execId", ""), int(res.get("pid") or 0))


class Sandbox(SandboxInfo):
    """A sandbox record bound to a client. Use ``client.sandboxes.create()`` / ``.get()`` to obtain one."""

    _http: _SyncHTTP
    commands: Commands
    files: Files
    pty: Ptys

    @classmethod
    def _bind(cls, http: _SyncHTTP, d: Dict[str, Any]) -> Sandbox:
        info = SandboxInfo.from_dict(d)
        sb = cls(**info.__dict__)
        sb._http = http
        sb.commands = Commands(http, sb.id)
        sb.files = Files(http, sb.id)
        sb.pty = Ptys(http, sb.id)
        return sb

    def _update(self, d: Dict[str, Any]) -> Sandbox:
        self.__dict__.update({k: v for k, v in SandboxInfo.from_dict(d).__dict__.items() if k != "id" or v})
        return self

    # lifecycle
    def refresh(self) -> Sandbox:
        return self._update(self._http.json("GET", f"/sandboxes/{self.id}", retry=True))

    def kill(self) -> None:
        self._http.json("DELETE", f"/sandboxes/{self.id}", retry=True)
        self.state = "killed"

    def freeze(self, *, wait: bool = True, timeout: float = 90, interval: float = 2) -> Sandbox:
        res = self._http.json("POST", f"/sandboxes/{self.id}/freeze", retry=True, timeout=120) or {}
        if res.get("state") == "frozen" and "id" in res:
            return self._update(res)
        if res.get("state") == "frozen":
            return self.refresh()
        if not wait:
            self.state = res.get("state", "freezing")
            return self
        deadline = time.monotonic() + timeout
        while True:
            self.refresh()
            if self.state == "frozen":
                return self
            if self.state not in ("freezing", "running"):
                raise ConflictError(
                    f"sandbox {self.id} ended in state {self.state!r} while freezing", status=409
                )
            if time.monotonic() >= deadline:
                raise WaitTimeout(f"sandbox {self.id} still {self.state} after {timeout:g}s")
            time.sleep(interval)

    def resume(self, *, timeout: Optional[int] = None) -> Sandbox:
        body = compact({"timeout": int(timeout) if timeout is not None else None})
        return self._update(
            self._http.json("POST", f"/sandboxes/{self.id}/resume", body=body, retry=True, timeout=120)
        )

    def rollback(self, *, version: int = 0, timeout: Optional[int] = None) -> Sandbox:
        body = compact({"version": int(version), "timeout": int(timeout) if timeout is not None else None})
        return self._update(self._http.json("POST", f"/sandboxes/{self.id}/rollback", body=body, timeout=120))

    def clone(
        self, *, version: int = 0, metadata: Optional[Mapping[str, Any]] = None, timeout: float = 120
    ) -> Sandbox:
        body = compact({"version": int(version), "metadata": check_metadata(metadata)})
        res = self._http.json(
            "POST",
            f"/sandboxes/{self.id}/clone",
            body=body,
            timeout=120,
            retry_after_until=time.monotonic() + timeout,
        )
        return Sandbox._bind(self._http, res)

    def set_timeout(self, seconds: int) -> None:
        self._http.json("POST", f"/sandboxes/{self.id}/timeout", body={"timeout": int(seconds)}, retry=True)

    def reset(self, *, timeout: float = 120) -> Sandbox:
        return self._update(
            self._http.json(
                "POST",
                f"/sandboxes/{self.id}/reset",
                timeout=120,
                retry_after_until=time.monotonic() + timeout,
            )
        )

    def metrics(self, *, start: Optional[int] = None, end: Optional[int] = None) -> List[MetricPoint]:
        rows = self._http.json(
            "GET", f"/sandboxes/{self.id}/metrics", params=_metrics_params(start, end), retry=True
        )
        return [MetricPoint.from_dict(r) for r in (rows or [])]

    # commands
    def exec(
        self,
        command: Command,
        *,
        cwd: Optional[str] = None,
        env: Optional[Mapping[str, str]] = None,
        user: Optional[str] = None,
        timeout: int = 60,
    ) -> CommandResult:
        body = _exec_body(command, cwd, env, user, timeout)
        return CommandResult.from_dict(
            self._http.json("POST", f"/sandboxes/{self.id}/exec", body=body, timeout=timeout + 30)
        )

    def exec_stream(
        self,
        command: Command,
        *,
        cwd: Optional[str] = None,
        env: Optional[Mapping[str, str]] = None,
        user: Optional[str] = None,
        timeout: int = 60,
    ) -> Iterator[ExecEvent]:
        body = _exec_body(command, cwd, env, user, timeout)
        for ev in self._http.sse("POST", f"/sandboxes/{self.id}/sse/exec", body=body, timeout=timeout + 30):
            yield _exec_event(ev)

    def run_code(
        self,
        code: str,
        *,
        language: str = "python",
        context_id: Optional[str] = None,
        env: Optional[Mapping[str, str]] = None,
        timeout: int = 60,
        images: str = "base64",
    ) -> CodeResult:
        body = _run_code_body(code, language, context_id, env, timeout, images)
        return CodeResult.from_dict(
            self._http.json("POST", f"/sandboxes/{self.id}/run-code", body=body, timeout=timeout + 30)
        )

    # sharing
    def grant(
        self,
        user_id: str,
        *,
        perm: str = "read",
        expires_at: Optional[str] = None,
        reason: Optional[str] = None,
    ) -> Grant:
        return Grant.from_dict(
            self._http.json(
                "PUT", f"/sandboxes/{self.id}/grants/{user_id}", body=_grant_body(perm, expires_at, reason)
            )
        )

    def grants(self) -> List[Grant]:
        return [
            Grant.from_dict(g)
            for g in (self._http.json("GET", f"/sandboxes/{self.id}/grants", retry=True) or [])
        ]

    def revoke(self, user_id: str) -> None:
        self._http.json("DELETE", f"/sandboxes/{self.id}/grants/{user_id}")

    def __enter__(self) -> Sandbox:
        return self

    def __exit__(self, *exc: object) -> None:
        self.kill()


class Sandboxes:
    def __init__(self, http: _SyncHTTP) -> None:
        self._http = http

    def create(
        self,
        template: Optional[str] = None,
        *,
        template_id: Optional[str] = None,
        timeout: Optional[int] = None,
        on_timeout: Optional[str] = None,
        metadata: Optional[Mapping[str, Any]] = None,
        env_vars: Optional[Mapping[str, str]] = None,
        network: Optional[Union[NetworkConfig, Mapping[str, Any]]] = None,
    ) -> Sandbox:
        body = _create_body(template, template_id, timeout, on_timeout, metadata, env_vars, network)
        return Sandbox._bind(self._http, self._http.json("POST", "/sandboxes", body=body, timeout=120))

    def list(self, state: Union[None, str, Sequence[str]] = None) -> List[SandboxSummary]:
        return _list_rows(
            self._http.json("GET", "/sandboxes", params={"state": _state_param(state)}, retry=True)
        )

    def shared(self) -> List[Sandbox]:
        return [
            Sandbox._bind(self._http, r)
            for r in (self._http.json("GET", "/sandboxes/shared", retry=True) or [])
        ]

    def get(self, sandbox_id: str) -> Sandbox:
        return Sandbox._bind(self._http, self._http.json("GET", f"/sandboxes/{sandbox_id}", retry=True))

    connect = get

    def kill(self, sandbox_id: str) -> None:
        self._http.json("DELETE", f"/sandboxes/{sandbox_id}", retry=True)

    def freeze(self, sandbox_id: str, *, wait: bool = True, timeout: float = 90) -> Sandbox:
        return self._stub(sandbox_id).freeze(wait=wait, timeout=timeout)

    def resume(self, sandbox_id: str, *, timeout: Optional[int] = None) -> Sandbox:
        return self._stub(sandbox_id).resume(timeout=timeout)

    def rollback(self, sandbox_id: str, *, version: int = 0, timeout: Optional[int] = None) -> Sandbox:
        return self._stub(sandbox_id).rollback(version=version, timeout=timeout)

    def clone(
        self, sandbox_id: str, *, version: int = 0, metadata: Optional[Mapping[str, Any]] = None
    ) -> Sandbox:
        return self._stub(sandbox_id).clone(version=version, metadata=metadata)

    def set_timeout(self, sandbox_id: str, seconds: int) -> None:
        self._stub(sandbox_id).set_timeout(seconds)

    def reset(self, sandbox_id: str) -> Sandbox:
        return self._stub(sandbox_id).reset()

    def metrics(
        self, sandbox_id: str, *, start: Optional[int] = None, end: Optional[int] = None
    ) -> List[MetricPoint]:
        return self._stub(sandbox_id).metrics(start=start, end=end)

    def grant(
        self,
        sandbox_id: str,
        user_id: str,
        *,
        perm: str = "read",
        expires_at: Optional[str] = None,
        reason: Optional[str] = None,
    ) -> Grant:
        return self._stub(sandbox_id).grant(user_id, perm=perm, expires_at=expires_at, reason=reason)

    def grants(self, sandbox_id: str) -> List[Grant]:
        return self._stub(sandbox_id).grants()

    def revoke(self, sandbox_id: str, user_id: str) -> None:
        self._stub(sandbox_id).revoke(user_id)

    def _stub(self, sandbox_id: str) -> Sandbox:
        return Sandbox._bind(self._http, {"id": sandbox_id})


# ---------- async ----------


class AsyncBackgroundCommand:
    def __init__(self, http: _AsyncHTTP, sandbox_id: str, exec_id: str, pid: int) -> None:
        self._http = http
        self.sandbox_id = sandbox_id
        self.exec_id = exec_id
        self.pid = pid

    def _p(self, suffix: str) -> str:
        return f"/sandboxes/{self.sandbox_id}/commands/{self.exec_id}/{suffix}"

    async def input(self, data: Union[str, bytes]) -> int:
        text = data.decode("utf-8") if isinstance(data, (bytes, bytearray)) else data
        res = await self._http.json("POST", self._p("input"), body={"data": text}) or {}
        return int(res.get("written", len(text)))

    async def close(self) -> None:
        await self._http.json("POST", self._p("close"))

    async def wait(self, *, timeout: Optional[float] = None) -> int:
        res = (
            await self._http.json(
                "POST", self._p("wait"), timeout=NO_DEADLINE if timeout is None else timeout
            )
            or {}
        )
        return int(res.get("exitCode", 0))

    async def kill(self, signal: int = 9) -> None:
        await self._http.json(
            "POST", f"/sandboxes/{self.sandbox_id}/commands/kill", body={"pid": self.pid, "signal": signal}
        )

    async def connect(self, *, from_seq: Optional[int] = None) -> AsyncIterator[ExecEvent]:
        """Attach to the command's output stream (SSE; user guide §8.3 / §15.4).

        Same events as the sync variant: ``stdout`` / ``stderr`` (with ``seq``), ``gap``,
        ``error``, then a final ``done``. ``from_seq`` re-attaches without replay; a reaped
        command yields nothing (404) or ``error`` then ``done``.
        """
        params = {"from_seq": from_seq} if from_seq else None
        try:
            async for ev in self._http.sse("GET", self._p("connect"), params=params, timeout=NO_DEADLINE):
                yield _exec_event(ev)
        except NotFoundError:
            return


class AsyncCommands:
    def __init__(self, http: _AsyncHTTP, sandbox_id: str) -> None:
        self._http = http
        self._id = sandbox_id

    async def start(
        self,
        command: Command,
        *,
        cwd: Optional[str] = None,
        env: Optional[Mapping[str, str]] = None,
        user: Optional[str] = None,
    ) -> AsyncBackgroundCommand:
        body = compact(
            {
                "command": normalize_command(command),
                "cwd": cwd,
                "envVars": dict(env) if env else None,
                "user": user,
            }
        )
        res = (
            await self._http.json("POST", f"/sandboxes/{self._id}/commands/start", body=body, timeout=120)
            or {}
        )
        return AsyncBackgroundCommand(self._http, self._id, res.get("execId", ""), int(res.get("pid") or 0))

    async def list(self) -> List[ProcessInfo]:
        rows = await self._http.json("GET", f"/sandboxes/{self._id}/commands", retry=True)
        return [ProcessInfo.from_dict(p) for p in (rows or [])]

    async def kill(self, pid: int, signal: int = 9) -> None:
        await self._http.json(
            "POST", f"/sandboxes/{self._id}/commands/kill", body={"pid": pid, "signal": signal}
        )

    async def send_stdin(self, pid: int, data: str) -> None:
        await self._http.json(
            "POST", f"/sandboxes/{self._id}/commands/send-stdin", body={"pid": pid, "data": data}
        )


class AsyncFiles:
    def __init__(self, http: _AsyncHTTP, sandbox_id: str) -> None:
        self._http = http
        self._id = sandbox_id

    async def write(
        self, path: str, data: Union[bytes, bytearray, memoryview, str], *, user: Optional[str] = None
    ) -> int:
        res = await self._http.json(
            "POST",
            f"/sandboxes/{self._id}/files/stream",
            params=compact({"path": path, "user": user}),
            body=_as_bytes(data),
            timeout=600,
        )
        return int((res or {}).get("written", 0))

    async def read(self, path: str, *, user: Optional[str] = None) -> bytes:
        resp = await self._http.request(
            "GET",
            f"/sandboxes/{self._id}/files/stream",
            params=compact({"path": path, "user": user}),
            timeout=600,
            retry=True,
        )
        return resp.content

    async def read_text(self, path: str, *, encoding: str = "utf-8", user: Optional[str] = None) -> str:
        return (await self.read(path, user=user)).decode(encoding)

    async def list(self, path: str = "/", *, user: Optional[str] = None) -> List[FileEntry]:
        rows = await self._http.json(
            "GET",
            f"/sandboxes/{self._id}/files/list",
            params=compact({"path": path, "user": user}),
            retry=True,
        )
        return [FileEntry.from_dict(r) for r in (rows or [])]


class AsyncPty:
    def __init__(self, http: _AsyncHTTP, sandbox_id: str, exec_id: str, pid: int) -> None:
        self._http = http
        self.sandbox_id = sandbox_id
        self.exec_id = exec_id
        self.pid = pid

    def _p(self, suffix: str) -> str:
        return f"/sandboxes/{self.sandbox_id}/pty/{self.exec_id}/{suffix}"

    async def input(self, data: Union[str, bytes]) -> None:
        await self._http.request("POST", self._p("input"), body=_as_bytes(data))

    async def resize(self, rows: int, cols: int) -> None:
        await self._http.json("POST", self._p("resize"), body={"rows": rows, "cols": cols})

    async def kill(self) -> None:
        await self._http.json("POST", self._p("kill"))

    async def connect(self) -> AsyncIterator[PtyEvent]:
        async for ev in self._http.sse("GET", self._p("connect"), timeout=NO_DEADLINE):
            yield _pty_event(ev)


class AsyncPtys:
    def __init__(self, http: _AsyncHTTP, sandbox_id: str) -> None:
        self._http = http
        self._id = sandbox_id

    async def open(
        self,
        command: Optional[Command] = None,
        *,
        rows: int = 24,
        cols: int = 80,
        cwd: Optional[str] = None,
        env: Optional[Mapping[str, str]] = None,
    ) -> AsyncPty:
        res = (
            await self._http.json(
                "POST",
                f"/sandboxes/{self._id}/pty",
                body=_pty_body(command, rows, cols, cwd, env),
                timeout=120,
            )
            or {}
        )
        return AsyncPty(self._http, self._id, res.get("execId", ""), int(res.get("pid") or 0))


class AsyncSandbox(SandboxInfo):
    _http: _AsyncHTTP
    commands: AsyncCommands
    files: AsyncFiles
    pty: AsyncPtys

    @classmethod
    def _bind(cls, http: _AsyncHTTP, d: Dict[str, Any]) -> AsyncSandbox:
        info = SandboxInfo.from_dict(d)
        sb = cls(**info.__dict__)
        sb._http = http
        sb.commands = AsyncCommands(http, sb.id)
        sb.files = AsyncFiles(http, sb.id)
        sb.pty = AsyncPtys(http, sb.id)
        return sb

    def _update(self, d: Dict[str, Any]) -> AsyncSandbox:
        self.__dict__.update({k: v for k, v in SandboxInfo.from_dict(d).__dict__.items() if k != "id" or v})
        return self

    async def refresh(self) -> AsyncSandbox:
        return self._update(await self._http.json("GET", f"/sandboxes/{self.id}", retry=True))

    async def kill(self) -> None:
        await self._http.json("DELETE", f"/sandboxes/{self.id}", retry=True)
        self.state = "killed"

    async def freeze(self, *, wait: bool = True, timeout: float = 90, interval: float = 2) -> AsyncSandbox:
        res = await self._http.json("POST", f"/sandboxes/{self.id}/freeze", retry=True, timeout=120) or {}
        if res.get("state") == "frozen" and "id" in res:
            return self._update(res)
        if res.get("state") == "frozen":
            return await self.refresh()
        if not wait:
            self.state = res.get("state", "freezing")
            return self
        deadline = time.monotonic() + timeout
        while True:
            await self.refresh()
            if self.state == "frozen":
                return self
            if self.state not in ("freezing", "running"):
                raise ConflictError(
                    f"sandbox {self.id} ended in state {self.state!r} while freezing", status=409
                )
            if time.monotonic() >= deadline:
                raise WaitTimeout(f"sandbox {self.id} still {self.state} after {timeout:g}s")
            await asyncio.sleep(interval)

    async def resume(self, *, timeout: Optional[int] = None) -> AsyncSandbox:
        body = compact({"timeout": int(timeout) if timeout is not None else None})
        return self._update(
            await self._http.json("POST", f"/sandboxes/{self.id}/resume", body=body, retry=True, timeout=120)
        )

    async def rollback(self, *, version: int = 0, timeout: Optional[int] = None) -> AsyncSandbox:
        body = compact({"version": int(version), "timeout": int(timeout) if timeout is not None else None})
        return self._update(
            await self._http.json("POST", f"/sandboxes/{self.id}/rollback", body=body, timeout=120)
        )

    async def clone(
        self, *, version: int = 0, metadata: Optional[Mapping[str, Any]] = None, timeout: float = 120
    ) -> AsyncSandbox:
        body = compact({"version": int(version), "metadata": check_metadata(metadata)})
        res = await self._http.json(
            "POST",
            f"/sandboxes/{self.id}/clone",
            body=body,
            timeout=120,
            retry_after_until=time.monotonic() + timeout,
        )
        return AsyncSandbox._bind(self._http, res)

    async def set_timeout(self, seconds: int) -> None:
        await self._http.json(
            "POST", f"/sandboxes/{self.id}/timeout", body={"timeout": int(seconds)}, retry=True
        )

    async def reset(self, *, timeout: float = 120) -> AsyncSandbox:
        return self._update(
            await self._http.json(
                "POST",
                f"/sandboxes/{self.id}/reset",
                timeout=120,
                retry_after_until=time.monotonic() + timeout,
            )
        )

    async def metrics(self, *, start: Optional[int] = None, end: Optional[int] = None) -> List[MetricPoint]:
        rows = await self._http.json(
            "GET", f"/sandboxes/{self.id}/metrics", params=_metrics_params(start, end), retry=True
        )
        return [MetricPoint.from_dict(r) for r in (rows or [])]

    async def exec(
        self,
        command: Command,
        *,
        cwd: Optional[str] = None,
        env: Optional[Mapping[str, str]] = None,
        user: Optional[str] = None,
        timeout: int = 60,
    ) -> CommandResult:
        body = _exec_body(command, cwd, env, user, timeout)
        return CommandResult.from_dict(
            await self._http.json("POST", f"/sandboxes/{self.id}/exec", body=body, timeout=timeout + 30)
        )

    async def exec_stream(
        self,
        command: Command,
        *,
        cwd: Optional[str] = None,
        env: Optional[Mapping[str, str]] = None,
        user: Optional[str] = None,
        timeout: int = 60,
    ) -> AsyncIterator[ExecEvent]:
        body = _exec_body(command, cwd, env, user, timeout)
        async for ev in self._http.sse(
            "POST", f"/sandboxes/{self.id}/sse/exec", body=body, timeout=timeout + 30
        ):
            yield _exec_event(ev)

    async def run_code(
        self,
        code: str,
        *,
        language: str = "python",
        context_id: Optional[str] = None,
        env: Optional[Mapping[str, str]] = None,
        timeout: int = 60,
        images: str = "base64",
    ) -> CodeResult:
        body = _run_code_body(code, language, context_id, env, timeout, images)
        return CodeResult.from_dict(
            await self._http.json("POST", f"/sandboxes/{self.id}/run-code", body=body, timeout=timeout + 30)
        )

    async def grant(
        self,
        user_id: str,
        *,
        perm: str = "read",
        expires_at: Optional[str] = None,
        reason: Optional[str] = None,
    ) -> Grant:
        return Grant.from_dict(
            await self._http.json(
                "PUT", f"/sandboxes/{self.id}/grants/{user_id}", body=_grant_body(perm, expires_at, reason)
            )
        )

    async def grants(self) -> List[Grant]:
        rows = await self._http.json("GET", f"/sandboxes/{self.id}/grants", retry=True)
        return [Grant.from_dict(g) for g in (rows or [])]

    async def revoke(self, user_id: str) -> None:
        await self._http.json("DELETE", f"/sandboxes/{self.id}/grants/{user_id}")

    async def __aenter__(self) -> AsyncSandbox:
        return self

    async def __aexit__(self, *exc: object) -> None:
        await self.kill()


class AsyncSandboxes:
    def __init__(self, http: _AsyncHTTP) -> None:
        self._http = http

    async def create(
        self,
        template: Optional[str] = None,
        *,
        template_id: Optional[str] = None,
        timeout: Optional[int] = None,
        on_timeout: Optional[str] = None,
        metadata: Optional[Mapping[str, Any]] = None,
        env_vars: Optional[Mapping[str, str]] = None,
        network: Optional[Union[NetworkConfig, Mapping[str, Any]]] = None,
    ) -> AsyncSandbox:
        body = _create_body(template, template_id, timeout, on_timeout, metadata, env_vars, network)
        return AsyncSandbox._bind(
            self._http, await self._http.json("POST", "/sandboxes", body=body, timeout=120)
        )

    async def list(self, state: Union[None, str, Sequence[str]] = None) -> List[SandboxSummary]:
        return _list_rows(
            await self._http.json("GET", "/sandboxes", params={"state": _state_param(state)}, retry=True)
        )

    async def shared(self) -> List[AsyncSandbox]:
        rows = await self._http.json("GET", "/sandboxes/shared", retry=True)
        return [AsyncSandbox._bind(self._http, r) for r in (rows or [])]

    async def get(self, sandbox_id: str) -> AsyncSandbox:
        return AsyncSandbox._bind(
            self._http, await self._http.json("GET", f"/sandboxes/{sandbox_id}", retry=True)
        )

    connect = get

    async def kill(self, sandbox_id: str) -> None:
        await self._http.json("DELETE", f"/sandboxes/{sandbox_id}", retry=True)

    async def freeze(self, sandbox_id: str, *, wait: bool = True, timeout: float = 90) -> AsyncSandbox:
        return await self._stub(sandbox_id).freeze(wait=wait, timeout=timeout)

    async def resume(self, sandbox_id: str, *, timeout: Optional[int] = None) -> AsyncSandbox:
        return await self._stub(sandbox_id).resume(timeout=timeout)

    async def rollback(
        self, sandbox_id: str, *, version: int = 0, timeout: Optional[int] = None
    ) -> AsyncSandbox:
        return await self._stub(sandbox_id).rollback(version=version, timeout=timeout)

    async def clone(
        self, sandbox_id: str, *, version: int = 0, metadata: Optional[Mapping[str, Any]] = None
    ) -> AsyncSandbox:
        return await self._stub(sandbox_id).clone(version=version, metadata=metadata)

    async def set_timeout(self, sandbox_id: str, seconds: int) -> None:
        await self._stub(sandbox_id).set_timeout(seconds)

    async def reset(self, sandbox_id: str) -> AsyncSandbox:
        return await self._stub(sandbox_id).reset()

    async def metrics(
        self, sandbox_id: str, *, start: Optional[int] = None, end: Optional[int] = None
    ) -> List[MetricPoint]:
        return await self._stub(sandbox_id).metrics(start=start, end=end)

    async def grant(
        self,
        sandbox_id: str,
        user_id: str,
        *,
        perm: str = "read",
        expires_at: Optional[str] = None,
        reason: Optional[str] = None,
    ) -> Grant:
        return await self._stub(sandbox_id).grant(user_id, perm=perm, expires_at=expires_at, reason=reason)

    async def grants(self, sandbox_id: str) -> List[Grant]:
        return await self._stub(sandbox_id).grants()

    async def revoke(self, sandbox_id: str, user_id: str) -> None:
        await self._stub(sandbox_id).revoke(user_id)

    def _stub(self, sandbox_id: str) -> AsyncSandbox:
        return AsyncSandbox._bind(self._http, {"id": sandbox_id})
