package sbbox

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

var ctx = context.Background()

func TestNewKeyResolution(t *testing.T) {
	t.Setenv("SBBOX_API_KEY", "")
	t.Setenv("GSK_API_KEY", "")
	t.Setenv("SBBOX_BASE_URL", "")
	if _, err := New(); err == nil || !strings.Contains(err.Error(), "SBBOX_API_KEY") || !strings.Contains(err.Error(), "GSK_API_KEY") {
		t.Fatalf("want missing-key error naming both vars, got %v", err)
	}
	t.Setenv("GSK_API_KEY", "gsk-env")
	c, err := New()
	if err != nil || c.apiKey != "gsk-env" || c.BaseURL() != defaultBaseURL {
		t.Fatalf("env fallback: %v %+v", err, c)
	}
	t.Setenv("SBBOX_API_KEY", "gsk-primary")
	t.Setenv("SBBOX_BASE_URL", "https://example.test/api/sb-box/v1/")
	c, _ = New()
	if c.apiKey != "gsk-primary" || c.BaseURL() != "https://example.test/api/sb-box/v1" {
		t.Fatalf("precedence: %+v", c)
	}
	c, _ = New(WithAPIKey("gsk-arg"), WithBaseURL("http://x"), WithUserAgentSuffix("myapp/1"))
	if c.apiKey != "gsk-arg" || !strings.HasSuffix(c.userAgent(), " myapp/1") {
		t.Fatalf("args: %+v %s", c, c.userAgent())
	}
}

func TestAuthAndUserAgent(t *testing.T) {
	m := newMock(t)
	c := m.client(t)
	if _, err := c.Limits(ctx); err != nil {
		t.Fatal(err)
	}
	r := m.last()
	if r.Header.Get("X-Api-Key") != testKey || !strings.HasPrefix(r.Header.Get("User-Agent"), "sbbox-go-sdk/0.1.0") {
		t.Fatalf("headers: %v", r.Header)
	}
	bad := m.client(t, WithAPIKey("gsk-wrong"))
	_, err := bad.Limits(ctx)
	if !IsAuthentication(err) || Status(err) != 401 {
		t.Fatalf("want 401 AuthenticationError, got %v", err)
	}
}

func TestAccountEndpointsAndRetry(t *testing.T) {
	m := newMock(t)
	c := m.client(t)
	reg, err := c.Regions(ctx) // first answer is a 503 → retried
	if err != nil || reg.Default != "westus2" || len(reg.Regions) != 2 {
		t.Fatalf("regions: %v %+v", err, reg)
	}
	if n := m.count("GET", "/regions"); n != 2 {
		t.Fatalf("want 2 attempts, got %d", n)
	}
	lim, _ := c.Limits(ctx)
	if lim.Plan != "plus" || lim.Limits.MaxConcurrentSandboxes != 20 || lim.Usage.RunningSandboxes != 1 {
		t.Fatalf("limits: %+v", lim)
	}
	u, err := c.Usage(ctx, 7)
	if err != nil || m.last().Query != "days=7" || u.Direct.Credits != 1.5 || len(u.Daily) != 1 || u.Daily[0].Direct.RunSec != 60 || u.Daily[0].Day != "20261104" || len(u.Raw) == 0 {
		t.Fatalf("usage: %v %+v", err, u)
	}
}

func TestNoRetryOnServerErrorForCreate(t *testing.T) {
	m := newMock(t)
	c := m.client(t)
	_, err := c.Sandboxes.Create(ctx, CreateOptions{Template: "boom"})
	if !IsServerError(err) {
		t.Fatalf("want ServerError, got %v", err)
	}
	if strings.Contains(err.Error(), "gsk-leaked") || !strings.Contains(err.Error(), "gsk-REDACTED") {
		t.Fatalf("key not redacted: %v", err)
	}
	if n := m.count("POST", "/sandboxes"); n != 1 {
		t.Fatalf("create must not retry, got %d attempts", n)
	}
}

