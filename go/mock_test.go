package sbbox

import (
	"bufio"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const testKey = "gsk-test-key-abc123"

// mockServer implements the slice of the contract the tests need. Every
// handler asserts the API key header; requests are recorded for assertions.
type mockServer struct {
	t      *testing.T
	srv    *httptest.Server
	mu     sync.Mutex
	reqs   []recordedReq
	files  map[string][]byte
	freeze int // GET /sandboxes/sbx-freeze polls before "frozen"
	// When set, GET /sandboxes/sbx-freeze answers these states in order
	// (the last one repeats) instead of the freeze countdown.
	freezeStates []string
	clone        int // 503s before clone succeeds
	build        int // GETs before template ready
	fail         int // 503s before /regions succeeds
}

type recordedReq struct {
	Method, Path, Query string
	Body                []byte
	Header              http.Header
}

func sandboxJSON(id, state string) map[string]any {
	return map[string]any{
		"id": id, "templateId": "tpl-base", "state": state, "region": "westus2",
		"portUrl": "https://{port}-" + id + ".sandbox.example.test", "domain": "example.test",
		"createdAt": "2026-11-04T00:00:00Z", "startedAt": "2026-11-04T00:00:01Z",
		"cpuCount": 1, "memoryMB": 2048, "diskSizeGB": 20, "idleTimeout": 600,
		"versions": []map[string]any{{"version": 1, "frozenAt": "2026-11-04T01:00:00Z", "label": "v1"}},
		"metadata": map[string]string{"cogenId": "u1", "k": "v"},
	}
}

func newMock(t *testing.T) *mockServer {
	m := &mockServer{t: t, files: map[string][]byte{}, freeze: 2, clone: 2, build: 2, fail: 1}
	mux := http.NewServeMux()
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		m.mu.Lock()
		m.reqs = append(m.reqs, recordedReq{r.Method, r.URL.Path, r.URL.RawQuery, body, r.Header.Clone()})
		m.mu.Unlock()
		if r.Header.Get("X-Api-Key") != testKey && !strings.HasSuffix(r.URL.Path, "/ws/pty") {
			writeJSON(w, 401, map[string]any{"detail": "Invalid API key"})
			return
		}
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "sbbox-go-sdk/"+Version) {
			writeJSON(w, 400, map[string]any{"detail": "bad user agent"})
			return
		}
		m.route(w, r, body)
	}))
	_ = mux
	t.Cleanup(m.srv.Close)
	return m
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func sse(w http.ResponseWriter, events ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(200)
	fl, _ := w.(http.Flusher)
	for _, e := range events {
		_, _ = io.WriteString(w, e)
		if fl != nil {
			fl.Flush()
		}
	}
}

