"""HTTP core shared by the sync and async clients: auth, retries, error mapping, SSE."""

from __future__ import annotations

import asyncio
import json
import os
import random
import time
from typing import Any, AsyncIterator, Dict, Iterator, List, Mapping, Optional, Sequence, Union

import httpx

from ._sse import SSEEvent, aiter_sse, iter_sse
from ._version import __version__
from .errors import TransportError, error_for_response, redact

DEFAULT_BASE_URL = "https://www.genspark.ai/api/sb-box/v1"
USER_AGENT = f"sbbox-python-sdk/{__version__} (+https://www.genspark.ai/sb-box/docs)"

_RETRY_STATUSES = (502, 503, 504)
_BACKOFF = (0.5, 1.0, 2.0)
_CONNECT_TIMEOUT = 10.0


class _NoDeadline:
    """Marker for calls that block for as long as the server does (wait / connect streams)."""

    def __repr__(self) -> str:
        return "NO_DEADLINE"


NO_DEADLINE = _NoDeadline()
RequestTimeout = Union[None, float, _NoDeadline]
Json = Dict[str, Any]
Body = Union[None, Mapping[str, Any], bytes]


def resolve_api_key(api_key: Optional[str]) -> str:
    key = api_key or os.environ.get("SBBOX_API_KEY") or os.environ.get("GSK_API_KEY")
    if not key:
        raise ValueError(
            "SB-Box API key missing: pass api_key=... or set SBBOX_API_KEY (or GSK_API_KEY) in the environment"
        )
    return key.strip()


def resolve_base_url(base_url: Optional[str]) -> str:
    url = base_url or os.environ.get("SBBOX_BASE_URL") or DEFAULT_BASE_URL
    return url.rstrip("/")


def _retry_after(resp: httpx.Response) -> Optional[float]:
    v = resp.headers.get("retry-after")
    if not v:
        return None
    try:
        return float(v)
    except ValueError:
        return None


def _parse_body(resp: httpx.Response) -> Any:
    if not resp.content:
        return None
    ctype = resp.headers.get("content-type", "")
    if "json" in ctype:
        try:
            return resp.json()
        except ValueError:
            pass
    try:
        return json.loads(resp.content)
    except ValueError:
        return resp.text


def raise_for_response(resp: httpx.Response) -> None:
    if resp.status_code < 400:
        return
    raise error_for_response(
        resp.status_code,
        _parse_body(resp),
        retry_after=_retry_after(resp),
        request_id=resp.headers.get("x-request-id"),
    )


class _Core:
    """Request construction + retry policy, independent of the httpx client flavour."""

    def __init__(
        self,
        api_key: Optional[str],
        base_url: Optional[str],
        timeout: float,
        retries: int,
        user_agent_suffix: Optional[str],
    ) -> None:
        self.api_key = resolve_api_key(api_key)
        self.base_url = resolve_base_url(base_url)
        self.timeout = timeout
        self.retries = max(0, retries)
        ua = USER_AGENT
        if user_agent_suffix:
            ua = f"{ua} {user_agent_suffix}"
        self.headers = {"X-Api-Key": self.api_key, "User-Agent": ua, "Accept": "application/json"}

    def url(self, path: str) -> str:
        return f"{self.base_url}/{path.lstrip('/')}"

    def build(
        self,
        method: str,
        path: str,
        *,
        params: Optional[Mapping[str, Any]] = None,
        body: Body = None,
        timeout: RequestTimeout = None,
        stream: bool = False,
    ) -> Dict[str, Any]:
        headers = dict(self.headers)
        deadline: Any
        if isinstance(timeout, _NoDeadline):
            deadline = httpx.Timeout(None, connect=_CONNECT_TIMEOUT)
        else:
            deadline = timeout if timeout is not None else self.timeout
        kw: Dict[str, Any] = {
            "method": method,
            "url": self.url(path),
            "headers": headers,
            "timeout": deadline,
        }
        if params:
            kw["params"] = {k: v for k, v in params.items() if v is not None}
        if isinstance(body, (bytes, bytearray)):
            headers["Content-Type"] = "application/octet-stream"
            kw["content"] = bytes(body)
        elif body is not None:
            headers["Content-Type"] = "application/json"
            kw["content"] = json.dumps(dict(body)).encode()
        if stream:
            headers["Accept"] = "text/event-stream"
        return kw

    def backoff(self, attempt: int) -> float:
        base = _BACKOFF[min(attempt, len(_BACKOFF) - 1)]
        return base + random.uniform(0, base / 4)

    def should_retry(self, attempt: int, status: Optional[int], exc: Optional[Exception]) -> bool:
        if attempt >= self.retries:
            return False
        if exc is not None:
            return True
        return status in _RETRY_STATUSES


