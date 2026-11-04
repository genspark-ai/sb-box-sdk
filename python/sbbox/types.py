"""Typed records returned by the SDK. Every record keeps the raw server payload in ``.raw``."""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Dict, List, Optional


def _get(d: Dict[str, Any], *names: str, default: Any = None) -> Any:
    for n in names:
        if n in d and d[n] is not None:
            return d[n]
    return default


@dataclass
class Regions:
    regions: List[str]
    default: Optional[str]
    raw: Dict[str, Any] = field(repr=False, default_factory=dict)

    @classmethod
    def from_dict(cls, d: Dict[str, Any]) -> "Regions":
        return cls(regions=list(d.get("regions") or []), default=d.get("default"), raw=d)


@dataclass
class Limits:
    plan: Optional[str]
    plan_required: Optional[str]
    limits: Dict[str, Any]
    usage: Dict[str, Any]
    raw: Dict[str, Any] = field(repr=False, default_factory=dict)

    @classmethod
    def from_dict(cls, d: Dict[str, Any]) -> "Limits":
        return cls(
            plan=d.get("plan"),
            plan_required=d.get("planRequired"),
            limits=dict(d.get("limits") or {}),
            usage=dict(d.get("usage") or {}),
            raw=d,
        )


@dataclass
class Usage:
    days: int
    from_: Optional[str]
    to: Optional[str]
    rate_card: Dict[str, Any]
    direct: Dict[str, Any]
    daily: List[Dict[str, Any]]
    raw: Dict[str, Any] = field(repr=False, default_factory=dict)

    @property
    def credits(self) -> float:
        return float(self.direct.get("credits") or 0)

    @classmethod
    def from_dict(cls, d: Dict[str, Any]) -> "Usage":
        return cls(
            days=int(d.get("days") or 0),
            from_=d.get("from"),
            to=d.get("to"),
            rate_card=dict(d.get("rateCard") or {}),
            direct=dict(d.get("direct") or {}),
            daily=list(d.get("daily") or []),
            raw=d,
        )


@dataclass
class Template:
    id: str
    name: Optional[str]
    description: Optional[str]
    namespace: Optional[str]
    base_image: Optional[str]
    status: Optional[str]
    build_error: Optional[str]
    region: Optional[str]
    is_owner: Optional[bool]
    can_create: Optional[bool]
    cpu_count: Optional[int]
    memory_mb: Optional[int]
    disk_size_gb: Optional[int]
    start_cmd: Optional[str]
    ready_cmd: Optional[str]
    created_at: Optional[str]
    updated_at: Optional[str]
    raw: Dict[str, Any] = field(repr=False, default_factory=dict)

    @classmethod
    def from_dict(cls, d: Dict[str, Any]) -> "Template":
        return cls(
            id=_get(d, "id", "templateId", "template_id", default=""),
            name=d.get("name"),
            description=d.get("description"),
            namespace=d.get("namespace"),
            base_image=_get(d, "base_image", "baseImage", "image"),
            status=d.get("status"),
            build_error=_get(d, "buildError", "build_error"),
            region=d.get("region"),
            is_owner=d.get("is_owner"),
            can_create=d.get("can_create"),
            cpu_count=_get(d, "cpuCount", "cpu_count"),
            memory_mb=_get(d, "memoryMB", "memory_mb"),
            disk_size_gb=_get(d, "diskSizeGB", "disk_size_gb"),
            start_cmd=_get(d, "startCmd", "start_cmd"),
            ready_cmd=_get(d, "readyCmd", "ready_cmd"),
            created_at=_get(d, "created_at", "createdAt"),
            updated_at=_get(d, "updated_at", "updatedAt"),
            raw=d,
        )


@dataclass
class TemplateBuild:
    template_id: str
    status: str
    region: Optional[str]
    owner: Optional[str]
    raw: Dict[str, Any] = field(repr=False, default_factory=dict)

    @classmethod
    def from_dict(cls, d: Dict[str, Any]) -> "TemplateBuild":
        return cls(
            template_id=_get(d, "templateId", "template_id", "id", default=""),
            status=d.get("status") or "building",
            region=d.get("region"),
            owner=d.get("owner"),
            raw=d,
        )


