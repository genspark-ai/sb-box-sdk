# SB-Box REST API — contract for the SDKs

Source of truth: the public user guide (https://www.genspark.ai/sb-box/docs,
§2, §3, §8.5, §15, §16). This file condenses it for SDK authors. When they disagree, the
guide wins; fix this file.

## Transport

- Base URL `https://www.genspark.ai/api/sb-box/v1` (configurable via `SBBOX_BASE_URL`).
- Auth on every call: header `X-Api-Key: gsk-…` **or** `Authorization: Bearer gsk-…`. The SDKs
  read the key from an explicit argument, else `SBBOX_API_KEY`, else `GSK_API_KEY`.
- JSON bodies (`Content-Type: application/json`) except the two file-stream routes.
- **All lifetimes are seconds.** Never send `timeoutMs`.
- `User-Agent: sbbox-<lang>-sdk/<version>` on every request.
- Killed sandboxes answer `404 sandbox not found` everywhere, like unknown ids.

## Error envelope

Two shapes, by route:
- `{"detail": <string | object>}` — most routes. Object form carries `code` (`sb_box_plan_required`,
  `sb_box_insufficient_credits`, `sb_box_concurrency_limit`, `sb_box_spec_limit`,
  `sb_box_build_concurrency_limit`, `sb_box_region_retired`) plus fields (`plan`, `limit`, `current`,
  `requested`, `balance`, `message`, `upgrade`, `region`).
- `{"error": <string>, …}` — `exec`, `run-code` (extra: `sandbox_id`, `state`, `suggestion`,
  `stage`, `killed`), `rollback` (extra: `reason`, `recovery`, `resumable`).
- Top-level `{"reason": "memory_expired", "recovery": ["reset"]}` on `428` (resume / rollback / clone).
- `422` FastAPI validation: `{"status": -2, "message": "...", "errors": [{"loc": [...], "msg": ...}]}`.
- `503` may carry `Retry-After` (clone still starting, reset storage busy).

Status → SDK error class (every class exposes `status`, `code`, `message`, `detail`, `retry_after`):

| Status | Class | Notes |
|---|---|---|
| 400 | `ValidationError` | bad input |
| 401 | `AuthenticationError` | missing / invalid key |
| 402 | `InsufficientCreditsError` | `code=sb_box_insufficient_credits`, `balance` |
| 403 | `PlanRequiredError` when `code=sb_box_plan_required` (`plan`, `upgrade`), else `ForbiddenError` (not enabled / permission denied) |
| 404 | `NotFoundError` (`code=sb_box_region_retired` → `RegionRetiredError` subclass) |
| 408 | `CommandTimeoutError` (exec: `stage=timeout`, `killed`) |
| 409 | `LimitError` when `code` ends with `_limit` (`plan`, `limit`, `current`/`requested`); `InterpreterUnavailableError` when `code=sb_box_interpreter_unavailable`; else `ConflictError` |
| 410 | `DiskLostError` — `disk_lost`, body `{"error","reason":"disk_lost","resumable":false,"recovery":[…]}`. Follow `recovery`: on **resume** it is `["reset","recreate"]` (the sandbox was never frozen, so there is no save point); on **reset** it is `["resume","rollback","recreate"]` (`rollback(0)` restores the newest save point); on **rollback** 410 = never frozen → create a new sandbox |
| 422 | `ValidationError` (collect `errors[]`) |
| 428 | `MemoryExpiredError` (`recovery`) |
| 499 | `ClientDisconnectedError` |
| 502 | `InterpreterStartFailedError` when `code=sb_box_interpreter_start_failed`, else `ServerError` |
| 5xx | `ServerError` |
| transport | `ConnectionError`-equivalent of the language |

Retry policy (built in, configurable, default on): idempotent reads (GET) and `freeze`/`resume`/
`timeout`/`kill` retry up to 3× with jittered backoff (0.5 s, 1 s, 2 s) on connection errors and
502/503/504; `clone` and `reset` honour `Retry-After` on 503 (default 2 s) up to the call deadline;
`create`, `exec`, `run-code`, `commands/*`, file writes are **never** retried automatically.

## Endpoints

### Account

| Method / path | Body / query | Response |
|---|---|---|
| `GET /regions` | — | `{"regions": [..], "default": "westus2"}` |
| `GET /limits` | — | `{"plan","planRequired","limits":{maxConcurrentSandboxes,maxCpu,maxMemoryMB,maxDiskGB,maxConcurrentBuilds},"usage":{runningSandboxes,buildingTemplates}}` |
| `GET /usage?days=30` | `days` 1–366 | `{"days","from","to","rateCard":{…},"direct":{runSec,cpuCoreSec,memGBSec,diskGBSec,credits},"daily":[{"day":"YYYYMMDD","direct":{run_sec,cpu_core_sec,mem_gb_sec,disk_gb_sec,credits}}]}` (extra fields exist; keep raw) |

### Templates

