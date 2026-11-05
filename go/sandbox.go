package sbbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Sandbox is a handle bound to a client. Its embedded SandboxInfo is the
// record as of the last call that returned one (Refresh updates it).
type Sandbox struct {
	SandboxInfo
	c        *Client
	Commands *Commands
	Files    *Files
	Pty      *Pty
}

func readJSON(resp *http.Response, out any) error {
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return &TransportError{Err: err}
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return nil
	}
	return decode(b, out)
}

func (sb *Sandbox) svc() *SandboxesService { return sb.c.Sandboxes }

// Refresh reloads the record.
func (sb *Sandbox) Refresh(ctx context.Context) error {
	cur, err := sb.svc().Get(ctx, sb.ID)
	if err != nil {
		return err
	}
	sb.SandboxInfo = cur.SandboxInfo
	return nil
}

func (sb *Sandbox) adopt(info *SandboxInfo, err error) error {
	if err != nil {
		return err
	}
	if info != nil && info.ID != "" {
		sb.SandboxInfo = *info
	} else if info != nil && info.State != "" {
		sb.State = info.State
	}
	return nil
}

// Kill deletes the sandbox.
func (sb *Sandbox) Kill(ctx context.Context) error { return sb.svc().Kill(ctx, sb.ID) }

// Freeze snapshots the sandbox (see SandboxesService.Freeze).
func (sb *Sandbox) Freeze(ctx context.Context, wait bool) error {
	return sb.adopt(sb.svc().Freeze(ctx, sb.ID, wait))
}

// Resume brings the sandbox back (timeout seconds, 0 = unchanged).
func (sb *Sandbox) Resume(ctx context.Context, timeout int) error {
	return sb.adopt(sb.svc().Resume(ctx, sb.ID, timeout))
}

// Rollback restores a save point (0 = newest).
func (sb *Sandbox) Rollback(ctx context.Context, version int) error {
	return sb.adopt(sb.svc().Rollback(ctx, sb.ID, version, 0))
}

// Clone creates a new sandbox from this one's save point.
func (sb *Sandbox) Clone(ctx context.Context, version int, metadata map[string]string) (*Sandbox, error) {
	return sb.svc().Clone(ctx, sb.ID, version, metadata)
}

// SetTimeout sets the lifetime deadline in seconds (0 clears it).
func (sb *Sandbox) SetTimeout(ctx context.Context, seconds int) error {
	return sb.svc().SetTimeout(ctx, sb.ID, seconds)
}

// Reset reboots the sandbox from its disk.
func (sb *Sandbox) Reset(ctx context.Context) error { return sb.adopt(sb.svc().Reset(ctx, sb.ID)) }

// Metrics returns samples (unix seconds; 0 = server default).
func (sb *Sandbox) Metrics(ctx context.Context, start, end int64) ([]Metric, error) {
	return sb.svc().Metrics(ctx, sb.ID, start, end)
}

// PortURL returns the public URL of a guest port.
func (sb *Sandbox) PortURL(port int) string { return sb.PortURLFor(port) }

// ShellCommand wraps a shell string as an argv: ["/bin/sh", "-c", s].
func ShellCommand(s string) []string { return []string{"/bin/sh", "-c", s} }

func execBody(command []string, opts *ExecOptions) (map[string]any, int) {
	body := map[string]any{"command": command}
	timeout := 60
	if opts != nil {
		if opts.Cwd != "" {
			body["cwd"] = opts.Cwd
		}
		if len(opts.Env) > 0 {
			body["envVars"] = opts.Env
		}
		if opts.User != "" {
			body["user"] = opts.User
		}
		if opts.Timeout > 0 {
			timeout = opts.Timeout
		}
	}
	body["timeout"] = timeout
	return body, timeout
}