func TestTemplates(t *testing.T) {
	m := newMock(t)
	c := m.client(t)
	b, err := c.Templates.Build(ctx, TemplateBuildRequest{Name: "n", Image: "python:3.12", CPUCount: 2, MemoryMB: 4096})
	if err != nil || b.TemplateID != "tpl-new" || b.Status != "building" {
		t.Fatalf("build: %v %+v", err, b)
	}
	body := m.lastBody(t)
	if body["image"] != "python:3.12" || body["cpuCount"] != float64(2) || body["memoryMB"] != float64(4096) {
		t.Fatalf("build body: %v", body)
	}
	tpl, err := c.Templates.WaitReady(ctx, "tpl-new", time.Minute, 5*time.Millisecond)
	if err != nil || tpl.Status != "ready" || tpl.CPUCount != 2 {
		t.Fatalf("wait: %v %+v", err, tpl)
	}
	if n := m.count("GET", "/templates/tpl-new"); n != 3 {
		t.Fatalf("want 3 polls, got %d", n)
	}
	_, err = c.Templates.WaitReady(ctx, "tpl-bad", time.Minute, time.Millisecond)
	var be *TemplateBuildError
	if !errors.As(err, &be) || be.BuildError == "" || strings.Contains(err.Error(), "gsk-secret") {
		t.Fatalf("want TemplateBuildError (redacted), got %v", err)
	}
	if _, err := c.Templates.Build(ctx, TemplateBuildRequest{Name: "x"}); !IsValidation(err) || Status(err) != 422 {
		t.Fatalf("422 → ValidationError: %v", err)
	}
	_, err = c.Templates.Build(ctx, TemplateBuildRequest{Image: "i", CPUCount: 99})
	var le *LimitError
	if !errors.As(err, &le) || le.Code != "sb_box_spec_limit" || le.Limit() != 4 || le.Requested() != 99 || le.Plan() != "plus" {
		t.Fatalf("spec limit: %v", err)
	}
	list, err := c.Templates.List(ctx)
	if err != nil || len(list) != 1 || list[0].Namespace != "private" || !list[0].IsOwner {
		t.Fatalf("list: %v %+v", err, list)
	}
	sh, _ := c.Templates.Shared(ctx)
	if len(sh) != 1 || sh[0].ID != "tpl-s" {
		t.Fatalf("shared: %+v", sh)
	}
	if err := c.Templates.Delete(ctx, "tpl-1"); err != nil {
		t.Fatal(err)
	}
	g, err := c.Templates.Grant(ctx, "tpl-1", "bob", nil)
	if err != nil || g.Permission != "read" || m.lastBody(t)["perm"] != "read" {
		t.Fatalf("grant default read: %v %+v", err, g)
	}
	g, _ = c.Templates.Grant(ctx, "tpl-1", "bob", &GrantOptions{Perm: "write"})
	if g.Permission != "write" {
		t.Fatalf("grant write: %+v", g)
	}
	gs, _ := c.Templates.Grants(ctx, "tpl-1")
	if len(gs) != 1 || gs[0].GranteeID != "bob" {
		t.Fatalf("grants: %+v", gs)
	}
	if err := c.Templates.Revoke(ctx, "tpl-1", "bob"); err != nil || m.last().Method != "DELETE" {
		t.Fatalf("revoke: %v", err)
	}
	_, err = c.Templates.Get(ctx, "tpl-gone")
	var rr *RegionRetiredError
	if !errors.As(err, &rr) || !IsNotFound(err) || !IsRegionRetired(err) || rr.Region() != "eastus" {
		t.Fatalf("region retired: %v", err)
	}
}

