"""Minimal Server-Sent-Events parser (``event:`` / ``data:`` / ``id:`` lines, blank line ends an event)."""

from __future__ import annotations

import json
from collections.abc import AsyncIterator, Iterable, Iterator
from dataclasses import dataclass, field
from typing import Any, List, Optional


@dataclass
class SSEEvent:
    event: str = "message"
    data: str = ""
    id: Optional[str] = None
    _lines: List[str] = field(default_factory=list, repr=False)

    def json(self) -> Any:
        return json.loads(self.data) if self.data else None


class _Parser:
    def __init__(self) -> None:
        self._buf = ""
        self._cur = SSEEvent()
        self._has = False

    def feed(self, chunk: str) -> Iterator[SSEEvent]:
        self._buf += chunk
        while True:
            nl = self._buf.find("\n")
            if nl < 0:
                return
            line = self._buf[:nl].rstrip("\r")
            self._buf = self._buf[nl + 1 :]
            ev = self._line(line)
            if ev is not None:
                yield ev

    def flush(self) -> Iterator[SSEEvent]:
        if self._buf:
            ev = self._line(self._buf.rstrip("\r"))
            self._buf = ""
            if ev is not None:
                yield ev
        if self._has:
            yield self._finish()

    def _line(self, line: str) -> Optional[SSEEvent]:
        if line == "":
            return self._finish() if self._has else None
        if line.startswith(":"):
            return None
        name, _, value = line.partition(":")
        value = value.removeprefix(" ")
        self._has = True
        if name == "event":
            self._cur.event = value
        elif name == "data":
            self._cur._lines.append(value)
        elif name == "id":
            self._cur.id = value
        return None

    def _finish(self) -> SSEEvent:
        ev = self._cur
        ev.data = "\n".join(ev._lines)
        self._cur = SSEEvent()
        self._has = False
        return ev


def iter_sse(chunks: Iterable[str]) -> Iterator[SSEEvent]:
    p = _Parser()
    for chunk in chunks:
        yield from p.feed(chunk)
    yield from p.flush()


async def aiter_sse(chunks: AsyncIterator[str]) -> AsyncIterator[SSEEvent]:
    p = _Parser()
    async for chunk in chunks:
        for ev in p.feed(chunk):
            yield ev
    for ev in p.flush():
        yield ev