// Exec runs a command to completion. A non-zero exit is returned in the
// result, not as an error; only a server-side timeout is an error (408).
func (sb *Sandbox) Exec(ctx context.Context, command []string, opts *ExecOptions) (*CommandResult, error) {
	body, timeout := execBody(command, opts)
	var out CommandResult
	err := sb.c.doJSON(ctx, &requestSpec{method: http.MethodPost, path: sbPath(sb.ID, "/exec"), json: body, timeout: time.Duration(timeout+30) * time.Second}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ExecStream runs a command and streams its output as events.
func (sb *Sandbox) ExecStream(ctx context.Context, command []string, opts *ExecOptions) (*ExecStream, error) {
	body, timeout := execBody(command, opts)
	resp, err := sb.c.do(ctx, &requestSpec{method: http.MethodPost, path: sbPath(sb.ID, "/sse/exec"), json: body, timeout: time.Duration(timeout+30) * time.Second, accept: "text/event-stream"})
	if err != nil {
		return nil, err
	}
	return &ExecStream{sse: newSSEReader(resp.Body)}, nil
}

// RunCode runs code in the sandbox's stateful interpreter (public base only).
func (sb *Sandbox) RunCode(ctx context.Context, code string, opts *RunCodeOptions) (*CodeResult, error) {
	body := map[string]any{"code": code, "language": "python"}
	timeout := 60
	if opts != nil {
		if opts.Language != "" {
			body["language"] = opts.Language
		}
		if opts.ContextID != "" {
			body["contextId"] = opts.ContextID
		}
		if len(opts.Env) > 0 {
			body["envVars"] = opts.Env
		}
		if opts.Timeout > 0 {
			timeout = opts.Timeout
		}
		if opts.Images != "" {
			body["images"] = opts.Images
		}
	}
	body["timeout"] = timeout
	var out CodeResult
	err := sb.c.doJSON(ctx, &requestSpec{method: http.MethodPost, path: sbPath(sb.ID, "/run-code"), json: body, timeout: time.Duration(timeout+30) * time.Second}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// Commands covers background commands.
type Commands struct{ sb *Sandbox }

// BackgroundCommand is a command started with Commands.Start.
type BackgroundCommand struct {
	ExecID string `json:"execId"`
	PID    int    `json:"pid"`
	sb     *Sandbox
}

func startBody(command []string, opts *StartOptions) map[string]any {
	body := map[string]any{"command": command}
	if opts != nil {
		if opts.Cwd != "" {
			body["cwd"] = opts.Cwd
		}
		if len(opts.Env) > 0 {
			body["envVars"] = opts.Env
		}
		if opts.User != "" {
			body["user"] = opts.User
		}
	}
	return body
}

// Start launches a command in the background.
func (c *Commands) Start(ctx context.Context, command []string, opts *StartOptions) (*BackgroundCommand, error) {
	var out BackgroundCommand
	if err := c.sb.c.doJSON(ctx, &requestSpec{method: http.MethodPost, path: sbPath(c.sb.ID, "/commands/start"), json: startBody(command, opts)}, &out); err != nil {
		return nil, err
	}
	out.sb = c.sb
	return &out, nil
}

// List returns the running background commands.
func (c *Commands) List(ctx context.Context) ([]ProcessInfo, error) {
	var out []ProcessInfo
	err := c.sb.c.doJSON(ctx, &requestSpec{method: http.MethodGet, path: sbPath(c.sb.ID, "/commands"), retry: true}, &out)
	return out, err
}

// Kill sends a signal to a pid (signal 0 = 9).
func (c *Commands) Kill(ctx context.Context, pid, signal int) error {
	if signal == 0 {
		signal = 9
	}
	return c.sb.c.doJSON(ctx, &requestSpec{method: http.MethodPost, path: sbPath(c.sb.ID, "/commands/kill"), json: map[string]int{"pid": pid, "signal": signal}}, nil)
}

// SendStdin writes to a pid's stdin.
func (c *Commands) SendStdin(ctx context.Context, pid int, data string) error {
	return c.sb.c.doJSON(ctx, &requestSpec{method: http.MethodPost, path: sbPath(c.sb.ID, "/commands/send-stdin"), json: map[string]any{"pid": pid, "data": data}}, nil)
}

// Attach rebinds a command obtained elsewhere (e.g. from List) to this sandbox.
func (c *Commands) Attach(execID string, pid int) *BackgroundCommand {
	return &BackgroundCommand{ExecID: execID, PID: pid, sb: c.sb}
}

func (b *BackgroundCommand) path(rest string) string {
	return sbPath(b.sb.ID, "/commands/", url.PathEscape(b.ExecID), rest)
}

// Input writes to the command's stdin; returns the byte count written.
func (b *BackgroundCommand) Input(ctx context.Context, data string) (int, error) {
	var out struct {
		Written int `json:"written"`
	}
	err := b.sb.c.doJSON(ctx, &requestSpec{method: http.MethodPost, path: b.path("/input"), json: map[string]string{"data": data}}, &out)
	return out.Written, err
}

// Close closes the command's stdin.
func (b *BackgroundCommand) Close(ctx context.Context) error {
	return b.sb.c.doJSON(ctx, &requestSpec{method: http.MethodPost, path: b.path("/close")}, nil)
}

// Wait blocks until the command exits and returns its exit code.
func (b *BackgroundCommand) Wait(ctx context.Context) (int, error) {
	var out struct {
		ExitCode int `json:"exitCode"`
	}
	err := b.sb.c.doJSON(ctx, &requestSpec{method: http.MethodPost, path: b.path("/wait"), timeout: -1}, &out)
	return out.ExitCode, err
}

// Kill signals the command (signal 0 = 9).
func (b *BackgroundCommand) Kill(ctx context.Context, signal int) error {
	return b.sb.Commands.Kill(ctx, b.PID, signal)
}

// Connect streams the command's output (SSE). A NotFoundError means the command
// was already reaped.
//
// Experimental: not part of the documented SB-Box contract (the user guide states
// background-command output is not streamed); the endpoint may change or disappear
// without notice.
func (b *BackgroundCommand) Connect(ctx context.Context) (*ExecStream, error) {
	resp, err := b.sb.c.do(ctx, &requestSpec{method: http.MethodGet, path: b.path("/connect"), timeout: -1, accept: "text/event-stream"})
	if err != nil {
		return nil, err
	}
	return &ExecStream{sse: newSSEReader(resp.Body)}, nil
}

// Files covers the sandbox filesystem.
type Files struct{ sb *Sandbox }

func fileQuery(path, user string) url.Values {
	q := url.Values{"path": {path}}
	if user != "" {
		q.Set("user", user)
	}
	return q
}

// Write stores data (an io.Reader or []byte / string) at path; returns the
// byte count written. user "" = the sandbox default user.
func (f *Files) Write(ctx context.Context, path string, data any, user string) (int64, error) {
	var r io.Reader
	switch d := data.(type) {
	case io.Reader:
		r = d
	case []byte:
		r = bytes.NewReader(d)
	case string:
		r = bytes.NewReader([]byte(d))
	default:
		return 0, fmt.Errorf("sbbox: Files.Write: unsupported data type %T", data)
	}
	var out struct {
		Written int64 `json:"written"`
	}
	err := f.sb.c.doJSON(ctx, &requestSpec{method: http.MethodPost, path: sbPath(f.sb.ID, "/files/stream"), query: fileQuery(path, user), raw: r, timeout: 10 * time.Minute}, &out)
	return out.Written, err
}

// Open returns the file's body as a stream; the caller closes it.
func (f *Files) Open(ctx context.Context, path, user string) (io.ReadCloser, error) {
	resp, err := f.sb.c.do(ctx, &requestSpec{method: http.MethodGet, path: sbPath(f.sb.ID, "/files/stream"), query: fileQuery(path, user), retry: true, timeout: 10 * time.Minute, accept: "*/*"})
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// Read returns the file's bytes.
func (f *Files) Read(ctx context.Context, path, user string) ([]byte, error) {
	rc, err := f.Open(ctx, path, user)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		return nil, &TransportError{Err: err}
	}
	return b, nil
}

// ReadText returns the file as a UTF-8 string.
func (f *Files) ReadText(ctx context.Context, path, user string) (string, error) {
	b, err := f.Read(ctx, path, user)
	return string(b), err
}

// ReadTo copies the file into w and returns the byte count.
func (f *Files) ReadTo(ctx context.Context, path, user string, w io.Writer) (int64, error) {
	rc, err := f.Open(ctx, path, user)
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	n, err := io.Copy(w, rc)
	if err != nil {
		return n, &TransportError{Err: err}
	}
	return n, nil
}

// List returns a directory's entries ("" = "/"; missing dir = empty).
func (f *Files) List(ctx context.Context, path, user string) ([]FileEntry, error) {
	if path == "" {
		path = "/"
	}
	var out []FileEntry
	err := f.sb.c.doJSON(ctx, &requestSpec{method: http.MethodGet, path: sbPath(f.sb.ID, "/files/list"), query: fileQuery(path, user), retry: true}, &out)
	return out, err
}

// Pty covers interactive terminals.
type Pty struct{ sb *Sandbox }

// PtySession is an open terminal.
type PtySession struct {
	ExecID string `json:"execId"`
	PID    int    `json:"pid"`
	sb     *Sandbox
}

// Open starts a terminal (opts nil = /bin/bash 24×80).
func (p *Pty) Open(ctx context.Context, opts *PtyOptions) (*PtySession, error) {
	body := map[string]any{"command": []string{"/bin/bash"}, "rows": 24, "cols": 80}
	if opts != nil {
		if len(opts.Command) > 0 {
			body["command"] = opts.Command
		}
		if opts.Rows > 0 {
			body["rows"] = opts.Rows
		}
		if opts.Cols > 0 {
			body["cols"] = opts.Cols
		}
		if opts.Cwd != "" {
			body["cwd"] = opts.Cwd
		}
		if len(opts.Env) > 0 {
			body["envVars"] = opts.Env
		}
	}
	var out PtySession
	if err := p.sb.c.doJSON(ctx, &requestSpec{method: http.MethodPost, path: sbPath(p.sb.ID, "/pty"), json: body}, &out); err != nil {
		return nil, err
	}
	out.sb = p.sb
	return &out, nil
}

// Attach rebinds an existing terminal id to this sandbox.
func (p *Pty) Attach(execID string, pid int) *PtySession {
	return &PtySession{ExecID: execID, PID: pid, sb: p.sb}
}

func (s *PtySession) path(rest string) string {
	return sbPath(s.sb.ID, "/pty/", url.PathEscape(s.ExecID), rest)
}

// Input sends raw bytes to the terminal.
func (s *PtySession) Input(ctx context.Context, data []byte) error {
	return s.sb.c.doJSON(ctx, &requestSpec{method: http.MethodPost, path: s.path("/input"), raw: bytes.NewReader(data)}, nil)
}

// Resize changes the terminal size.
func (s *PtySession) Resize(ctx context.Context, rows, cols int) error {
	return s.sb.c.doJSON(ctx, &requestSpec{method: http.MethodPost, path: s.path("/resize"), json: map[string]int{"rows": rows, "cols": cols}}, nil)
}

// Kill ends the terminal.
func (s *PtySession) Kill(ctx context.Context) error {
	return s.sb.c.doJSON(ctx, &requestSpec{method: http.MethodPost, path: s.path("/kill")}, nil)
}

// Connect streams the terminal's output (SSE).
func (s *PtySession) Connect(ctx context.Context) (*PtyStream, error) {
	resp, err := s.sb.c.do(ctx, &requestSpec{method: http.MethodGet, path: s.path("/connect"), timeout: -1, accept: "text/event-stream"})
	if err != nil {
		return nil, err
	}
	return &PtyStream{sse: newSSEReader(resp.Body)}, nil
}

// ConnectWS opens a bidirectional terminal over WebSocket (GET …/ws/pty).
// opts nil = /bin/bash 24×80.
func (p *Pty) ConnectWS(ctx context.Context, opts *PtyOptions) (*PtyWS, error) {
	u, err := url.Parse(p.sb.c.baseURL + sbPath(p.sb.ID, "/ws/pty"))
	if err != nil {
		return nil, err
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	}
	hdr := http.Header{"X-Api-Key": {p.sb.c.apiKey}, "User-Agent": {p.sb.c.userAgent()}}
	conn, err := wsDial(ctx, u.String(), hdr, p.sb.c.http)
	if err != nil {
		return nil, err
	}
	first := map[string]any{}
	if opts != nil {
		if len(opts.Command) > 0 {
			first["command"] = opts.Command
		}
		if opts.Rows > 0 {
			first["rows"] = opts.Rows
		}
		if opts.Cols > 0 {
			first["cols"] = opts.Cols
		}
		if opts.Cwd != "" {
			first["cwd"] = opts.Cwd
		}
		if len(opts.Env) > 0 {
			first["envVars"] = opts.Env
		}
	}
	if err := conn.writeJSON(first); err != nil {
		conn.Close()
		return nil, err
	}
	return &PtyWS{conn: conn}, nil
}

// PtyWS is a WebSocket terminal.
type PtyWS struct{ conn *wsConn }

// Next returns the next output / exit / error event; false on close.
func (w *PtyWS) Next() (PtyEvent, bool) {
	for {
		msg, err := w.conn.readText()
		if err != nil {
			return PtyEvent{}, false
		}
		var f struct {
			Type     string `json:"type"`
			Data     string `json:"data"`
			ExitCode int    `json:"exitCode"`
			Error    string `json:"error"`
		}
		if json.Unmarshal(msg, &f) != nil {
			continue
		}
		switch f.Type {
		case "stdout", "data":
			return PtyEvent{Data: f.Data}, true
		case "exit":
			return PtyEvent{Exit: true, ExitCode: f.ExitCode, Reason: "exit"}, true
		case "error":
			return PtyEvent{Exit: true, Error: f.Error}, true
		}
	}
}

// Input sends keystrokes.
func (w *PtyWS) Input(data string) error {
	return w.conn.writeJSON(map[string]string{"type": "stdin", "data": data})
}

// Resize changes the terminal size.
func (w *PtyWS) Resize(rows, cols int) error {
	return w.conn.writeJSON(map[string]any{"type": "resize", "rows": rows, "cols": cols})
}

// Kill ends the terminal process.
func (w *PtyWS) Kill() error { return w.conn.writeJSON(map[string]string{"type": "kill"}) }

// Close closes the connection.
func (w *PtyWS) Close() error { return w.conn.Close() }