@dataclass
class Grant:
    grantee_id: str
    permission: str
    granted_at: Optional[str]
    expires_at: Optional[str]
    raw: Dict[str, Any] = field(repr=False, default_factory=dict)

    @classmethod
    def from_dict(cls, d: Dict[str, Any]) -> "Grant":
        return cls(
            grantee_id=_get(d, "grantee_id", "granteeId", default=""),
            permission=_get(d, "permission", "perm", default="read"),
            granted_at=_get(d, "granted_at", "grantedAt"),
            expires_at=_get(d, "expires_at", "expiresAt"),
            raw=d,
        )


@dataclass
class SandboxVersion:
    version: int
    frozen_at: Optional[str]
    label: Optional[str]

    @classmethod
    def from_dict(cls, d: Dict[str, Any]) -> "SandboxVersion":
        return cls(
            version=int(d.get("version", 0)), frozen_at=_get(d, "frozenAt", "frozen_at"), label=d.get("label")
        )


@dataclass
class SandboxInfo:
    """A sandbox record as returned by create / get / freeze / resume / rollback / clone / reset."""

    id: str
    template_id: Optional[str]
    state: Optional[str]
    region: Optional[str]
    port_url_template: Optional[str]
    domain: Optional[str]
    created_at: Optional[str]
    started_at: Optional[str]
    frozen_at: Optional[str]
    timeout_at: Optional[str]
    cpu_count: Optional[int]
    memory_mb: Optional[int]
    disk_size_gb: Optional[int]
    idle_timeout: Optional[int]
    versions: List[SandboxVersion]
    metadata: Dict[str, Any]
    traffic_access_token: Optional[str]
    is_owner: Optional[bool]
    has_write: Optional[bool]
    raw: Dict[str, Any] = field(repr=False, default_factory=dict)

    def port_url(self, port: int) -> str:
        """Public URL for ``port`` — substitutes the ``{port}`` placeholder of the record's ``portUrl``."""
        if not self.port_url_template:
            raise ValueError("sandbox record has no portUrl")
        return self.port_url_template.replace("{port}", str(int(port)))

    @classmethod
    def from_dict(cls, d: Dict[str, Any]) -> "SandboxInfo":
        return cls(
            id=_get(d, "id", "sandboxId", "sandbox_id", default=""),
            template_id=_get(d, "templateId", "template_id"),
            state=d.get("state"),
            region=d.get("region"),
            port_url_template=_get(d, "portUrl", "port_url"),
            domain=d.get("domain"),
            created_at=_get(d, "createdAt", "created_at"),
            started_at=_get(d, "startedAt", "started_at"),
            frozen_at=_get(d, "frozenAt", "frozen_at"),
            timeout_at=_get(d, "timeoutAt", "timeout_at"),
            cpu_count=_get(d, "cpuCount", "cpu_count"),
            memory_mb=_get(d, "memoryMB", "memory_mb"),
            disk_size_gb=_get(d, "diskSizeGB", "disk_size_gb"),
            idle_timeout=_get(d, "idleTimeout", "idle_timeout"),
            versions=[SandboxVersion.from_dict(v) for v in (d.get("versions") or [])],
            metadata=dict(d.get("metadata") or {}),
            traffic_access_token=_get(d, "trafficAccessToken", "traffic_access_token"),
            is_owner=d.get("is_owner"),
            has_write=d.get("has_write"),
            raw=d,
        )


