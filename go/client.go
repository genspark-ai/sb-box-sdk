// Package sbbox is the official Go SDK for SB-Box sandboxes.
//
//	client, err := sbbox.New()                // SBBOX_API_KEY or GSK_API_KEY
//	sb, err := client.Sandboxes.Create(ctx, sbbox.CreateOptions{Template: "base"})
//	res, err := sb.Exec(ctx, []string{"echo", "hello"}, nil)
//	defer sb.Kill(ctx)
package sbbox

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Client talks to the SB-Box API.
type Client struct {
	apiKey   string
	baseURL  string
	http     *http.Client
	timeout  time.Duration
	retries  int
	uaSuffix string

	Templates *TemplatesService
	Sandboxes *SandboxesService
}

// Option configures a Client.
type Option func(*Client)

// WithAPIKey sets the API key (else SBBOX_API_KEY, else GSK_API_KEY).
func WithAPIKey(key string) Option { return func(c *Client) { c.apiKey = key } }

// WithBaseURL sets the API base URL (else SBBOX_BASE_URL, else production).
func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = u } }

// WithHTTPClient replaces the underlying *http.Client.
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// WithTimeout sets the default per-request timeout (default 60 s).
func WithTimeout(d time.Duration) Option { return func(c *Client) { c.timeout = d } }

// WithRetries sets the retry count for idempotent calls (default 3; 0 disables).
func WithRetries(n int) Option { return func(c *Client) { c.retries = n } }

// WithUserAgentSuffix appends a product token to the User-Agent.
func WithUserAgentSuffix(s string) Option { return func(c *Client) { c.uaSuffix = s } }

// New builds a Client. It fails when no API key can be found.
func New(opts ...Option) (*Client, error) {
	c := &Client{timeout: 60 * time.Second, retries: 3}
	for _, o := range opts {
		o(c)
	}
	if c.apiKey == "" {
		c.apiKey = firstEnv("SBBOX_API_KEY", "GSK_API_KEY")
	}
	if c.apiKey == "" {
		return nil, errors.New("sbbox: no API key: pass sbbox.WithAPIKey or set SBBOX_API_KEY (or GSK_API_KEY)")
	}
	if c.baseURL == "" {
		c.baseURL = firstEnv("SBBOX_BASE_URL")
	}
	if c.baseURL == "" {
		c.baseURL = defaultBaseURL
	}
	c.baseURL = strings.TrimRight(c.baseURL, "/")
	if _, err := url.Parse(c.baseURL); err != nil {
		return nil, err
	}
	if c.http == nil {
		c.http = &http.Client{}
	}
	c.Templates = &TemplatesService{c: c}
	c.Sandboxes = &SandboxesService{c: c}
	return c, nil
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

// BaseURL returns the configured base URL.
func (c *Client) BaseURL() string { return c.baseURL }

// Regions lists the available regions.
func (c *Client) Regions(ctx context.Context) (*Regions, error) {
	var out Regions
	err := c.doJSON(ctx, &requestSpec{method: http.MethodGet, path: "/regions", retry: true}, &out)
	return &out, err
}

// Limits returns the account's plan limits and current usage counters.
func (c *Client) Limits(ctx context.Context) (*Limits, error) {
	var out Limits
	err := c.doJSON(ctx, &requestSpec{method: http.MethodGet, path: "/limits", retry: true}, &out)
	return &out, err
}

// Usage returns usage for the last days (1–366; 0 = server default of 30).
func (c *Client) Usage(ctx context.Context, days int) (*Usage, error) {
	q := url.Values{}
	if days > 0 {
		q.Set("days", strconv.Itoa(days))
	}
	var raw json.RawMessage
	if err := c.doJSON(ctx, &requestSpec{method: http.MethodGet, path: "/usage", query: q, retry: true}, &raw); err != nil {
		return nil, err
	}
	var out Usage
	if err := decode(raw, &out); err != nil {
		return nil, err
	}
	out.Raw = append([]byte(nil), raw...)
	return &out, nil
}
