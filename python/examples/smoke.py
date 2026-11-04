"""End-to-end smoke test against a real environment.

    SBBOX_API_KEY=gsk-… [SBBOX_BASE_URL=…] python examples/smoke.py

Creates from the public base image, runs a command, writes/reads a file, runs code, freezes (waiting
for the frozen state), resumes, and kills. Never prints the key.
"""

import sys
import time
from typing import Callable, TypeVar

import sbbox
from sbbox.errors import redact


T = TypeVar("T")


def step(name: str, fn: Callable[[], T]) -> T:
    t0 = time.monotonic()
    out = fn()
    print(f"  ok  {name:<12} {time.monotonic() - t0:6.2f}s  {redact(out) if out is not None else ''}")
    return out


def main() -> int:
    client = sbbox.Client()
    print("base_url:", client.base_url)
    sb = step("create", lambda: client.sandboxes.create(metadata={"purpose": "sdk-smoke"}))
    try:
        step("exec", lambda: sb.exec("echo smoke-ok").stdout.strip())
        step("files.write", lambda: sb.files.write("/tmp/smoke.txt", "round-trip"))
        step("files.read", lambda: sb.files.read_text("/tmp/smoke.txt"))
        step("files.list", lambda: [e.name for e in sb.files.list("/tmp")][:5])
        step("exec_stream", lambda: [e.kind for e in sb.exec_stream("echo a; echo b >&2")])
        try:
            step("run_code", lambda: sb.run_code("1+1").text)
        except sbbox.InterpreterUnavailableError as e:
            print("  skip run_code:", e.message)
        step("freeze", lambda: sb.freeze().state)
        step("resume", lambda: sb.resume().state)
        step("exec-after", lambda: sb.exec("cat /tmp/smoke.txt").stdout)
    finally:
        step("kill", lambda: sb.kill())
    try:
        client.sandboxes.get(sb.id)
        print("  FAIL killed sandbox still visible")
        return 1
    except sbbox.NotFoundError:
        print("  ok  killed sandbox answers 404")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except sbbox.SbBoxError as e:
        print("FAIL", type(e).__name__, redact(e))
        sys.exit(1)