func TestSandboxLifecycle(t *testing.T) {
	m := newMock(t)
	c := m.client(t)
	sb, err := c.Sandboxes.Create(ctx, CreateOptions{Timeout: 300, OnTimeout: "kill", Metadata: map[string]string{"a": "b"}, EnvVars: map[string]string{"X": "1"},
		Network: &Network{AllowOut: []string{"pypi.org"}}})
	if err != nil {
		t.Fatal(err)
	}
	body := m.lastBody(t)
	if body["templateName"] != "base" || body["timeout"] != float64(300) || body["lifecycle"].(map[string]any)["onTimeout"] != "kill" ||
		body["metadata"].(map[string]any)["a"] != "b" || body["envVars"].(map[string]any)["X"] != "1" || body["network"].(map[string]any)["allowOut"].([]any)[0] != "pypi.org" {
		t.Fatalf("create body: %v", body)
	}
	if _, ok := body["timeoutMs"]; ok {
		t.Fatal("timeoutMs must never be sent")
	}
	if sb.ID != "sbx-1" || sb.State != "running" || sb.TrafficAccessToken != "tok" || sb.Metadata["k"] != "v" || len(sb.Versions) != 1 {
		t.Fatalf("record: %+v", sb.SandboxInfo)
	}
	if got := sb.PortURL(8080); got != "https://8080-sbx-1.sandbox.example.test" {
		t.Fatalf("port url: %s", got)
	}
	_, _ = c.Sandboxes.Create(ctx, CreateOptions{TemplateID: "tpl-9", Template: "ignored"})
	if b := m.lastBody(t); b["templateId"] != "tpl-9" || b["templateName"] != nil {
		t.Fatalf("templateId precedence: %v", b)
	}

	rows, err := c.Sandboxes.List(ctx, "running", "frozen")
	if err != nil || m.last().Query != "state=running%2Cfrozen" || len(rows) != 1 || rows[0].TemplateID != "tpl-base" || rows[0].PortURLFor(80) != "https://80-sbx-1.x" {
		t.Fatalf("list: %v %q %+v", err, m.last().Query, rows)
	}
	_, _ = c.Sandboxes.List(ctx)
	if m.last().Query != "" {
		t.Fatalf("list without state must send no query, got %q", m.last().Query)
	}
	sh, _ := c.Sandboxes.Shared(ctx)
	if len(sh) != 1 || sh[0].State != "frozen" {
		t.Fatalf("shared: %+v", sh)
	}
	got, err := c.Sandboxes.Get(ctx, "sbx-1")
	if err != nil || got.ID != "sbx-1" || got.Commands == nil || got.Files == nil || got.Pty == nil {
		t.Fatalf("get: %v %+v", err, got)
	}
	_, err = c.Sandboxes.Get(ctx, "sbx-killed")
	if !IsNotFound(err) || IsRegionRetired(err) {
		t.Fatalf("killed → NotFoundError: %v", err)
	}

	if err := sb.Freeze(ctx, true); err != nil || sb.State != "frozen" {
		t.Fatalf("freeze 200: %v %s", err, sb.State)
	}
	if err := sb.Resume(ctx, 120); err != nil || sb.State != "running" || m.lastBody(t)["timeout"] != float64(120) {
		t.Fatalf("resume: %v", err)
	}
	_ = sb.Resume(ctx, 0)
	if len(m.lastBody(t)) != 0 {
		t.Fatalf("resume without timeout must send {}: %v", m.lastBody(t))
	}
	if err := sb.Rollback(ctx, 0); err != nil || m.lastBody(t)["version"] != float64(0) {
		t.Fatalf("rollback: %v", err)
	}
	if err := sb.Rollback(ctx, 5); !IsValidation(err) {
		t.Fatalf("rollback bad version → 400 ValidationError: %v", err)
	} else if e := unwrap(err); e.Message != "unknown version" || e.Fields["reason"] != "no_such_version" {
		t.Fatalf("error-shape parsing: %+v", e)
	}
	clone, err := sb.Clone(ctx, 0, map[string]string{"c": "d"})
	if err != nil || clone.ID != "sbx-clone" {
		t.Fatalf("clone: %v", err)
	}
	if n := m.count("POST", "/sandboxes/sbx-1/clone"); n != 3 {
		t.Fatalf("clone must honour Retry-After on 503, got %d attempts", n)
	}
	if err := sb.SetTimeout(ctx, 0); err != nil || m.lastBody(t)["timeout"] != float64(0) {
		t.Fatalf("set timeout 0: %v", err)
	}
	if err := sb.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	ms, err := sb.Metrics(ctx, 10, 20)
	if err != nil || len(ms) != 1 || ms[0].CPUUsedPct != 12.5 || m.last().Query != "end=20&start=10" {
		t.Fatalf("metrics: %v %+v %q", err, ms, m.last().Query)
	}
	g, err := c.Sandboxes.Grant(ctx, "sbx-1", "bob", &GrantOptions{Perm: "write", ExpiresAt: "2027-01-01T00:00:00Z"})
	if err != nil || g.SandboxID != "sbx-1" || g.ExpiresAt != "2027-01-01T00:00:00Z" {
		t.Fatalf("grant: %v %+v", err, g)
	}
	gs, _ := c.Sandboxes.Grants(ctx, "sbx-1")
	if len(gs) != 1 || gs[0].Permission != "write" {
		t.Fatalf("grants: %+v", gs)
	}
	if err := c.Sandboxes.Revoke(ctx, "sbx-1", "bob"); err != nil {
		t.Fatal(err)
	}
	if err := sb.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if err := sb.Kill(ctx); err != nil || m.last().Method != "DELETE" || m.last().Path != "/sandboxes/sbx-1" {
		t.Fatalf("kill: %v", err)
	}
}

