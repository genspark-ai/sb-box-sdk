"""Template resources (sync + async)."""

from __future__ import annotations

import asyncio
import time
from typing import Any, Dict, List, Optional, Sequence

from ._client import _AsyncHTTP, _SyncHTTP, compact
from .errors import TemplateBuildError, WaitTimeout
from .types import Grant, Template, TemplateBuild


def _build_body(
    name: Optional[str],
    image: Optional[str],
    dockerfile: Optional[str],
    cpu_count: int,
    memory_mb: int,
    disk_size_gb: int,
    start_cmd: Optional[str],
    ready_cmd: Optional[str],
    region: Optional[str],
    log_paths: Optional[Sequence[str]],
    description: Optional[str],
) -> Dict[str, Any]:
    if bool(image) == bool(dockerfile):
        raise ValueError("pass exactly one of image= or dockerfile=")
    if log_paths is not None and len(log_paths) > 8:
        raise ValueError("log_paths accepts at most 8 entries")
    return compact(
        {
            "name": name,
            "image": image,
            "dockerfile": dockerfile,
            "cpuCount": cpu_count,
            "memoryMB": memory_mb,
            "diskSizeGB": disk_size_gb,
            "startCmd": start_cmd,
            "readyCmd": ready_cmd,
            "region": region,
            "logPaths": list(log_paths) if log_paths is not None else None,
            "description": description,
        }
    )


def _grant_body(perm: str, expires_at: Optional[str], reason: Optional[str]) -> Dict[str, Any]:
    if perm not in ("read", "write"):
        raise ValueError("perm must be 'read' or 'write'")
    return compact({"perm": perm, "expires_at": expires_at, "reason": reason})


def _templates_of(data: Any) -> List[Template]:
    rows = data.get("templates", []) if isinstance(data, dict) else (data or [])
    return [Template.from_dict(t) for t in rows]


def _settled(tpl: Template) -> bool:
    return tpl.status in ("ready", "error")


class Templates:
    def __init__(self, http: _SyncHTTP) -> None:
        self._http = http

    def build(
        self,
        *,
        name: Optional[str] = None,
        image: Optional[str] = None,
        dockerfile: Optional[str] = None,
        cpu_count: int = 1,
        memory_mb: int = 2048,
        disk_size_gb: int = 20,
        start_cmd: Optional[str] = None,
        ready_cmd: Optional[str] = None,
        region: Optional[str] = None,
        log_paths: Optional[Sequence[str]] = None,
        description: Optional[str] = None,
    ) -> TemplateBuild:
        body = _build_body(
            name,
            image,
            dockerfile,
            cpu_count,
            memory_mb,
            disk_size_gb,
            start_cmd,
            ready_cmd,
            region,
            log_paths,
            description,
        )
        return TemplateBuild.from_dict(self._http.json("POST", "/templates/build", body=body, timeout=120))

    def wait_ready(self, template_id: str, *, timeout: float = 600, interval: float = 5) -> Template:
        deadline = time.monotonic() + timeout
        while True:
            tpl = self.get(template_id)
            if tpl.status == "ready":
                return tpl
            if tpl.status == "error":
                raise TemplateBuildError(template_id, tpl.build_error, tpl)
            if time.monotonic() >= deadline:
                raise WaitTimeout(f"template {template_id} still {tpl.status} after {timeout:g}s")
            time.sleep(min(interval, max(0.0, deadline - time.monotonic())))

    def list(self) -> List[Template]:
        return _templates_of(self._http.json("GET", "/templates", retry=True))

    def shared(self) -> List[Template]:
        return _templates_of(self._http.json("GET", "/templates/shared", retry=True))

    def get(self, template_id: str) -> Template:
        return Template.from_dict(self._http.json("GET", f"/templates/{template_id}", retry=True))

    def delete(self, template_id: str) -> None:
        self._http.json("DELETE", f"/templates/{template_id}")

    def grant(
        self,
        template_id: str,
        user_id: str,
        *,
        perm: str = "read",
        expires_at: Optional[str] = None,
        reason: Optional[str] = None,
    ) -> Grant:
        body = _grant_body(perm, expires_at, reason)
        return Grant.from_dict(
            self._http.json("PUT", f"/templates/{template_id}/grants/{user_id}", body=body)
        )

    def grants(self, template_id: str) -> List[Grant]:
        return [
            Grant.from_dict(g)
            for g in (self._http.json("GET", f"/templates/{template_id}/grants", retry=True) or [])
        ]

    def revoke(self, template_id: str, user_id: str) -> None:
        self._http.json("DELETE", f"/templates/{template_id}/grants/{user_id}")


class AsyncTemplates:
    def __init__(self, http: _AsyncHTTP) -> None:
        self._http = http

    async def build(
        self,
        *,
        name: Optional[str] = None,
        image: Optional[str] = None,
        dockerfile: Optional[str] = None,
        cpu_count: int = 1,
        memory_mb: int = 2048,
        disk_size_gb: int = 20,
        start_cmd: Optional[str] = None,
        ready_cmd: Optional[str] = None,
        region: Optional[str] = None,
        log_paths: Optional[Sequence[str]] = None,
        description: Optional[str] = None,
    ) -> TemplateBuild:
        body = _build_body(
            name,
            image,
            dockerfile,
            cpu_count,
            memory_mb,
            disk_size_gb,
            start_cmd,
            ready_cmd,
            region,
            log_paths,
            description,
        )
        return TemplateBuild.from_dict(
            await self._http.json("POST", "/templates/build", body=body, timeout=120)
        )

    async def wait_ready(self, template_id: str, *, timeout: float = 600, interval: float = 5) -> Template:
        deadline = time.monotonic() + timeout
        while True:
            tpl = await self.get(template_id)
            if tpl.status == "ready":
                return tpl
            if tpl.status == "error":
                raise TemplateBuildError(template_id, tpl.build_error, tpl)
            if time.monotonic() >= deadline:
                raise WaitTimeout(f"template {template_id} still {tpl.status} after {timeout:g}s")
            await asyncio.sleep(min(interval, max(0.0, deadline - time.monotonic())))

    async def list(self) -> List[Template]:
        return _templates_of(await self._http.json("GET", "/templates", retry=True))

    async def shared(self) -> List[Template]:
        return _templates_of(await self._http.json("GET", "/templates/shared", retry=True))

    async def get(self, template_id: str) -> Template:
        return Template.from_dict(await self._http.json("GET", f"/templates/{template_id}", retry=True))

    async def delete(self, template_id: str) -> None:
        await self._http.json("DELETE", f"/templates/{template_id}")

    async def grant(
        self,
        template_id: str,
        user_id: str,
        *,
        perm: str = "read",
        expires_at: Optional[str] = None,
        reason: Optional[str] = None,
    ) -> Grant:
        body = _grant_body(perm, expires_at, reason)
        return Grant.from_dict(
            await self._http.json("PUT", f"/templates/{template_id}/grants/{user_id}", body=body)
        )

    async def grants(self, template_id: str) -> List[Grant]:
        rows = await self._http.json("GET", f"/templates/{template_id}/grants", retry=True)
        return [Grant.from_dict(g) for g in (rows or [])]

    async def revoke(self, template_id: str, user_id: str) -> None:
        await self._http.json("DELETE", f"/templates/{template_id}/grants/{user_id}")
