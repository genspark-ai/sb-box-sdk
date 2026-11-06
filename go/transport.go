package sbbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Version is the SDK version sent in the User-Agent.
const Version = "0.1.1"

const defaultBaseURL = "https://www.genspark.ai/api/sb-box/v1"

type requestSpec struct {
	method  string
	path    string
	query   url.Values
	json    any
	raw     io.Reader
	rawType string
	timeout time.Duration
	// retry enables the idempotent retry policy (connection errors, 502/503/504).
	retry bool
	// honourRetryAfter re-tries 503 with Retry-After until the context / deadline ends.
	honourRetryAfter bool
	accept           string
}

func (c *Client) userAgent() string {
	ua := "sbbox-go-sdk/" + Version + " (+https://www.genspark.ai/sb-box/docs)"
	if c.uaSuffix != "" {
		ua += " " + c.uaSuffix
	}
	return ua
}

func (c *Client) newRequest(ctx context.Context, spec *requestSpec) (*http.Request, error) {
	u := c.baseURL + spec.path
	if len(spec.query) > 0 {
		u += "?" + spec.query.Encode()
	}
	var body io.Reader
	ctype := ""
	if spec.json != nil {
		b, err := json.Marshal(spec.json)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(b)
		ctype = "application/json"
	} else if spec.raw != nil {
		body = spec.raw
		ctype = spec.rawType
		if ctype == "" {
			ctype = "application/octet-stream"
		}
	}
	req, err := http.NewRequestWithContext(ctx, spec.method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", c.apiKey)
	req.Header.Set("User-Agent", c.userAgent())
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if spec.accept != "" {
		req.Header.Set("Accept", spec.accept)
	} else {
		req.Header.Set("Accept", "application/json")
	}
	return req, nil
}

// bufferedBody makes a request body replayable for retries.
func bufferedBody(spec *requestSpec) ([]byte, error) {
	if spec.raw == nil {
		return nil, nil
	}
	b, err := io.ReadAll(spec.raw)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func retryable(status int) bool { return status == 502 || status == 503 || status == 504 }

var backoffs = []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second}

func jitter(d time.Duration) time.Duration {
	return d/2 + time.Duration(rand.Int63n(int64(d/2)+1))
}

// do performs the request and returns the response for a 2xx; non-2xx bodies
// are turned into typed errors. The caller must close the body.
func (c *Client) do(ctx context.Context, spec *requestSpec) (*http.Response, error) {
	timeout := spec.timeout
	if timeout == 0 {
		timeout = c.timeout
	}
	var raw []byte
	if spec.raw != nil && (spec.retry || spec.honourRetryAfter) {
		var err error
		if raw, err = bufferedBody(spec); err != nil {
			return nil, err
		}
	}
	maxAttempts := 1
	if spec.retry {
		maxAttempts = 1 + c.retries
	}
	deadline, hasDeadline := ctx.Deadline()
	_ = deadline
	var lastErr error
	for attempt := 0; ; attempt++ {
		if raw != nil {
			spec.raw = bytes.NewReader(raw)
		}
		rctx, cancel := ctx, func() {}
		if timeout > 0 {
			rctx, cancel = context.WithTimeout(ctx, timeout)
		}
		req, err := c.newRequest(rctx, spec)
		if err != nil {
			cancel()
			return nil, err
		}
		resp, err := c.http.Do(req)
		if err != nil {
			cancel()
			lastErr = &TransportError{Err: err}
			if ctx.Err() != nil || attempt+1 >= maxAttempts {
				return nil, lastErr
			}
			time.Sleep(jitter(backoffs[min(attempt, len(backoffs)-1)]))
			continue
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			// Streaming bodies keep the per-request context alive via the response closer.
			resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
			return resp, nil
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		cancel()
		lastErr = parseError(resp, body)
		if spec.honourRetryAfter && resp.StatusCode == 503 {
			wait := 2 * time.Second
			if e := unwrap(lastErr); e != nil && e.RetryAfter > 0 {
				wait = e.RetryAfter
			}
			if hasDeadline && time.Until(deadline) < wait {
				return nil, lastErr
			}
			if !hasDeadline && attempt >= c.retries*10 {
				return nil, lastErr
			}
			select {
			case <-ctx.Done():
				return nil, lastErr
			case <-time.After(wait):
			}
			continue
		}
		if spec.retry && retryable(resp.StatusCode) && attempt+1 < maxAttempts {
			time.Sleep(jitter(backoffs[min(attempt, len(backoffs)-1)]))
			continue
		}
		return nil, lastErr
	}
}

type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnClose) Close() error { err := c.ReadCloser.Close(); c.cancel(); return err }

// doJSON performs the request and decodes a JSON answer into out (may be nil).
func (c *Client) doJSON(ctx context.Context, spec *requestSpec, out any) error {
	resp, err := c.do(ctx, spec)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return &TransportError{Err: err}
	}
	if rawOut, ok := out.(*json.RawMessage); ok {
		*rawOut = append((*rawOut)[:0], b...)
		return nil
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return nil
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("sbbox: decode %s %s: %w", spec.method, spec.path, err)
	}
	return nil
}

// TransportError wraps a connection / IO failure.
type TransportError struct{ Err error }

func (e *TransportError) Error() string { return "sbbox: transport: " + Redact(e.Err.Error()) }
func (e *TransportError) Unwrap() error { return e.Err }

// IsTransport reports whether err is a connection / IO failure.
func IsTransport(err error) bool { var e *TransportError; return errors.As(err, &e) }

func validateMetadata(m map[string]string) error {
	for k := range m {
		if strings.TrimSpace(k) == "" {
			return errors.New("sbbox: metadata keys must be non-empty strings")
		}
	}
	return nil
}
