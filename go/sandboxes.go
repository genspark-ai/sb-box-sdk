package sbbox

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// SandboxesService covers /sandboxes.
type SandboxesService struct{ c *Client }

func sbPath(id string, rest ...string) string {
	return "/sandboxes/" + url.PathEscape(id) + strings.Join(rest, "")
}

func (s *SandboxesService) handle(info *SandboxInfo) *Sandbox {
	sb := &Sandbox{c: s.c, SandboxInfo: *info}
	sb.Commands = &Commands{sb: sb}
	sb.Files = &Files{sb: sb}
	sb.Pty = &Pty{sb: sb}
	return sb
}

// Create starts a sandbox and returns a handle bound to this client.
func (s *SandboxesService) Create(ctx context.Context, opts CreateOptions) (*Sandbox, error) {
	if err := validateMetadata(opts.Metadata); err != nil {
		return nil, err
	}
	body := map[string]any{}
	switch {
	case opts.TemplateID != "":
		body["templateId"] = opts.TemplateID
	case opts.Template != "":
		body["templateName"] = opts.Template
	default:
		body["templateName"] = "base"
	}
	if opts.Timeout > 0 {
		body["timeout"] = opts.Timeout
	}
	if opts.OnTimeout != "" {
		body["lifecycle"] = map[string]string{"onTimeout": opts.OnTimeout}
	}
	if len(opts.Metadata) > 0 {
		body["metadata"] = opts.Metadata
	}
	if len(opts.EnvVars) > 0 {
		body["envVars"] = opts.EnvVars
	}
	if opts.Network != nil {
		body["network"] = opts.Network
	}
	var info SandboxInfo
	if err := s.c.doJSON(ctx, &requestSpec{method: http.MethodPost, path: "/sandboxes", json: body, timeout: 120 * time.Second}, &info); err != nil {
		return nil, err
	}
	return s.handle(&info), nil
}

// List returns compact rows, optionally filtered by state(s)
// (starting, running, freezing, frozen, exited, failed, unknown, all).
func (s *SandboxesService) List(ctx context.Context, states ...string) ([]SandboxSummary, error) {
	q := url.Values{}
	if len(states) > 0 {
		q.Set("state", strings.Join(states, ","))
	}
	var out struct {
		Sandboxes []SandboxSummary `json:"sandboxes"`
	}
	err := s.c.doJSON(ctx, &requestSpec{method: http.MethodGet, path: "/sandboxes", query: q, retry: true}, &out)
	return out.Sandboxes, err
}

// Shared returns sandboxes shared with the caller.
func (s *SandboxesService) Shared(ctx context.Context) ([]SandboxInfo, error) {
	var out []SandboxInfo
	err := s.c.doJSON(ctx, &requestSpec{method: http.MethodGet, path: "/sandboxes/shared", retry: true}, &out)
	return out, err
}

// Get returns a handle for an existing sandbox.
func (s *SandboxesService) Get(ctx context.Context, id string) (*Sandbox, error) {
	var info SandboxInfo
	if err := s.c.doJSON(ctx, &requestSpec{method: http.MethodGet, path: sbPath(id), retry: true}, &info); err != nil {
		return nil, err
	}
	return s.handle(&info), nil
}

// Connect is an alias of Get.
func (s *SandboxesService) Connect(ctx context.Context, id string) (*Sandbox, error) {
	return s.Get(ctx, id)
}

// Kill deletes a sandbox. Later calls on the id answer not found.
func (s *SandboxesService) Kill(ctx context.Context, id string) error {
	return s.c.doJSON(ctx, &requestSpec{method: http.MethodDelete, path: sbPath(id), retry: true}, nil)
}

// Freeze snapshots a sandbox. With wait, a 202 "freezing" answer is polled
// until the sandbox is frozen (2 s interval, 90 s cap).
func (s *SandboxesService) Freeze(ctx context.Context, id string, wait bool) (*SandboxInfo, error) {
	resp, err := s.c.do(ctx, &requestSpec{method: http.MethodPost, path: sbPath(id, "/freeze"), retry: true, timeout: 120 * time.Second})
	if err != nil {
		return nil, err
	}
	var info SandboxInfo
	if err := readJSON(resp, &info); err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusAccepted || !wait {
		if info.ID == "" {
			info.ID = id
		}
		return &info, nil
	}
	deadline := time.Now().Add(freezeWaitCap)
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(freezePollInterval):
		}
		cur, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		switch cur.State {
		case "frozen":
			return &cur.SandboxInfo, nil
		case "freezing", "running":
			// running can still show up right after a 202: the freeze is
			// queued or the read lags the write.
		default:
			return nil, &ConflictError{&APIError{Status: 409, Message: fmt.Sprintf("freeze of %s ended in state %q", id, cur.State), Fields: map[string]any{"state": cur.State}}}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("sbbox: freeze of %s still in progress after %s", id, freezeWaitCap)
		}
	}
}