class _SyncHTTP:
    def __init__(self, core: _Core, transport: Optional[httpx.BaseTransport] = None) -> None:
        self.core = core
        self._http = httpx.Client(transport=transport) if transport is not None else httpx.Client()

    def close(self) -> None:
        self._http.close()

    def request(
        self,
        method: str,
        path: str,
        *,
        params: Optional[Mapping[str, Any]] = None,
        body: Body = None,
        timeout: RequestTimeout = None,
        retry: bool = False,
        retry_after_until: Optional[float] = None,
    ) -> httpx.Response:
        kw = self.core.build(method, path, params=params, body=body, timeout=timeout)
        attempt = 0
        while True:
            exc: Optional[Exception] = None
            resp: Optional[httpx.Response] = None
            try:
                resp = self._http.request(**kw)
            except httpx.HTTPError as e:
                exc = e
            if resp is not None:
                # 503 + Retry-After honoured until the call deadline (clone / reset).
                if (
                    retry_after_until is not None
                    and resp.status_code == 503
                    and time.monotonic() < retry_after_until
                ):
                    delay = _retry_after(resp) or 2.0
                    if time.monotonic() + delay <= retry_after_until:
                        time.sleep(delay)
                        continue
                if not (retry and self.core.should_retry(attempt, resp.status_code, None)):
                    raise_for_response(resp)
                    return resp
            elif not (retry and self.core.should_retry(attempt, None, exc)):
                raise TransportError(f"{method} {path}: {redact(exc)}") from exc
            time.sleep(self.core.backoff(attempt))
            attempt += 1

    def json(self, method: str, path: str, **kw: Any) -> Any:
        resp = self.request(method, path, **kw)
        return _parse_body(resp)

    def sse(
        self,
        method: str,
        path: str,
        *,
        body: Body = None,
        params: Optional[Mapping[str, Any]] = None,
        timeout: RequestTimeout = None,
    ) -> Iterator[SSEEvent]:
        kw = self.core.build(method, path, params=params, body=body, timeout=timeout, stream=True)
        try:
            with self._http.stream(**kw) as resp:
                if resp.status_code >= 400:
                    resp.read()
                    raise_for_response(resp)
                yield from iter_sse(resp.iter_text())
        except httpx.HTTPError as e:
            raise TransportError(f"{method} {path}: {redact(e)}") from e


class _AsyncHTTP:
    def __init__(self, core: _Core, transport: Optional[httpx.AsyncBaseTransport] = None) -> None:
        self.core = core
        self._http = httpx.AsyncClient(transport=transport) if transport is not None else httpx.AsyncClient()

    async def aclose(self) -> None:
        await self._http.aclose()

    async def request(
        self,
        method: str,
        path: str,
        *,
        params: Optional[Mapping[str, Any]] = None,
        body: Body = None,
        timeout: RequestTimeout = None,
        retry: bool = False,
        retry_after_until: Optional[float] = None,
    ) -> httpx.Response:
        kw = self.core.build(method, path, params=params, body=body, timeout=timeout)
        attempt = 0
        while True:
            exc: Optional[Exception] = None
            resp: Optional[httpx.Response] = None
            try:
                resp = await self._http.request(**kw)
            except httpx.HTTPError as e:
                exc = e
            if resp is not None:
                if (
                    retry_after_until is not None
                    and resp.status_code == 503
                    and time.monotonic() < retry_after_until
                ):
                    delay = _retry_after(resp) or 2.0
                    if time.monotonic() + delay <= retry_after_until:
                        await asyncio.sleep(delay)
                        continue
                if not (retry and self.core.should_retry(attempt, resp.status_code, None)):
                    raise_for_response(resp)
                    return resp
            elif not (retry and self.core.should_retry(attempt, None, exc)):
                raise TransportError(f"{method} {path}: {redact(exc)}") from exc
            await asyncio.sleep(self.core.backoff(attempt))
            attempt += 1

    async def json(self, method: str, path: str, **kw: Any) -> Any:
        resp = await self.request(method, path, **kw)
        return _parse_body(resp)

    async def sse(
        self,
        method: str,
        path: str,
        *,
        body: Body = None,
        params: Optional[Mapping[str, Any]] = None,
        timeout: RequestTimeout = None,
    ) -> AsyncIterator[SSEEvent]:
        kw = self.core.build(method, path, params=params, body=body, timeout=timeout, stream=True)
        try:
            async with self._http.stream(**kw) as resp:
                if resp.status_code >= 400:
                    await resp.aread()
                    raise_for_response(resp)
                async for ev in aiter_sse(resp.aiter_text()):
                    yield ev
        except httpx.HTTPError as e:
            raise TransportError(f"{method} {path}: {redact(e)}") from e


def normalize_command(command: Union[str, Sequence[str]]) -> List[str]:
    if isinstance(command, str):
        return ["/bin/sh", "-c", command]
    cmd = [str(c) for c in command]
    if not cmd:
        raise ValueError("command must not be empty")
    return cmd


def check_metadata(metadata: Optional[Mapping[str, Any]]) -> Optional[Dict[str, str]]:
    if metadata is None:
        return None
    out: Dict[str, str] = {}
    for k, v in metadata.items():
        if not isinstance(v, str):
            raise ValueError(f"metadata values must be strings; {k!r} is {type(v).__name__}")
        out[str(k)] = v
    return out


def compact(d: Mapping[str, Any]) -> Dict[str, Any]:
    return {k: v for k, v in d.items() if v is not None}