func TestFreeze202Polls(t *testing.T) {
	m := newMock(t)
	c := m.client(t)
	start := time.Now()
	info, err := c.Sandboxes.Freeze(ctx, "sbx-freeze", true)
	if err != nil || info.State != "frozen" {
		t.Fatalf("freeze wait: %v %+v", err, info)
	}
	if n := m.count("GET", "/sandboxes/sbx-freeze"); n != 3 {
		t.Fatalf("want 3 polls, got %d", n)
	}
	if time.Since(start) < 4*time.Second {
		t.Fatalf("poll interval should be 2s, took %s", time.Since(start))
	}
	m2 := newMock(t)
	info, err = m2.client(t).Sandboxes.Freeze(ctx, "sbx-freeze", false)
	if err != nil || info.State != "freezing" || info.ID != "sbx-freeze" || m2.count("GET", "/sandboxes/sbx-freeze") != 0 {
		t.Fatalf("freeze no-wait: %v %+v", err, info)
	}
}

func TestFreezeWaitToleratesRunning(t *testing.T) {
	old := freezePollInterval
	freezePollInterval = 10 * time.Millisecond
	t.Cleanup(func() { freezePollInterval = old })

	m := newMock(t)
	m.freezeStates = []string{"running", "freezing", "frozen"}
	info, err := m.client(t).Sandboxes.Freeze(ctx, "sbx-freeze", true)
	if err != nil || info.State != "frozen" {
		t.Fatalf("freeze wait through running: %v %+v", err, info)
	}
	if n := m.count("GET", "/sandboxes/sbx-freeze"); n != 3 {
		t.Fatalf("want 3 polls, got %d", n)
	}

	m2 := newMock(t)
	m2.freezeStates = []string{"freezing", "failed"}
	_, err = m2.client(t).Sandboxes.Freeze(ctx, "sbx-freeze", true)
	var ce *ConflictError
	if !errors.As(err, &ce) || !strings.Contains(ce.Error(), `"failed"`) {
		t.Fatalf("want ConflictError naming failed, got %v", err)
	}
	if n := m2.count("GET", "/sandboxes/sbx-freeze"); n != 2 {
		t.Fatalf("want 2 polls before failure, got %d", n)
	}
}

func TestErrorMapping(t *testing.T) {
	m := newMock(t)
	c := m.client(t)
	_, err := c.Sandboxes.Create(ctx, CreateOptions{Template: "nocredits"})
	var ic *InsufficientCreditsError
	if !errors.As(err, &ic) || !IsInsufficientCredits(err) || ic.Balance() != -3.5 || ic.Code != "sb_box_insufficient_credits" {
		t.Fatalf("402: %v", err)
	}
	_, err = c.Sandboxes.Create(ctx, CreateOptions{Template: "needplan"})
	var pr *PlanRequiredError
	if !errors.As(err, &pr) || !IsPlanRequired(err) || pr.Plan() != "plus" || pr.Upgrade() != "https://x/upgrade" {
		t.Fatalf("403 plan: %v", err)
	}
	_, err = c.Sandboxes.Create(ctx, CreateOptions{Template: "toomany"})
	var le *LimitError
	if !errors.As(err, &le) || !IsLimit(err) || le.Limit() != 1 || le.Current() != 1 || le.Plan() != "free" {
		t.Fatalf("409 limit: %v", err)
	}
	_, err = c.Sandboxes.Resume(ctx, "sbx-expired", 0)
	var me *MemoryExpiredError
	if !errors.As(err, &me) || !IsMemoryExpired(err) || me.Code != "memory_expired" || len(me.Recovery()) != 1 || me.Recovery()[0] != "reset" {
		t.Fatalf("428: %v", err)
	}
	_, err = c.Sandboxes.Reset(ctx, "sbx-lost")
	if !IsDiskLost(err) || Status(err) != 410 {
		t.Fatalf("410: %v", err)
	}
	if e := unwrap(err); e.RequestID != "" && e.Detail == nil {
		t.Fatalf("detail kept: %+v", e)
	}
	if Status(errors.New("plain")) != 0 || IsNotFound(nil) {
		t.Fatal("non-SDK errors classify as nothing")
	}
	if Redact("key gsk-abc.def_1 here") != "key gsk-REDACTED here" {
		t.Fatal("redact")
	}
}

