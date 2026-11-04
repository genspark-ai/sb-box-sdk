"""Top-level clients."""

from __future__ import annotations

from typing import Any, Optional

import httpx

from ._client import _AsyncHTTP, _Core, _SyncHTTP
from .sandboxes import AsyncSandboxes, Sandboxes
from .templates import AsyncTemplates, Templates
from .types import Limits, Regions, Usage


class Client:
    """Synchronous SB-Box client.

    ``api_key`` defaults to ``SBBOX_API_KEY`` / ``GSK_API_KEY``; ``base_url`` to ``SBBOX_BASE_URL``
    or the production API.
    """

    def __init__(
        self,
        api_key: Optional[str] = None,
        *,
        base_url: Optional[str] = None,
        timeout: float = 60,
        retries: int = 3,
        user_agent_suffix: Optional[str] = None,
        transport: Optional[httpx.BaseTransport] = None,
    ) -> None:
        self._core = _Core(api_key, base_url, timeout, retries, user_agent_suffix)
        self._http = _SyncHTTP(self._core, transport)
        self.sandboxes = Sandboxes(self._http)
        self.templates = Templates(self._http)

    @property
    def base_url(self) -> str:
        return self._core.base_url

    def regions(self) -> Regions:
        return Regions.from_dict(self._http.json("GET", "/regions", retry=True) or {})

    def limits(self) -> Limits:
        return Limits.from_dict(self._http.json("GET", "/limits", retry=True) or {})

    def usage(self, days: int = 30) -> Usage:
        if not 1 <= int(days) <= 366:
            raise ValueError("days must be between 1 and 366")
        return Usage.from_dict(self._http.json("GET", "/usage", params={"days": int(days)}, retry=True) or {})

    def close(self) -> None:
        self._http.close()

    def __enter__(self) -> "Client":
        return self

    def __exit__(self, *exc: Any) -> None:
        self.close()

    def __repr__(self) -> str:
        return f"sbbox.Client(base_url={self.base_url!r})"


class AsyncClient:
    """Asynchronous SB-Box client (same surface as :class:`Client`, every method awaitable)."""

    def __init__(
        self,
        api_key: Optional[str] = None,
        *,
        base_url: Optional[str] = None,
        timeout: float = 60,
        retries: int = 3,
        user_agent_suffix: Optional[str] = None,
        transport: Optional[httpx.AsyncBaseTransport] = None,
    ) -> None:
        self._core = _Core(api_key, base_url, timeout, retries, user_agent_suffix)
        self._http = _AsyncHTTP(self._core, transport)
        self.sandboxes = AsyncSandboxes(self._http)
        self.templates = AsyncTemplates(self._http)

    @property
    def base_url(self) -> str:
        return self._core.base_url

    async def regions(self) -> Regions:
        return Regions.from_dict(await self._http.json("GET", "/regions", retry=True) or {})

    async def limits(self) -> Limits:
        return Limits.from_dict(await self._http.json("GET", "/limits", retry=True) or {})

    async def usage(self, days: int = 30) -> Usage:
        if not 1 <= int(days) <= 366:
            raise ValueError("days must be between 1 and 366")
        return Usage.from_dict(
            await self._http.json("GET", "/usage", params={"days": int(days)}, retry=True) or {}
        )

    async def aclose(self) -> None:
        await self._http.aclose()

    async def __aenter__(self) -> "AsyncClient":
        return self

    async def __aexit__(self, *exc: Any) -> None:
        await self.aclose()

    def __repr__(self) -> str:
        return f"sbbox.AsyncClient(base_url={self.base_url!r})"