func (m *mockServer) route(w http.ResponseWriter, r *http.Request, body []byte) {
	p := r.URL.Path
	var in map[string]any
	_ = json.Unmarshal(body, &in)
	switch {
	case p == "/regions":
		m.mu.Lock()
		f := m.fail
		m.fail--
		m.mu.Unlock()
		if f > 0 {
			writeJSON(w, 503, map[string]any{"detail": "warming up"})
			return
		}
		writeJSON(w, 200, map[string]any{"regions": []string{"westus2", "swedencentral"}, "default": "westus2"})
	case p == "/limits":
		writeJSON(w, 200, map[string]any{"plan": "plus", "planRequired": false,
			"limits": map[string]int{"maxConcurrentSandboxes": 20, "maxCpu": 4, "maxMemoryMB": 8192, "maxDiskGB": 50, "maxConcurrentBuilds": 2},
			"usage":  map[string]int{"runningSandboxes": 1, "buildingTemplates": 0}})
	case p == "/usage":
		writeJSON(w, 200, map[string]any{"days": 7, "from": "20261028", "to": "20261104", "rateCard": map[string]any{"cpu": 0.1},
			"direct": map[string]any{"runSec": 60, "cpuCoreSec": 60, "memGBSec": 120, "diskGBSec": 1200, "credits": 1.5},
			"daily":  []map[string]any{{"day": "20261104", "direct": map[string]any{"run_sec": 60, "credits": 1.5}}}})
	case p == "/templates/build":
		if in["image"] == nil && in["dockerfile"] == nil {
			writeJSON(w, 422, map[string]any{"status": -2, "message": "image or dockerfile required", "errors": []any{map[string]any{"loc": []any{"body", "image"}, "msg": "required"}}})
			return
		}
		if in["cpuCount"] == float64(99) {
			writeJSON(w, 409, map[string]any{"detail": map[string]any{"code": "sb_box_spec_limit", "plan": "plus", "limit": 4, "requested": 99, "message": "cpu over limit"}})
			return
		}
		writeJSON(w, 200, map[string]any{"templateId": "tpl-new", "status": "building", "region": "westus2", "owner": "u1"})
	case p == "/templates":
		writeJSON(w, 200, map[string]any{"templates": []map[string]any{{"id": "tpl-1", "name": "py", "namespace": "private", "status": "ready", "is_owner": true, "can_create": true}}})
	case p == "/templates/shared":
		writeJSON(w, 200, []map[string]any{{"id": "tpl-s", "name": "shared", "namespace": "private", "status": "ready", "can_create": true}})
	case p == "/templates/tpl-new":
		m.mu.Lock()
		b := m.build
		m.build--
		m.mu.Unlock()
		st := "building"
		if b <= 0 {
			st = "ready"
		}
		writeJSON(w, 200, map[string]any{"id": "tpl-new", "name": "n", "namespace": "private", "status": st, "cpuCount": 2})
	case p == "/templates/tpl-bad":
		writeJSON(w, 200, map[string]any{"id": "tpl-bad", "status": "error", "buildError": "pull failed for gsk-secret-in-log"})
	case p == "/templates/tpl-1" && r.Method == "DELETE":
		writeJSON(w, 200, map[string]any{"status": "deleted"})
	case p == "/templates/tpl-1/grants/bob" && r.Method == "PUT":
		writeJSON(w, 200, map[string]any{"template_id": "tpl-1", "grantee_id": "bob", "permission": in["perm"], "granted_at": "t"})
	case p == "/templates/tpl-1/grants" && r.Method == "GET":
		writeJSON(w, 200, []map[string]any{{"grantee_id": "bob", "permission": "read", "granted_at": "t"}})
	case p == "/templates/tpl-1/grants/bob" && r.Method == "DELETE":
		writeJSON(w, 200, map[string]any{"status": "revoked"})
	case p == "/templates/tpl-gone":
		writeJSON(w, 404, map[string]any{"detail": map[string]any{"code": "sb_box_region_retired", "region": "eastus", "message": "region retired"}})

	case p == "/sandboxes" && r.Method == "POST":
		switch {
		case in["templateName"] == "nocredits":
			writeJSON(w, 402, map[string]any{"detail": map[string]any{"code": "sb_box_insufficient_credits", "balance": -3.5, "message": "insufficient credits"}})
		case in["templateName"] == "needplan":
			writeJSON(w, 403, map[string]any{"detail": map[string]any{"code": "sb_box_plan_required", "plan": "plus", "upgrade": "https://x/upgrade", "message": "plan required"}})
		case in["templateName"] == "toomany":
			writeJSON(w, 409, map[string]any{"detail": map[string]any{"code": "sb_box_concurrency_limit", "plan": "free", "limit": 1, "current": 1, "message": "too many"}})
		case in["templateName"] == "boom":
			writeJSON(w, 500, map[string]any{"detail": "internal failure key=gsk-leaked-key-xyz"})
		default:
			rec := sandboxJSON("sbx-1", "running")
			rec["trafficAccessToken"] = "tok"
			writeJSON(w, 200, rec)
		}
	case p == "/sandboxes" && r.Method == "GET":
		writeJSON(w, 200, map[string]any{"sandboxes": []map[string]any{{"id": "sbx-1", "template_id": "tpl-base", "state": "running", "portUrl": "https://{port}-sbx-1.x"}}, "hasMore": false})
	case p == "/sandboxes/shared":
		writeJSON(w, 200, []map[string]any{sandboxJSON("sbx-sh", "frozen")})
	case p == "/sandboxes/sbx-killed":
		writeJSON(w, 404, map[string]any{"detail": "sandbox not found"})
	case p == "/sandboxes/sbx-freeze" && r.Method == "GET":
		m.mu.Lock()
		f := m.freeze
		m.freeze--
		st := "freezing"
		if f <= 0 {
			st = "frozen"
		}
		if len(m.freezeStates) > 0 {
			st = m.freezeStates[0]
			if len(m.freezeStates) > 1 {
				m.freezeStates = m.freezeStates[1:]
			}
		}
		m.mu.Unlock()
		writeJSON(w, 200, sandboxJSON("sbx-freeze", st))
	case p == "/sandboxes/sbx-freeze/freeze":
		writeJSON(w, 202, map[string]any{"id": "sbx-freeze", "state": "freezing"})
	case p == "/sandboxes/sbx-1/freeze":
		writeJSON(w, 200, sandboxJSON("sbx-1", "frozen"))
	case p == "/sandboxes/sbx-1" && r.Method == "GET":
		writeJSON(w, 200, sandboxJSON("sbx-1", "running"))
	case p == "/sandboxes/sbx-1" && r.Method == "DELETE":
		writeJSON(w, 200, map[string]any{"status": "killed"})
	case p == "/sandboxes/sbx-1/resume":
		writeJSON(w, 200, sandboxJSON("sbx-1", "running"))
	case p == "/sandboxes/sbx-expired/resume":
		writeJSON(w, 428, map[string]any{"reason": "memory_expired", "recovery": []string{"reset"}})
	case p == "/sandboxes/sbx-lost/reset":
		writeJSON(w, 410, map[string]any{"detail": map[string]any{"code": "disk_lost", "message": "disk lost", "recovery": []string{"rollback"}, "resumable": false}})
	case p == "/sandboxes/sbx-1/rollback":
		if in["version"] != float64(0) {
			writeJSON(w, 400, map[string]any{"error": "unknown version", "reason": "no_such_version", "resumable": true})
			return
		}
		writeJSON(w, 200, sandboxJSON("sbx-1", "running"))
	case p == "/sandboxes/sbx-1/clone":
		m.mu.Lock()
		c := m.clone
		m.clone--
		m.mu.Unlock()
		if c > 0 {
			w.Header().Set("Retry-After", "0")
			writeJSON(w, 503, map[string]any{"detail": "clone still starting"})
			return
		}
		writeJSON(w, 200, sandboxJSON("sbx-clone", "running"))
	case p == "/sandboxes/sbx-1/timeout", p == "/sandboxes/sbx-1/reset":
		if p == "/sandboxes/sbx-1/reset" {
			writeJSON(w, 200, sandboxJSON("sbx-1", "running"))
			return
		}
		writeJSON(w, 200, map[string]any{"status": "ok"})
	case p == "/sandboxes/sbx-1/metrics":
		writeJSON(w, 200, []map[string]any{{"timestamp": "t", "timestampUnix": 1, "cpuCount": 1, "cpuUsedPct": 12.5, "memUsed": 100, "memTotal": 2048}})
	case p == "/sandboxes/sbx-1/grants/bob" && r.Method == "PUT":
		writeJSON(w, 200, map[string]any{"sandbox_id": "sbx-1", "grantee_id": "bob", "permission": in["perm"], "granted_at": "t", "expires_at": in["expires_at"]})
	case p == "/sandboxes/sbx-1/grants":
		writeJSON(w, 200, []map[string]any{{"grantee_id": "bob", "permission": "write", "granted_at": "t"}})
	case p == "/sandboxes/sbx-1/grants/bob" && r.Method == "DELETE":
		writeJSON(w, 200, map[string]any{"status": "revoked"})

	case p == "/sandboxes/sbx-1/exec":
		cmd, _ := in["command"].([]any)
		if len(cmd) == 3 && cmd[2] == "sleep 999" {
			writeJSON(w, 408, map[string]any{"error": "command timed out after 1s", "stage": "timeout", "killed": true, "sandbox_id": "sbx-1"})
			return
		}
		if len(cmd) > 0 && cmd[0] == "false" {
			writeJSON(w, 200, map[string]any{"stdout": "", "stderr": "nope\n", "exitCode": 1})
			return
		}
		writeJSON(w, 200, map[string]any{"stdout": fmt.Sprintf("%v cwd=%v user=%v\n", cmd, in["cwd"], in["user"]), "stderr": "", "exitCode": 0})
	case p == "/sandboxes/sbx-1/sse/exec":
		if in["command"].([]any)[0] == "explode" {
			sse(w, "event: execId\ndata: {\"execId\":\"e9\"}\n\n", "event: error\ndata: {\"error\":\"spawn failed\",\"stage\":\"start\"}\n\n")
			return
		}
		sse(w, ": comment\n\n", "event: execId\ndata: {\"execId\":\"e1\"}\n\n", "id: 1\nevent: stdout\ndata: {\"data\":\"hello\\n\"}\n\n",
			"id: 2\nevent: stderr\ndata: {\"data\":\"warn\"}\n\n", "event: exit\ndata: {\"exitCode\":3}\n\n")
	case p == "/sandboxes/sbx-1/run-code":
		if in["code"] == "1/0" {
			writeJSON(w, 200, map[string]any{"stdout": "", "stderr": "", "executionCount": 2, "complete": true,
				"error": map[string]any{"name": "ZeroDivisionError", "value": "division by zero", "traceback": "tb"}, "results": []any{}})
			return
		}
		if in["code"] == "unavailable" {
			writeJSON(w, 409, map[string]any{"error": "interpreter unavailable", "code": "sb_box_interpreter_unavailable"})
			return
		}
		writeJSON(w, 200, map[string]any{"stdout": "x\n", "stderr": "", "executionCount": 1, "complete": true, "error": nil,
			"results": []map[string]any{{"text": "2", "isMainResult": true}}})
	case p == "/sandboxes/sbx-1/commands/start":
		writeJSON(w, 200, map[string]any{"execId": "bg1", "pid": 4242})
	case p == "/sandboxes/sbx-1/commands" && r.Method == "GET":
		writeJSON(w, 200, []map[string]any{{"execId": "bg1", "pid": 4242, "sandboxId": "sbx-1"}})
	case p == "/sandboxes/sbx-1/commands/bg1/input":
		writeJSON(w, 200, map[string]any{"written": len(in["data"].(string))})
	case p == "/sandboxes/sbx-1/commands/bg1/close", p == "/sandboxes/sbx-1/commands/kill", p == "/sandboxes/sbx-1/commands/send-stdin":
		writeJSON(w, 200, map[string]any{"status": "ok"})
	case p == "/sandboxes/sbx-1/commands/bg1/wait":
		writeJSON(w, 200, map[string]any{"exitCode": 7})
	case p == "/sandboxes/sbx-1/commands/bg1/connect":
		sse(w, "event: stdout\ndata: {\"data\":\"bg out\"}\n\n", "event: exit\ndata: {\"exitCode\":0}\n\n")
	case p == "/sandboxes/sbx-1/commands/gone/connect":
		writeJSON(w, 404, map[string]any{"detail": "command not found"})

	case p == "/sandboxes/sbx-1/files/stream" && r.Method == "POST":
		if r.Header.Get("Content-Type") != "application/octet-stream" {
			writeJSON(w, 400, map[string]any{"detail": "expected octet-stream"})
			return
		}
		m.mu.Lock()
		m.files[r.URL.Query().Get("path")] = body
		m.mu.Unlock()
		writeJSON(w, 200, map[string]any{"written": len(body)})
	case p == "/sandboxes/sbx-1/files/stream" && r.Method == "GET":
		m.mu.Lock()
		b, ok := m.files[r.URL.Query().Get("path")]
		m.mu.Unlock()
		if !ok {
			writeJSON(w, 404, map[string]any{"detail": "file not found"})
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(200)
		_, _ = w.Write(b)
	case p == "/sandboxes/sbx-1/files/list":
		if r.URL.Query().Get("path") == "/missing" {
			writeJSON(w, 200, []any{})
			return
		}
		writeJSON(w, 200, []map[string]any{{"name": "a.txt", "path": "/tmp/a.txt", "isDir": false, "size": 5}, {"name": "d", "path": "/tmp/d", "isDir": true, "size": 0}})

	case p == "/sandboxes/sbx-1/pty" && r.Method == "POST":
		writeJSON(w, 200, map[string]any{"execId": "pty1", "pid": 99})
	case p == "/sandboxes/sbx-1/pty/pty1/input":
		if r.Header.Get("Content-Type") != "application/octet-stream" || string(body) != "ls\n" {
			writeJSON(w, 400, map[string]any{"detail": "bad pty input"})
			return
		}
		writeJSON(w, 200, map[string]any{"status": "ok"})
	case p == "/sandboxes/sbx-1/pty/pty1/resize", p == "/sandboxes/sbx-1/pty/pty1/kill":
		writeJSON(w, 200, map[string]any{"status": "ok"})
	case p == "/sandboxes/sbx-1/pty/pty1/connect":
		sse(w, "event: data\ndata: {\"data\":\"$ \"}\n\n", "event: exit\ndata: {\"reason\":\"eof\",\"exitCode\":0}\n\n")
	case p == "/sandboxes/sbx-1/ws/pty":
		m.wsPty(w, r)
	default:
		writeJSON(w, 404, map[string]any{"detail": "no route " + r.Method + " " + p})
	}
}

// wsPty is a tiny RFC 6455 server: handshake, read masked client frames,
// echo stdin as stdout, send exit on kill.
func (m *mockServer) wsPty(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Api-Key") != testKey {
		writeJSON(w, 401, map[string]any{"detail": "Invalid API key"})
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		m.t.Fatal("no hijack")
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		m.t.Fatal(err)
	}
	defer conn.Close()
	accept := wsAccept(r.Header.Get("Sec-WebSocket-Key"))
	fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", accept)
	_ = rw.Flush()
	send := func(op byte, payload []byte) {
		hdr := []byte{0x80 | op}
		n := len(payload)
		switch {
		case n < 126:
			hdr = append(hdr, byte(n))
		case n < 1<<16:
			hdr = append(hdr, 126, byte(n>>8), byte(n))
		default:
			hdr = append(hdr, 127)
			hdr = binary.BigEndian.AppendUint64(hdr, uint64(n))
		}
		_, _ = rw.Write(append(hdr, payload...))
		_ = rw.Flush()
	}
	first := true
	for {
		op, payload, err := readServerFrame(rw.Reader)
		if err != nil {
			return
		}
		switch op {
		case opClose:
			send(opClose, payload)
			return
		case opPing:
			send(opPong, payload)
			continue
		case opPong:
			continue
		}
		var f map[string]any
		_ = json.Unmarshal(payload, &f)
		if first {
			first = false
			if f["rows"] != float64(40) {
				send(opText, []byte(`{"type":"error","error":"first frame missing rows"}`))
				return
			}
			send(opPing, []byte("hi"))
			send(opText, []byte(`{"type":"stdout","data":"$ ","seq":1}`))
			// a large frame to exercise the 126 length path
			big, _ := json.Marshal(map[string]any{"type": "stdout", "data": strings.Repeat("x", 300), "seq": 2})
			send(opText, big)
			continue
		}
		switch f["type"] {
		case "stdin":
			out, _ := json.Marshal(map[string]any{"type": "stdout", "data": f["data"], "seq": 3})
			send(opText, out)
		case "resize":
			send(opText, []byte(`{"type":"stdout","data":"resized","seq":4}`))
		case "kill":
			send(opText, []byte(`{"type":"exit","exitCode":130}`))
			return
		}
	}
}