Record: `id, name, description, namespace ("public"|"private"), base_image, status ("building"|"ready"|"error"),
buildError?, region, is_owner, can_create, cogen_id?, created_at, updated_at` (plus `cpuCount, memoryMB,
diskSizeGB` on GET; `startCmd`/`readyCmd` are build inputs only and are NOT returned).

| Method / path | Body / query | Response |
|---|---|---|
| `POST /templates/build` | `{name?, image | dockerfile, cpuCount=1, memoryMB=2048, diskSizeGB=20, startCmd?, readyCmd?, region?, logPaths?[≤8], description?}` | `{"templateId","status":"building","region","owner"}` — async; poll GET until `ready`/`error` |
| `GET /templates` | — | `{"templates":[record…]}` |
| `GET /templates/shared` | — | `[record…]` |
| `GET /templates/{id}` | — | record |
| `DELETE /templates/{id}` | — | `{"status":"deleted"}` |
| `PUT /templates/{id}/grants/{userId}` | `{perm:"read"|"write"="read", expires_at?, reason?}` | `{"template_id","grantee_id","permission","granted_at","expires_at"}` |
| `GET /templates/{id}/grants` | — | `[{grantee_id,permission,granted_at,expires_at}]` |
| `DELETE /templates/{id}/grants/{userId}` | — | `{"status":"revoked"}` |

### Sandboxes — lifecycle

Record (create/get/freeze/resume/rollback/clone/reset answer the record; some fields only when set):
`id, templateId, state, region, portUrl ("https://{port}-<id>.sandbox…", literal `{port}` placeholder),
domain, createdAt, startedAt, frozenAt?, timeoutAt?, cpuCount, memoryMB, diskSizeGB, idleTimeout,
versions?[{version, frozenAt, label}], metadata{…, cogenId}, trafficAccessToken? (create only, when
allowPublicTraffic=false), is_owner?, has_write?, cogen_id?, template_id?, created_at?, updated_at?`.
States: `starting, running, freezing, frozen, exited, failed, unknown` (list only), `killed` is never returned.

| Method / path | Body / query | Response / notes |
|---|---|---|
| `POST /sandboxes` | `{templateName? | templateId?, timeout? (s, 0/omit = none), lifecycle:{onTimeout:"freeze"|"kill"}?, metadata?, envVars?, network:{allowInternetAccess?,allowOut?[],denyOut?[],allowPublicTraffic?,maskRequestHost?}?}` | record (state running). `templateName:"base"` = newest public base. Serialised per account; may wait up to 10 s |
| `GET /sandboxes?state=a,b` | `state` ⊆ {starting,running,freezing,frozen,exited,failed,unknown,all}; omit = all | `{"sandboxes":[compact rows: id, template_id, state, region, portUrl, domain, cogen_id, created_at, updated_at], "hasMore": false}` |
| `GET /sandboxes/shared` | — | `[record…]` |
| `GET /sandboxes/{id}` | — | record |
| `DELETE /sandboxes/{id}` | — | `{"status":"killed"}` |
| `POST /sandboxes/{id}/freeze` | — | 200 record (frozen) · 200 `{"state":"frozen","status":"already frozen"}` · **202** `{"state":"freezing",…}` → poll GET until `frozen` (SDK `freeze(wait=True)` polls, default 90 s) |
| `POST /sandboxes/{id}/resume` | `{timeout?}` | record (running); idempotent. 410 `disk_lost` = never frozen and the disk is gone → `recovery: ["reset","recreate"]` |
| `POST /sandboxes/{id}/rollback` | `{version=0, timeout?}` | record; `version 0` = newest save point. 410 `disk_lost` here = never frozen and the disk is gone → create a new sandbox |
| `POST /sandboxes/{id}/clone` | `{version=0, metadata?}` | new record (owner = caller); 503 + Retry-After while starting |
| `POST /sandboxes/{id}/timeout` | `{timeout}` (0 clears) | `{"status":"ok"}` |
| `POST /sandboxes/{id}/reset` | — | record (running); 410 `disk_lost` → `recovery: ["resume","rollback","recreate"]` — `rollback(0)` when `versions` is non-empty |
| `GET /sandboxes/{id}/metrics?start=&end=` | unix seconds | `[{timestamp,timestampUnix,cpuCount,cpuUsedPct,memUsed,memTotal,memCache,diskUsed,diskTotal}]` |
| `PUT/GET/DELETE /sandboxes/{id}/grants[/{userId}]` | as templates | as templates (`sandbox_id`) |

### Sandboxes — commands (frozen sandboxes auto-resume)

