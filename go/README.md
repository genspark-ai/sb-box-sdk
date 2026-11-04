# SB-Box Go SDK

Go client for [SB-Box](https://www.genspark.ai/sb-box/docs) sandboxes — create isolated
machines, run commands, move files, freeze and resume them, all from Go.

- Go 1.21+, standard library only (no third-party dependencies).
- Context-aware, typed errors, automatic retries for idempotent calls.

## Install

```bash
go get github.com/genspark-ai/sb-box-sdk/go
```

## Quickstart

```go
package main

import (
	"context"
	"fmt"
	"log"

	sbbox "github.com/genspark-ai/sb-box-sdk/go"
)

func main() {
	ctx := context.Background()
	client, err := sbbox.New() // reads SBBOX_API_KEY (or GSK_API_KEY)
	if err != nil {
		log.Fatal(err)
	}

	sb, err := client.Sandboxes.Create(ctx, sbbox.CreateOptions{Template: "base", Timeout: 600})
	if err != nil {
		log.Fatal(err)
	}
	defer sb.Kill(ctx)

	res, _ := sb.Exec(ctx, []string{"echo", "hello"}, nil)
	fmt.Print(res.Stdout) // hello

	sb.Files.Write(ctx, "/home/user/a.txt", "some text", "")
	text, _ := sb.Files.ReadText(ctx, "/home/user/a.txt", "")
	fmt.Println(text)

	sb.Freeze(ctx, true)  // snapshot; only the disk bills while frozen
	sb.Resume(ctx, 0)     // back in about a second, memory included
	fmt.Println(sb.PortURL(3000)) // public URL of a port served inside the sandbox
}
```

## Configuration

| Option | Environment | Default |
|---|---|---|
| `sbbox.WithAPIKey(key)` | `SBBOX_API_KEY`, then `GSK_API_KEY` | required |
| `sbbox.WithBaseURL(url)` | `SBBOX_BASE_URL` | `https://www.genspark.ai/api/sb-box/v1` |
| `sbbox.WithTimeout(d)` | — | 60 s per request |
| `sbbox.WithRetries(n)` | — | 3 |
| `sbbox.WithHTTPClient(c)` | — | `&http.Client{}` |
| `sbbox.WithUserAgentSuffix(s)` | — | — |

All durations sent to the API (`Timeout` on create / resume / exec / run code,
`SetTimeout`) are **seconds**.

## What you can do

```go
// Account
client.Regions(ctx); client.Limits(ctx); client.Usage(ctx, 30)

// Templates
b, _ := client.Templates.Build(ctx, sbbox.TemplateBuildRequest{Name: "py", Image: "python:3.12"})
tpl, err := client.Templates.WaitReady(ctx, b.TemplateID, 0, 0) // *sbbox.TemplateBuildError on failure
client.Templates.List(ctx); client.Templates.Shared(ctx); client.Templates.Get(ctx, id)
client.Templates.Delete(ctx, id)
client.Templates.Grant(ctx, id, userID, &sbbox.GrantOptions{Perm: "read"}) // Grants / Revoke

// Sandboxes
client.Sandboxes.Create / List(ctx, "running", "frozen") / Shared / Get / Kill
client.Sandboxes.Freeze(ctx, id, true) / Resume / Rollback / Clone / SetTimeout / Reset / Metrics
client.Sandboxes.Grant / Grants / Revoke

// Sandbox handle
sb.Exec(ctx, argv, &sbbox.ExecOptions{Cwd: "/w", Timeout: 120})       // exit code is data, not an error
st, _ := sb.ExecStream(ctx, sbbox.ShellCommand("make test"), nil)        // SSE: stdout / stderr / exit
for ev, ok := st.Next(); ok; ev, ok = st.Next() { fmt.Print(ev.Data) }
sb.RunCode(ctx, "import math; math.pi", nil)                           // stateful interpreter (base template)
bg, _ := sb.Commands.Start(ctx, []string{"npm", "run", "dev"}, nil)     // Input / Close / Wait / Kill / Connect
sb.Files.Write / Read / ReadText / ReadTo / Open / List
p, _ := sb.Pty.Open(ctx, nil); p.Input(ctx, []byte("ls\n")); p.Connect(ctx)
ws, _ := sb.Pty.ConnectWS(ctx, &sbbox.PtyOptions{Rows: 40, Cols: 120}) // bidirectional WebSocket terminal
```

`Freeze(ctx, true)` waits until the sandbox is frozen even when the server answers
"freezing"; `Clone` waits while the clone is still starting.

## Errors

Every failed call returns an error wrapping `*sbbox.APIError` (`Status`, `Code`,
`Message`, `Detail`, `RetryAfter`, `RequestID`, `Fields`). Use the typed errors or
helpers to branch:

```go
sb, err := client.Sandboxes.Create(ctx, sbbox.CreateOptions{})
var limit *sbbox.LimitError
switch {
case sbbox.IsInsufficientCredits(err): // 402 — add credits
case sbbox.IsPlanRequired(err):        // 403 — upgrade required
case errors.As(err, &limit):           // 409 — limit.Limit(), limit.Current()
case sbbox.IsNotFound(err):            // 404 — unknown id, or the sandbox was killed
case sbbox.IsMemoryExpired(err):       // 428 — resume refused; call Reset
case sbbox.IsDiskLost(err):            // 410 — Rollback to the last save point
case sbbox.IsCommandTimeout(err):      // 408 — exec / run code hit its timeout
case sbbox.IsTransport(err):           // network failure
}
```

| Status | Type |
|---|---|
| 400 / 422 | `*ValidationError` |
| 401 | `*AuthenticationError` |
| 402 | `*InsufficientCreditsError` |
| 403 | `*PlanRequiredError`, else `*ForbiddenError` |
| 404 | `*NotFoundError` (`*RegionRetiredError` for a retired region) |
| 408 | `*CommandTimeoutError` |
| 409 | `*LimitError`, `*InterpreterUnavailableError`, else `*ConflictError` |
| 410 | `*DiskLostError` |
| 428 | `*MemoryExpiredError` |
| 499 | `*ClientDisconnectedError` |
| 5xx | `*InterpreterStartFailedError` (502 interpreter), else `*ServerError` |

API keys never appear in error messages.

## Examples

`examples/quickstart` and `examples/smoke` (`SBBOX_API_KEY=… go run ./smoke` from the
`examples` directory) exercise the full create → exec → files → run code → freeze →
resume → kill path.

## License

MIT