@dataclass
class SandboxSummary:
    """Compact row from ``GET /sandboxes``."""

    id: str
    template_id: Optional[str]
    state: Optional[str]
    region: Optional[str]
    port_url_template: Optional[str]
    domain: Optional[str]
    created_at: Optional[str]
    updated_at: Optional[str]
    raw: Dict[str, Any] = field(repr=False, default_factory=dict)

    def port_url(self, port: int) -> str:
        if not self.port_url_template:
            raise ValueError("sandbox record has no portUrl")
        return self.port_url_template.replace("{port}", str(int(port)))

    @classmethod
    def from_dict(cls, d: Dict[str, Any]) -> "SandboxSummary":
        return cls(
            id=_get(d, "id", "sandbox_id", default=""),
            template_id=_get(d, "template_id", "templateId"),
            state=d.get("state"),
            region=d.get("region"),
            port_url_template=_get(d, "portUrl", "port_url"),
            domain=d.get("domain"),
            created_at=_get(d, "created_at", "createdAt"),
            updated_at=_get(d, "updated_at", "updatedAt"),
            raw=d,
        )


@dataclass
class CommandResult:
    stdout: str
    stderr: str
    exit_code: int
    raw: Dict[str, Any] = field(repr=False, default_factory=dict)

    @property
    def ok(self) -> bool:
        return self.exit_code == 0

    @classmethod
    def from_dict(cls, d: Dict[str, Any]) -> "CommandResult":
        return cls(
            stdout=d.get("stdout") or "",
            stderr=d.get("stderr") or "",
            exit_code=int(_get(d, "exitCode", "exit_code", default=0)),
            raw=d,
        )


@dataclass
class ExecEvent:
    """One event of a streamed command: kind ∈ {"execId", "stdout", "stderr", "exit", "error"}."""

    kind: str
    data: str = ""
    exit_code: Optional[int] = None
    exec_id: Optional[str] = None
    error: Optional[str] = None
    stage: Optional[str] = None
    seq: Optional[str] = None

    @classmethod
    def from_sse(cls, event: str, payload: Any, seq: Optional[str]) -> "ExecEvent":
        p = payload if isinstance(payload, dict) else {}
        if event == "execId":
            return cls(kind="execId", exec_id=p.get("execId"), seq=seq)
        if event in ("stdout", "stderr", "data"):
            return cls(kind="stdout" if event == "data" else event, data=p.get("data", "") or "", seq=seq)
        if event == "exit":
            code = _get(p, "exitCode", "exit_code")
            return cls(kind="exit", exit_code=int(code) if code is not None else None, seq=seq)
        if event == "error":
            return cls(kind="error", error=p.get("error") or str(payload), stage=p.get("stage"), seq=seq)
        return cls(kind=event, data=str(payload) if payload is not None else "", seq=seq)


@dataclass
class CodeError:
    name: str
    value: str
    traceback: Any

    @classmethod
    def from_dict(cls, d: Dict[str, Any]) -> "CodeError":
        return cls(name=d.get("name") or "", value=d.get("value") or "", traceback=d.get("traceback"))


@dataclass
class CodeResultItem:
    """One output of ``run_code`` — a MIME bundle (text, html, png, json, chart …)."""

    text: Optional[str]
    html: Optional[str]
    markdown: Optional[str]
    svg: Optional[str]
    png: Optional[str]
    jpeg: Optional[str]
    pdf: Optional[str]
    latex: Optional[str]
    json: Any
    javascript: Optional[str]
    data: Any
    chart: Any
    omitted: Optional[bool]
    is_main_result: bool
    raw: Dict[str, Any] = field(repr=False, default_factory=dict)

    @classmethod
    def from_dict(cls, d: Dict[str, Any]) -> "CodeResultItem":
        return cls(
            text=d.get("text"),
            html=d.get("html"),
            markdown=d.get("markdown"),
            svg=d.get("svg"),
            png=d.get("png"),
            jpeg=d.get("jpeg"),
            pdf=d.get("pdf"),
            latex=d.get("latex"),
            json=d.get("json"),
            javascript=d.get("javascript"),
            data=d.get("data"),
            chart=d.get("chart"),
            omitted=d.get("omitted"),
            is_main_result=bool(_get(d, "isMainResult", "is_main_result", default=False)),
            raw=d,
        )