| Method / path | Body | Response |
|---|---|---|
| `POST /sandboxes/{id}/exec` | `{command:[argv…] (required), cwd?, envVars?, user?, timeout=60 (≤600)}` | `{"stdout","stderr","exitCode"}` — non-zero exit is still 200; 408 `{"error","stage":"timeout","killed":true}` |
| `POST /sandboxes/{id}/sse/exec` | same | `text/event-stream`: `event: execId` `{"execId"}` → `stdout`/`stderr` `{"data"}` (with `id:` seq) → `exit` `{"exitCode"}` **or** `error` `{"error","stage"}` |
| `POST /sandboxes/{id}/run-code` | `{code (required), language="python", contextId?, envVars?, timeout=60 (≤540), images="base64"|"omit"}` | `{"stdout","stderr","executionCount","complete","error":{name,value,traceback}|null,"results":[mime bundles: text/html/markdown/svg/png/jpeg/pdf/latex/json/javascript/data/chart/omitted/isMainResult]}`; 409 `sb_box_interpreter_unavailable`, 502 `sb_box_interpreter_start_failed` |
| `POST /sandboxes/{id}/commands/start` | `{command, cwd?, envVars?, user?}` | `{"execId","pid"}` |
| `GET /sandboxes/{id}/commands` | — | `[{execId,pid,sandboxId}]` |
| `POST /sandboxes/{id}/commands/{execId}/input` | `{data}` | `{"written"}` |
| `POST /sandboxes/{id}/commands/{execId}/close` | — | `{"status":"ok"}` |
| `POST /sandboxes/{id}/commands/{execId}/wait` | — | `{"exitCode"}` (blocks) |
| `GET /sandboxes/{id}/commands/{execId}/connect` | header `Last-Event-ID` or `?from_seq=` to reattach | SSE: `stdout`/`stderr` `{"data"}` with `id:` = sequence, `event: gap` `{"error"}` (output lost between seqs), `event: error`, terminal `event: done` `{"status":"disconnected"}`. After the command has exited the stream is either 404 (unknown execId) or `event: error` ("session not re-attachable …") followed by `done` — SDKs end the iterator in both cases. Detaching never kills the process (guide §8.3 / §15.4) |
| `POST /sandboxes/{id}/commands/kill` | `{pid, signal=9}` | `{"status":"ok"}` |
| `POST /sandboxes/{id}/commands/send-stdin` | `{pid, data}` | `{"status":"ok"}` |

### Sandboxes — files (frozen sandboxes auto-resume)

| Method / path | Body / query | Response |
|---|---|---|
| `POST /sandboxes/{id}/files/stream?path=&user=` | raw bytes body, `Content-Type: application/octet-stream` | `{"written": n}` |
| `GET /sandboxes/{id}/files/stream?path=&user=` | — | raw bytes; 400 directory, 403 permission, 404 file not found |
| `GET /sandboxes/{id}/files/list?path=/&user=` | — | `[{name,path,isDir,size}]` (`[]` also for a missing dir) |

### Sandboxes — PTY

| Method / path | Body | Response |
|---|---|---|
| `POST /sandboxes/{id}/pty` | `{command=["/bin/bash"], rows=24, cols=80, cwd?, envVars?}` | `{"execId","pid"}` |
| `POST /sandboxes/{id}/pty/{execId}/input` | raw bytes | `{"status":"ok"}` |
| `GET /sandboxes/{id}/pty/{execId}/connect` | — | SSE: `event: data` `{"data"}` … `event: exit` `{"exitCode"}` (a `reason` field such as `"eof"` may be present but is undocumented) |
| `POST /sandboxes/{id}/pty/{execId}/resize` | `{rows, cols}` | `{"status":"ok"}` |
| `POST /sandboxes/{id}/pty/{execId}/kill` | — | `{"status":"ok"}` |
| `GET /sandboxes/{id}/ws/pty` (WebSocket) | first frame `{command?,cwd?,rows?,cols?,envVars?}` within 10 s; then `{"type":"stdin","data"}`, `{"type":"resize","rows","cols"}`, `{"type":"kill"}` | server frames `{"type":"stdout","data","seq"}`, `{"type":"exit","exitCode"}`, `{"type":"error","error"}`; auth `?api_key=` or `X-Api-Key` |

## Things the SDKs must get right

1. `portUrl` contains the literal `{port}`; provide `port_url(port)` that substitutes it.
2. `state=killed` never comes back; a kill makes every later call 404.
3. `freeze` can answer 202; expose `wait` (poll GET until `frozen`, 2 s interval, 90 s cap).
4. `templates.build` is async; expose `wait_ready(id, timeout=600, interval=5)` that returns the record
   or raises `TemplateBuildError` (status `error`, carries `buildError`).
5. `exec` exit codes are data, not errors. Only `CommandTimeoutError` (408) is raised.
6. File writes send raw bytes; strings are UTF-8 encoded. Reads return bytes (helpers for text).
7. SSE parsing: lines `event:`, `data:`, `id:`; blank line ends an event; `data` is JSON.
8. Never log or echo the API key; redact `gsk-…` in error strings.
9. `metadata` values are strings; reject non-string values client-side with a clear error.
10. Deprecated inputs (`timeoutMs`, `version` on resume, `namespace`/`templateNamespace` on create,
    `pause`) are not exposed.