// Freeze wait pacing; variables so tests can shorten them.
var (
	freezePollInterval = 2 * time.Second
	freezeWaitCap      = 90 * time.Second
)

// Resume brings a frozen sandbox back. timeout (seconds) 0 = unchanged.
func (s *SandboxesService) Resume(ctx context.Context, id string, timeout int) (*SandboxInfo, error) {
	body := map[string]any{}
	if timeout > 0 {
		body["timeout"] = timeout
	}
	var info SandboxInfo
	err := s.c.doJSON(ctx, &requestSpec{method: http.MethodPost, path: sbPath(id, "/resume"), json: body, retry: true, timeout: 120 * time.Second}, &info)
	return &info, err
}

// Rollback restores a save point (version 0 = newest).
func (s *SandboxesService) Rollback(ctx context.Context, id string, version, timeout int) (*SandboxInfo, error) {
	body := map[string]any{"version": version}
	if timeout > 0 {
		body["timeout"] = timeout
	}
	var info SandboxInfo
	err := s.c.doJSON(ctx, &requestSpec{method: http.MethodPost, path: sbPath(id, "/rollback"), json: body, timeout: 120 * time.Second}, &info)
	return &info, err
}

// Clone creates a new sandbox from a save point; 503 + Retry-After while the
// clone is still starting is retried until ctx ends.
func (s *SandboxesService) Clone(ctx context.Context, id string, version int, metadata map[string]string) (*Sandbox, error) {
	if err := validateMetadata(metadata); err != nil {
		return nil, err
	}
	body := map[string]any{"version": version}
	if len(metadata) > 0 {
		body["metadata"] = metadata
	}
	var info SandboxInfo
	if err := s.c.doJSON(ctx, &requestSpec{method: http.MethodPost, path: sbPath(id, "/clone"), json: body, honourRetryAfter: true, timeout: 120 * time.Second}, &info); err != nil {
		return nil, err
	}
	return s.handle(&info), nil
}

// SetTimeout sets the lifetime deadline in seconds (0 clears it).
func (s *SandboxesService) SetTimeout(ctx context.Context, id string, seconds int) error {
	return s.c.doJSON(ctx, &requestSpec{method: http.MethodPost, path: sbPath(id, "/timeout"), json: map[string]int{"timeout": seconds}, retry: true}, nil)
}

// Reset reboots the sandbox from its disk.
func (s *SandboxesService) Reset(ctx context.Context, id string) (*SandboxInfo, error) {
	var info SandboxInfo
	err := s.c.doJSON(ctx, &requestSpec{method: http.MethodPost, path: sbPath(id, "/reset"), honourRetryAfter: true, timeout: 120 * time.Second}, &info)
	return &info, err
}

// Metrics returns samples between start and end (unix seconds; 0 = server default).
func (s *SandboxesService) Metrics(ctx context.Context, id string, start, end int64) ([]Metric, error) {
	q := url.Values{}
	if start > 0 {
		q.Set("start", strconv.FormatInt(start, 10))
	}
	if end > 0 {
		q.Set("end", strconv.FormatInt(end, 10))
	}
	var out []Metric
	err := s.c.doJSON(ctx, &requestSpec{method: http.MethodGet, path: sbPath(id, "/metrics"), query: q, retry: true}, &out)
	return out, err
}

// Grant shares a sandbox with a user (opts nil = read).
func (s *SandboxesService) Grant(ctx context.Context, id, userID string, opts *GrantOptions) (*Grant, error) {
	return s.c.putGrant(ctx, sbPath(id), userID, opts)
}

// Grants lists a sandbox's grants.
func (s *SandboxesService) Grants(ctx context.Context, id string) ([]Grant, error) {
	return s.c.listGrants(ctx, sbPath(id))
}

// Revoke removes a user's grant.
func (s *SandboxesService) Revoke(ctx context.Context, id, userID string) error {
	return s.c.deleteGrant(ctx, sbPath(id), userID)
}
