"""Typed errors raised by the SB-Box SDK.

Every error exposes ``status`` (HTTP status, 0 for transport failures), ``code``
(the API's machine-readable code when present), ``message``, ``detail`` (the raw
parsed body) and ``retry_after`` / ``request_id`` when the server sent them.
"""

from __future__ import annotations

import re
from typing import Any, List, Optional

_KEY_RE = re.compile(r"gsk-[A-Za-z0-9._-]+")


def redact(text: Any) -> str:
    """Replace any API key that leaked into a string with ``gsk-REDACTED``."""
    return _KEY_RE.sub("gsk-REDACTED", str(text))


class SbBoxError(Exception):
    def __init__(
        self,
        message: str,
        *,
        status: int = 0,
        code: Optional[str] = None,
        detail: Any = None,
        retry_after: Optional[float] = None,
        request_id: Optional[str] = None,
    ) -> None:
        self.message = redact(message)
        self.status = status
        self.code = code
        self.detail = detail
        self.retry_after = retry_after
        self.request_id = request_id
        super().__init__(self.message)

    def __str__(self) -> str:
        bits = [self.message]
        if self.status:
            bits.append(f"(status={self.status}")
            if self.code:
                bits[-1] += f", code={self.code}"
            bits[-1] += ")"
        return " ".join(bits)

    def _field(self, name: str, default: Any = None) -> Any:
        if isinstance(self.detail, dict):
            inner = self.detail.get("detail")
            if isinstance(inner, dict) and name in inner:
                return inner[name]
            return self.detail.get(name, default)
        return default


class TransportError(SbBoxError):
    """The request never produced an HTTP response (connection refused, timeout, …)."""


class ValidationError(SbBoxError):
    @property
    def errors(self) -> List[Any]:
        return list(self._field("errors", []) or [])


class AuthenticationError(SbBoxError):
    pass


class ForbiddenError(SbBoxError):
    pass


class PlanRequiredError(ForbiddenError):
    @property
    def plan(self) -> Optional[str]:
        v = self._field("plan")
        return None if v is None else str(v)

    @property
    def upgrade(self) -> Optional[str]:
        v = self._field("upgrade")
        return None if v is None else str(v)


class InsufficientCreditsError(SbBoxError):
    @property
    def balance(self) -> Any:
        return self._field("balance")


class NotFoundError(SbBoxError):
    pass


class RegionRetiredError(NotFoundError):
    @property
    def region(self) -> Optional[str]:
        v = self._field("region")
        return None if v is None else str(v)


class CommandTimeoutError(SbBoxError):
    @property
    def killed(self) -> bool:
        return bool(self._field("killed", False))


class ConflictError(SbBoxError):
    pass


class LimitError(ConflictError):
    @property
    def plan(self) -> Optional[str]:
        v = self._field("plan")
        return None if v is None else str(v)

    @property
    def limit(self) -> Any:
        return self._field("limit")

    @property
    def current(self) -> Any:
        return self._field("current")

    @property
    def requested(self) -> Any:
        return self._field("requested")


class InterpreterUnavailableError(ConflictError):
    pass


class DiskLostError(SbBoxError):
    """410 — the disk is gone. ``recovery`` lists the accepted next steps
    (``rollback`` when the sandbox has save points); otherwise create a new sandbox."""

    @property
    def recovery(self) -> List[str]:
        return [str(x) for x in (self._field("recovery", []) or [])]

    @property
    def resumable(self) -> Optional[bool]:
        v = self._field("resumable")
        return None if v is None else bool(v)


class MemoryExpiredError(SbBoxError):
    @property
    def recovery(self) -> List[str]:
        return [str(x) for x in (self._field("recovery", []) or [])]


class ClientDisconnectedError(SbBoxError):
    pass


class ServerError(SbBoxError):
    pass


class InterpreterStartFailedError(ServerError):
    pass


class TemplateBuildError(SbBoxError):
    """``templates.wait_ready`` saw the build finish with status ``error``."""

    def __init__(self, template_id: str, build_error: Optional[str], template: Any = None) -> None:
        super().__init__(f"template {template_id} failed to build: {build_error or 'unknown error'}")
        self.template_id = template_id
        self.build_error = build_error
        self.template = template


class WaitTimeout(SbBoxError):
    """A client-side wait (freeze, build) exceeded its deadline."""


def _extract(body: Any) -> tuple[str, Optional[str]]:
    """Return (message, code) from either error envelope."""
    if isinstance(body, dict):
        code = body.get("code") or body.get("reason")
        d = body.get("detail")
        if isinstance(d, dict):
            code = d.get("code") or d.get("reason") or code
            msg = d.get("message") or d.get("error") or d.get("detail")
            if not isinstance(msg, str):
                msg = str(d)
            return msg, code
        if isinstance(d, str):
            return d, code
        for key in ("error", "message", "reason"):
            if isinstance(body.get(key), str):
                return body[key], code
        return str(body), code
    if isinstance(body, str) and body:
        return body, None
    return "", None


def error_for_response(
    status: int,
    body: Any,
    *,
    retry_after: Optional[float] = None,
    request_id: Optional[str] = None,
) -> SbBoxError:
    """Map an HTTP error response to the right exception class (API.md table)."""
    message, code = _extract(body)
    if not message:
        message = f"HTTP {status}"
    kw = dict(status=status, code=code, detail=body, retry_after=retry_after, request_id=request_id)
    cls: type = ServerError
    if status in (400, 422):
        cls = ValidationError
    elif status == 401:
        cls = AuthenticationError
    elif status == 402:
        cls = InsufficientCreditsError
    elif status == 403:
        cls = PlanRequiredError if code == "sb_box_plan_required" else ForbiddenError
    elif status == 404:
        cls = RegionRetiredError if code == "sb_box_region_retired" else NotFoundError
    elif status == 408:
        cls = CommandTimeoutError
    elif status == 409:
        if code and code.endswith("_limit"):
            cls = LimitError
        elif code == "sb_box_interpreter_unavailable":
            cls = InterpreterUnavailableError
        else:
            cls = ConflictError
    elif status == 410:
        cls = DiskLostError
    elif status == 428:
        cls = MemoryExpiredError
    elif status == 499:
        cls = ClientDisconnectedError
    elif status == 502 and code == "sb_box_interpreter_start_failed":
        cls = InterpreterStartFailedError
    elif status >= 500:
        cls = ServerError
    elif status >= 400:
        cls = SbBoxError
    err: SbBoxError = cls(message, **kw)
    return err


__all__ = [
    "AuthenticationError",
    "ClientDisconnectedError",
    "CommandTimeoutError",
    "ConflictError",
    "DiskLostError",
    "ForbiddenError",
    "InsufficientCreditsError",
    "InterpreterStartFailedError",
    "InterpreterUnavailableError",
    "LimitError",
    "MemoryExpiredError",
    "NotFoundError",
    "PlanRequiredError",
    "RegionRetiredError",
    "SbBoxError",
    "ServerError",
    "TemplateBuildError",
    "TransportError",
    "ValidationError",
    "WaitTimeout",
    "error_for_response",
    "redact",
]