func TestExecAndStream(t *testing.T) {
	m := newMock(t)
	sb, _ := m.client(t).Sandboxes.Get(ctx, "sbx-1")
	res, err := sb.Exec(ctx, []string{"echo", "hi"}, &ExecOptions{Cwd: "/tmp", User: "root", Env: map[string]string{"A": "1"}, Timeout: 5})
	if err != nil || res.ExitCode != 0 || !strings.Contains(res.Stdout, "cwd=/tmp user=root") {
		t.Fatalf("exec: %v %+v", err, res)
	}
	b := m.lastBody(t)
	if b["timeout"] != float64(5) || b["envVars"].(map[string]any)["A"] != "1" || b["command"].([]any)[1] != "hi" {
		t.Fatalf("exec body: %v", b)
	}
	_, _ = sb.Exec(ctx, []string{"echo"}, nil)
	if m.lastBody(t)["timeout"] != float64(60) {
		t.Fatal("default timeout 60")
	}
	res, err = sb.Exec(ctx, []string{"false"}, nil)
	if err != nil || res.ExitCode != 1 || res.Stderr != "nope\n" {
		t.Fatalf("non-zero exit is data: %v %+v", err, res)
	}
	_, err = sb.Exec(ctx, ShellCommand("sleep 999"), &ExecOptions{Timeout: 1})
	var te *CommandTimeoutError
	if !errors.As(err, &te) || !IsCommandTimeout(err) || !te.Killed() || te.Fields["stage"] != "timeout" {
		t.Fatalf("408: %v", err)
	}

	st, err := sb.ExecStream(ctx, []string{"echo", "hi"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	var out string
	exit := -1
	for ev, ok := st.Next(); ok; ev, ok = st.Next() {
		kinds = append(kinds, ev.Kind)
		switch ev.Kind {
		case "execId":
			if ev.ExecID != "e1" {
				t.Fatalf("execId: %+v", ev)
			}
		case "stdout", "stderr":
			out += ev.Kind + ":" + ev.Data + ";"
		case "exit":
			exit = ev.ExitCode
		}
	}
	if st.Err() != nil || strings.Join(kinds, ",") != "execId,stdout,stderr,exit" || out != "stdout:hello\n;stderr:warn;" || exit != 3 {
		t.Fatalf("stream: %v %v %q %d", st.Err(), kinds, out, exit)
	}
	st, _ = sb.ExecStream(ctx, []string{"explode"}, nil)
	var last ExecEvent
	for ev, ok := st.Next(); ok; ev, ok = st.Next() {
		last = ev
	}
	if last.Kind != "error" || last.Data != "spawn failed" || last.Stage != "start" {
		t.Fatalf("error event: %+v", last)
	}
}

func TestRunCode(t *testing.T) {
	m := newMock(t)
	sb, _ := m.client(t).Sandboxes.Get(ctx, "sbx-1")
	r, err := sb.RunCode(ctx, "1+1", nil)
	if err != nil || r.Stdout != "x\n" || r.ExecutionCount != 1 || r.Error != nil || len(r.Results) != 1 || !bytes.Contains(r.Results[0], []byte(`"isMainResult":true`)) {
		t.Fatalf("run-code: %v %+v", err, r)
	}
	b := m.lastBody(t)
	if b["language"] != "python" || b["timeout"] != float64(60) {
		t.Fatalf("defaults: %v", b)
	}
	_, _ = sb.RunCode(ctx, "x", &RunCodeOptions{Language: "js", ContextID: "ctx1", Images: "omit", Timeout: 9})
	b = m.lastBody(t)
	if b["language"] != "js" || b["contextId"] != "ctx1" || b["images"] != "omit" || b["timeout"] != float64(9) {
		t.Fatalf("options: %v", b)
	}
	r, err = sb.RunCode(ctx, "1/0", nil)
	if err != nil || r.Error == nil || r.Error.Name != "ZeroDivisionError" {
		t.Fatalf("interpreter error is data: %v %+v", err, r)
	}
	_, err = sb.RunCode(ctx, "unavailable", nil)
	var iu *InterpreterUnavailableError
	if !errors.As(err, &iu) || iu.Message != "interpreter unavailable" {
		t.Fatalf("409 interpreter: %v", err)
	}
}

func TestBackgroundCommands(t *testing.T) {
	m := newMock(t)
	sb, _ := m.client(t).Sandboxes.Get(ctx, "sbx-1")
	bg, err := sb.Commands.Start(ctx, []string{"cat"}, &StartOptions{Cwd: "/w"})
	if err != nil || bg.ExecID != "bg1" || bg.PID != 4242 || m.lastBody(t)["cwd"] != "/w" {
		t.Fatalf("start: %v %+v", err, bg)
	}
	n, err := bg.Input(ctx, "hello")
	if err != nil || n != 5 {
		t.Fatalf("input: %v %d", err, n)
	}
	if err := bg.Close(ctx); err != nil {
		t.Fatal(err)
	}
	code, err := bg.Wait(ctx)
	if err != nil || code != 7 {
		t.Fatalf("wait: %v %d", err, code)
	}
	st, err := bg.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ev, ok := st.Next()
	if !ok || ev.Kind != "stdout" || ev.Data != "bg out" {
		t.Fatalf("connect: %+v", ev)
	}
	if _, err := sb.Commands.Attach("gone", 1).Connect(ctx); !IsNotFound(err) {
		t.Fatalf("reaped → NotFound: %v", err)
	}
	list, _ := sb.Commands.List(ctx)
	if len(list) != 1 || list[0].PID != 4242 {
		t.Fatalf("list: %+v", list)
	}
	if err := bg.Kill(ctx, 0); err != nil || m.lastBody(t)["signal"] != float64(9) || m.lastBody(t)["pid"] != float64(4242) {
		t.Fatalf("kill default signal: %v", err)
	}
	if err := sb.Commands.Kill(ctx, 4242, 15); err != nil || m.lastBody(t)["signal"] != float64(15) {
		t.Fatalf("kill 15: %v", err)
	}
	if err := sb.Commands.SendStdin(ctx, 4242, "q\n"); err != nil || m.lastBody(t)["data"] != "q\n" {
		t.Fatalf("send-stdin: %v", err)
	}
}

func TestFiles(t *testing.T) {
	m := newMock(t)
	sb, _ := m.client(t).Sandboxes.Get(ctx, "sbx-1")
	data := []byte{0, 1, 2, 0xff, 'h', 'i'}
	n, err := sb.Files.Write(ctx, "/tmp/bin", data, "")
	if err != nil || n != int64(len(data)) || m.last().Query != "path=%2Ftmp%2Fbin" {
		t.Fatalf("write bytes: %v %d %q", err, n, m.last().Query)
	}
	got, err := sb.Files.Read(ctx, "/tmp/bin", "")
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("round trip: %v %v", err, got)
	}
	if _, err := sb.Files.Write(ctx, "/tmp/t.txt", "héllo", "root"); err != nil || !strings.Contains(m.last().Query, "user=root") {
		t.Fatalf("write string: %v", err)
	}
	s, _ := sb.Files.ReadText(ctx, "/tmp/t.txt", "")
	if s != "héllo" {
		t.Fatalf("read text: %q", s)
	}
	if _, err := sb.Files.Write(ctx, "/tmp/r", strings.NewReader("reader"), ""); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if n, err := sb.Files.ReadTo(ctx, "/tmp/r", "", &buf); err != nil || n != 6 || buf.String() != "reader" {
		t.Fatalf("read to: %v %d %q", err, n, buf.String())
	}
	if _, err := sb.Files.Write(ctx, "/x", 42, ""); err == nil {
		t.Fatal("unsupported type must error")
	}
	if _, err := sb.Files.Read(ctx, "/nope", ""); !IsNotFound(err) {
		t.Fatalf("missing file: %v", err)
	}
	ls, err := sb.Files.List(ctx, "", "")
	if err != nil || len(ls) != 2 || !ls[1].IsDir || ls[0].Size != 5 || m.last().Query != "path=%2F" {
		t.Fatalf("list: %v %+v", err, ls)
	}
	ls, err = sb.Files.List(ctx, "/missing", "")
	if err != nil || len(ls) != 0 {
		t.Fatalf("missing dir is empty: %v %+v", err, ls)
	}
}

func TestPty(t *testing.T) {
	m := newMock(t)
	sb, _ := m.client(t).Sandboxes.Get(ctx, "sbx-1")
	p, err := sb.Pty.Open(ctx, nil)
	if err != nil || p.ExecID != "pty1" || p.PID != 99 {
		t.Fatalf("open: %v %+v", err, p)
	}
	b := m.lastBody(t)
	if b["rows"] != float64(24) || b["cols"] != float64(80) || b["command"].([]any)[0] != "/bin/bash" {
		t.Fatalf("defaults: %v", b)
	}
	if err := p.Input(ctx, []byte("ls\n")); err != nil {
		t.Fatalf("input: %v", err)
	}
	if err := p.Resize(ctx, 50, 100); err != nil || m.lastBody(t)["cols"] != float64(100) {
		t.Fatalf("resize: %v", err)
	}
	st, err := p.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ev, _ := st.Next()
	if ev.Data != "$ " {
		t.Fatalf("data: %+v", ev)
	}
	ev, _ = st.Next()
	if !ev.Exit || ev.Reason != "eof" {
		t.Fatalf("exit: %+v", ev)
	}
	if _, ok := st.Next(); ok {
		t.Fatal("stream must end after exit")
	}
	if err := p.Kill(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestPtyWebSocket(t *testing.T) {
	m := newMock(t)
	sb, _ := m.client(t).Sandboxes.Get(ctx, "sbx-1")
	ws, err := sb.Pty.ConnectWS(ctx, &PtyOptions{Rows: 40, Cols: 120})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	ev, ok := ws.Next() // ping is answered transparently first
	if !ok || ev.Data != "$ " {
		t.Fatalf("first: %+v %v", ev, ok)
	}
	ev, _ = ws.Next()
	if len(ev.Data) != 300 {
		t.Fatalf("large frame: %d", len(ev.Data))
	}
	if err := ws.Input("echo hi\n"); err != nil {
		t.Fatal(err)
	}
	ev, _ = ws.Next()
	if ev.Data != "echo hi\n" {
		t.Fatalf("echo: %+v", ev)
	}
	if err := ws.Resize(10, 20); err != nil {
		t.Fatal(err)
	}
	if ev, _ = ws.Next(); ev.Data != "resized" {
		t.Fatalf("resize: %+v", ev)
	}
	if err := ws.Kill(); err != nil {
		t.Fatal(err)
	}
	ev, ok = ws.Next()
	if !ok || !ev.Exit || ev.ExitCode != 130 {
		t.Fatalf("exit: %+v %v", ev, ok)
	}
	if _, ok := ws.Next(); ok {
		t.Fatal("closed after exit")
	}
	bad := m.client(t, WithAPIKey("gsk-nope"))
	sbBad, _ := bad.Sandboxes.Get(ctx, "sbx-1")
	if sbBad != nil {
		t.Fatal("unexpected")
	}
	sbBad = bad.Sandboxes.handle(&SandboxInfo{ID: "sbx-1"})
	if _, err := sbBad.Pty.ConnectWS(ctx, nil); !IsAuthentication(err) {
		t.Fatalf("ws 401: %v", err)
	}
}

func TestSSEParser(t *testing.T) {
	r := newSSEReader(nopCloser{strings.NewReader("event: a\ndata: {\"x\":1}\ndata: {\"y\":2}\nid: 7\n\n: keepalive\n\ndata:bare\n")})
	ev, err := r.next()
	if err != nil || ev.Event != "a" || ev.Data != "{\"x\":1}\n{\"y\":2}" || ev.ID != "7" {
		t.Fatalf("multi-line: %+v %v", ev, err)
	}
	ev, err = r.next()
	if err != nil || ev.Data != "bare" || ev.Event != "" {
		t.Fatalf("eof-terminated: %+v %v", ev, err)
	}
	if _, err := r.next(); err == nil {
		t.Fatal("want EOF")
	}
}

type nopCloser struct{ *strings.Reader }

func (nopCloser) Close() error { return nil }

func TestMetadataValidation(t *testing.T) {
	m := newMock(t)
	_, err := m.client(t).Sandboxes.Create(ctx, CreateOptions{Metadata: map[string]string{" ": "v"}})
	if err == nil || m.count("POST", "/sandboxes") != 0 {
		t.Fatalf("empty metadata key must fail client-side: %v", err)
	}
}