@dataclass
class CodeResult:
    stdout: str
    stderr: str
    execution_count: Optional[int]
    complete: bool
    error: Optional[CodeError]
    results: List[CodeResultItem]
    raw: Dict[str, Any] = field(repr=False, default_factory=dict)

    @property
    def text(self) -> Optional[str]:
        """Text of the main result (the last expression), if any."""
        for r in self.results:
            if r.is_main_result and r.text is not None:
                return r.text
        return self.results[-1].text if self.results else None

    @classmethod
    def from_dict(cls, d: Dict[str, Any]) -> "CodeResult":
        err = d.get("error")
        return cls(
            stdout=d.get("stdout") or "",
            stderr=d.get("stderr") or "",
            execution_count=_get(d, "executionCount", "execution_count"),
            complete=bool(d.get("complete", True)),
            error=CodeError.from_dict(err) if isinstance(err, dict) else None,
            results=[CodeResultItem.from_dict(r) for r in (d.get("results") or []) if isinstance(r, dict)],
            raw=d,
        )


@dataclass
class FileEntry:
    name: str
    path: str
    is_dir: bool
    size: int
    raw: Dict[str, Any] = field(repr=False, default_factory=dict)

    @classmethod
    def from_dict(cls, d: Dict[str, Any]) -> "FileEntry":
        return cls(
            name=d.get("name") or "",
            path=d.get("path") or "",
            is_dir=bool(_get(d, "isDir", "is_dir", default=False)),
            size=int(d.get("size") or 0),
            raw=d,
        )


@dataclass
class ProcessInfo:
    exec_id: str
    pid: int
    sandbox_id: Optional[str]
    raw: Dict[str, Any] = field(repr=False, default_factory=dict)

    @classmethod
    def from_dict(cls, d: Dict[str, Any]) -> "ProcessInfo":
        return cls(
            exec_id=_get(d, "execId", "exec_id", default=""),
            pid=int(d.get("pid") or 0),
            sandbox_id=_get(d, "sandboxId", "sandbox_id"),
            raw=d,
        )


@dataclass
class PtyEvent:
    """``data`` events carry terminal output; the final ``exit`` event carries ``exit_code``."""

    kind: str
    data: str = ""
    exit_code: Optional[int] = None
    reason: Optional[str] = None


@dataclass
class MetricPoint:
    timestamp: Optional[str]
    timestamp_unix: Optional[int]
    cpu_count: Optional[int]
    cpu_used_pct: Optional[float]
    mem_used: Optional[int]
    mem_total: Optional[int]
    mem_cache: Optional[int]
    disk_used: Optional[int]
    disk_total: Optional[int]
    raw: Dict[str, Any] = field(repr=False, default_factory=dict)

    @classmethod
    def from_dict(cls, d: Dict[str, Any]) -> "MetricPoint":
        return cls(
            timestamp=d.get("timestamp"),
            timestamp_unix=d.get("timestampUnix"),
            cpu_count=d.get("cpuCount"),
            cpu_used_pct=d.get("cpuUsedPct"),
            mem_used=d.get("memUsed"),
            mem_total=d.get("memTotal"),
            mem_cache=d.get("memCache"),
            disk_used=d.get("diskUsed"),
            disk_total=d.get("diskTotal"),
            raw=d,
        )


@dataclass
class NetworkConfig:
    allow_internet_access: Optional[bool] = None
    allow_out: Optional[List[str]] = None
    deny_out: Optional[List[str]] = None
    allow_public_traffic: Optional[bool] = None
    mask_request_host: Optional[bool] = None

    def to_dict(self) -> Dict[str, Any]:
        out: Dict[str, Any] = {}
        if self.allow_internet_access is not None:
            out["allowInternetAccess"] = self.allow_internet_access
        if self.allow_out is not None:
            out["allowOut"] = list(self.allow_out)
        if self.deny_out is not None:
            out["denyOut"] = list(self.deny_out)
        if self.allow_public_traffic is not None:
            out["allowPublicTraffic"] = self.allow_public_traffic
        if self.mask_request_host is not None:
            out["maskRequestHost"] = self.mask_request_host
        return out