func readServerFrame(br *bufio.Reader) (byte, []byte, error) {
	h := make([]byte, 2)
	if _, err := io.ReadFull(br, h); err != nil {
		return 0, nil, err
	}
	op := h[0] & 0x0F
	if h[1]&0x80 == 0 {
		return 0, nil, fmt.Errorf("client frame not masked")
	}
	n := uint64(h[1] & 0x7F)
	switch n {
	case 126:
		b := make([]byte, 2)
		if _, err := io.ReadFull(br, b); err != nil {
			return 0, nil, err
		}
		n = uint64(binary.BigEndian.Uint16(b))
	case 127:
		b := make([]byte, 8)
		if _, err := io.ReadFull(br, b); err != nil {
			return 0, nil, err
		}
		n = binary.BigEndian.Uint64(b)
	}
	mask := make([]byte, 4)
	if _, err := io.ReadFull(br, mask); err != nil {
		return 0, nil, err
	}
	p := make([]byte, n)
	if _, err := io.ReadFull(br, p); err != nil {
		return 0, nil, err
	}
	for i := range p {
		p[i] ^= mask[i%4]
	}
	return op, p, nil
}

var _ = base64.StdEncoding

func (m *mockServer) client(t *testing.T, opts ...Option) *Client {
	t.Helper()
	all := append([]Option{WithAPIKey(testKey), WithBaseURL(m.srv.URL)}, opts...)
	c, err := New(all...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (m *mockServer) last() recordedReq {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reqs[len(m.reqs)-1]
}

func (m *mockServer) count(method, path string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, r := range m.reqs {
		if r.Method == method && r.Path == path {
			n++
		}
	}
	return n
}

func (m *mockServer) lastBody(t *testing.T) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(m.last().Body, &v); err != nil {
		t.Fatalf("last body not JSON: %v (%q)", err, m.last().Body)
	}
	return v
}
