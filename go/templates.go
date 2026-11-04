package sbbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

func decode(b []byte, out any) error {
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("sbbox: decode: %w", err)
	}
	return nil
}

// TemplatesService covers /templates.
type TemplatesService struct{ c *Client }

// Build starts an asynchronous template build; poll with WaitReady.
func (s *TemplatesService) Build(ctx context.Context, req TemplateBuildRequest) (*TemplateBuild, error) {
	var out TemplateBuild
	err := s.c.doJSON(ctx, &requestSpec{method: http.MethodPost, path: "/templates/build", json: req, timeout: 120 * time.Second}, &out)
	return &out, err
}

// WaitReady polls GET /templates/{id} until status is ready (returns the
// record) or error (returns *TemplateBuildError). timeout 0 = 10 min,
// interval 0 = 5 s.
func (s *TemplatesService) WaitReady(ctx context.Context, id string, timeout, interval time.Duration) (*Template, error) {
	if timeout == 0 {
		timeout = 10 * time.Minute
	}
	if interval == 0 {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		t, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		switch t.Status {
		case "ready":
			return t, nil
		case "error":
			return nil, &TemplateBuildError{TemplateID: id, BuildError: t.BuildError}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("sbbox: template %s still %q after %s", id, t.Status, timeout)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
	}
}

// List returns the caller's templates.
func (s *TemplatesService) List(ctx context.Context) ([]Template, error) {
	var out struct {
		Templates []Template `json:"templates"`
	}
	err := s.c.doJSON(ctx, &requestSpec{method: http.MethodGet, path: "/templates", retry: true}, &out)
	return out.Templates, err
}

// Shared returns templates shared with the caller.
func (s *TemplatesService) Shared(ctx context.Context) ([]Template, error) {
	var out []Template
	err := s.c.doJSON(ctx, &requestSpec{method: http.MethodGet, path: "/templates/shared", retry: true}, &out)
	return out, err
}

// Get returns one template.
func (s *TemplatesService) Get(ctx context.Context, id string) (*Template, error) {
	var out Template
	err := s.c.doJSON(ctx, &requestSpec{method: http.MethodGet, path: "/templates/" + url.PathEscape(id), retry: true}, &out)
	return &out, err
}

// Delete removes a template.
func (s *TemplatesService) Delete(ctx context.Context, id string) error {
	return s.c.doJSON(ctx, &requestSpec{method: http.MethodDelete, path: "/templates/" + url.PathEscape(id)}, nil)
}

type grantBody struct {
	Perm      string `json:"perm"`
	ExpiresAt string `json:"expires_at,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

func (c *Client) putGrant(ctx context.Context, base, userID string, opts *GrantOptions) (*Grant, error) {
	b := grantBody{Perm: "read"}
	if opts != nil {
		if opts.Perm != "" {
			b.Perm = opts.Perm
		}
		b.ExpiresAt, b.Reason = opts.ExpiresAt, opts.Reason
	}
	var out Grant
	err := c.doJSON(ctx, &requestSpec{method: http.MethodPut, path: base + "/grants/" + url.PathEscape(userID), json: b}, &out)
	return &out, err
}

func (c *Client) listGrants(ctx context.Context, base string) ([]Grant, error) {
	var out []Grant
	err := c.doJSON(ctx, &requestSpec{method: http.MethodGet, path: base + "/grants", retry: true}, &out)
	return out, err
}

func (c *Client) deleteGrant(ctx context.Context, base, userID string) error {
	return c.doJSON(ctx, &requestSpec{method: http.MethodDelete, path: base + "/grants/" + url.PathEscape(userID)}, nil)
}

// Grant shares a template with a user (opts nil = read).
func (s *TemplatesService) Grant(ctx context.Context, id, userID string, opts *GrantOptions) (*Grant, error) {
	return s.c.putGrant(ctx, "/templates/"+url.PathEscape(id), userID, opts)
}

// Grants lists a template's grants.
func (s *TemplatesService) Grants(ctx context.Context, id string) ([]Grant, error) {
	return s.c.listGrants(ctx, "/templates/"+url.PathEscape(id))
}

// Revoke removes a user's grant.
func (s *TemplatesService) Revoke(ctx context.Context, id, userID string) error {
	return s.c.deleteGrant(ctx, "/templates/"+url.PathEscape(id), userID)
}
