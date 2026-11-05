package sbbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var keyPattern = regexp.MustCompile(`gsk-[A-Za-z0-9._-]+`)

// Redact replaces any API key that may appear in s.
func Redact(s string) string { return keyPattern.ReplaceAllString(s, "gsk-REDACTED") }

// APIError is the base error for every non-2xx response. Typed errors
// (*LimitError, *PlanRequiredError, …) embed it; use errors.As or the Is*
// helpers to classify.
type APIError struct {
	Status     int
	Code       string
	Message    string
	Detail     json.RawMessage
	RetryAfter time.Duration
	RequestID  string
	// Fields carries any extra keys of a structured detail / error body
	// (plan, limit, current, requested, balance, upgrade, region, reason,
	// recovery, stage, killed, suggestion, sandbox_id, state, errors …).
	Fields map[string]any
}

func (e *APIError) Error() string {
	msg := Redact(e.Message)
	if e.Code != "" {
		return fmt.Sprintf("sbbox: %d %s: %s", e.Status, e.Code, msg)
	}
	return fmt.Sprintf("sbbox: %d: %s", e.Status, msg)
}

func (e *APIError) str(k string) string {
	if v, ok := e.Fields[k].(string); ok {
		return v
	}
	return ""
}

func (e *APIError) num(k string) float64 {
	if v, ok := e.Fields[k].(float64); ok {
		return v
	}
	return 0
}

// ValidationError — 400 / 422.
type ValidationError struct{ *APIError }

// AuthenticationError — 401.
type AuthenticationError struct{ *APIError }

// InsufficientCreditsError — 402 (sb_box_insufficient_credits).
type InsufficientCreditsError struct{ *APIError }

// Balance is the account's credit balance reported by the server.
func (e *InsufficientCreditsError) Balance() float64 { return e.num("balance") }

// PlanRequiredError — 403 with code sb_box_plan_required.
type PlanRequiredError struct{ *APIError }

// Plan returns the plan the feature needs; Upgrade the upgrade URL.
func (e *PlanRequiredError) Plan() string    { return e.str("plan") }
func (e *PlanRequiredError) Upgrade() string { return e.str("upgrade") }

// ForbiddenError — any other 403.
type ForbiddenError struct{ *APIError }

// NotFoundError — 404 (also every call on a killed sandbox).
type NotFoundError struct{ *APIError }

// RegionRetiredError — 404 with code sb_box_region_retired.
type RegionRetiredError struct{ *NotFoundError }

// Region returns the retired region.
func (e *RegionRetiredError) Region() string { return e.str("region") }

// CommandTimeoutError — 408 from exec / run-code.
type CommandTimeoutError struct{ *APIError }

// Killed reports whether the server killed the command on timeout.
func (e *CommandTimeoutError) Killed() bool { v, _ := e.Fields["killed"].(bool); return v }

// LimitError — 409 with a *_limit code.
type LimitError struct{ *APIError }

func (e *LimitError) Plan() string       { return e.str("plan") }
func (e *LimitError) Limit() float64     { return e.num("limit") }
func (e *LimitError) Current() float64   { return e.num("current") }
func (e *LimitError) Requested() float64 { return e.num("requested") }

// InterpreterUnavailableError — 409 sb_box_interpreter_unavailable.
type InterpreterUnavailableError struct{ *APIError }

// ConflictError — any other 409.
type ConflictError struct{ *APIError }

// DiskLostError — 410; Recovery lists the accepted next steps ("rollback"
// when the sandbox has save points), otherwise create a new sandbox.
type DiskLostError struct{ *APIError }

func (e *DiskLostError) Recovery() []string { return stringList(e.Fields["recovery"]) }

// Resumable reports the body's "resumable" flag (nil when absent).
func (e *DiskLostError) Resumable() *bool {
	b, ok := e.Fields["resumable"].(bool)
	if !ok {
		return nil
	}
	return &b
}

// MemoryExpiredError — 428; Recovery lists the accepted next steps.
type MemoryExpiredError struct{ *APIError }

func (e *MemoryExpiredError) Recovery() []string { return stringList(e.Fields["recovery"]) }

func stringList(v any) []string {
	raw, _ := v.([]any)
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		if s, ok := r.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// ClientDisconnectedError — 499.
type ClientDisconnectedError struct{ *APIError }

// InterpreterStartFailedError — 502 sb_box_interpreter_start_failed.
type InterpreterStartFailedError struct{ *APIError }

// ServerError — any other 5xx.
type ServerError struct{ *APIError }

// TemplateBuildError is returned by Templates.WaitReady when the build ends
// in status "error".
type TemplateBuildError struct {
	TemplateID string
	BuildError string
}

func (e *TemplateBuildError) Error() string {
	return fmt.Sprintf("sbbox: template %s build failed: %s", e.TemplateID, Redact(e.BuildError))
}

// unwrap returns the embedded *Error for classification.
func unwrap(err error) *APIError {
	var e *APIError
	if errors.As(err, &e) {
		return e
	}
	return nil
}

func IsNotFound(err error) bool { var e *NotFoundError; return errors.As(err, &e) }
func IsRegionRetired(err error) bool {
	var e *RegionRetiredError
	return errors.As(err, &e)
}
func IsPlanRequired(err error) bool { var e *PlanRequiredError; return errors.As(err, &e) }
func IsInsufficientCredits(err error) bool {
	var e *InsufficientCreditsError
	return errors.As(err, &e)
}
func IsLimit(err error) bool          { var e *LimitError; return errors.As(err, &e) }
func IsMemoryExpired(err error) bool  { var e *MemoryExpiredError; return errors.As(err, &e) }
func IsDiskLost(err error) bool       { var e *DiskLostError; return errors.As(err, &e) }
func IsCommandTimeout(err error) bool { var e *CommandTimeoutError; return errors.As(err, &e) }
func IsAuthentication(err error) bool {
	var e *AuthenticationError
	return errors.As(err, &e)
}
func IsValidation(err error) bool { var e *ValidationError; return errors.As(err, &e) }
func IsConflict(err error) bool   { var e *ConflictError; return errors.As(err, &e) }
func IsServerError(err error) bool {
	var e *ServerError
	return errors.As(err, &e)
}

// Status returns the HTTP status of an SDK error, or 0.
func Status(err error) int {
	if e := unwrap(err); e != nil {
		return e.Status
	}
	return 0
}

// parseError builds the typed error for a non-2xx response.
func parseError(resp *http.Response, body []byte) error {
	base := &APIError{Status: resp.StatusCode, Detail: json.RawMessage(body), RequestID: resp.Header.Get("x-request-id"), Fields: map[string]any{}}
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(strings.TrimSpace(ra)); err == nil {
			base.RetryAfter = time.Duration(secs) * time.Second
		}
	}
	var top map[string]any
	if json.Unmarshal(body, &top) == nil && top != nil {
		switch d := top["detail"].(type) {
		case string:
			base.Message = d
		case map[string]any:
			for k, v := range d {
				base.Fields[k] = v
			}
		}
		for k, v := range top {
			if k == "detail" {
				continue
			}
			base.Fields[k] = v
		}
		if base.Message == "" {
			for _, k := range []string{"message", "error", "msg"} {
				if s, ok := base.Fields[k].(string); ok && s != "" {
					base.Message = s
					break
				}
			}
		}
		if c, ok := base.Fields["code"].(string); ok {
			base.Code = c
		}
		if base.Message == "" {
			if r, ok := base.Fields["reason"].(string); ok {
				base.Message = r
			}
		}
	}
	if base.Message == "" {
		base.Message = strings.TrimSpace(string(body))
		if base.Message == "" {
			base.Message = http.StatusText(resp.StatusCode)
		}
	}
	if resp.StatusCode == 428 && base.Code == "" {
		base.Code, _ = base.Fields["reason"].(string)
	}
	return classify(base)
}

func classify(e *APIError) error {
	switch e.Status {
	case 400, 422:
		return &ValidationError{e}
	case 401:
		return &AuthenticationError{e}
	case 402:
		return &InsufficientCreditsError{e}
	case 403:
		if e.Code == "sb_box_plan_required" {
			return &PlanRequiredError{e}
		}
		return &ForbiddenError{e}
	case 404:
		nf := &NotFoundError{e}
		if e.Code == "sb_box_region_retired" {
			return &RegionRetiredError{nf}
		}
		return nf
	case 408:
		return &CommandTimeoutError{e}
	case 409:
		if strings.HasSuffix(e.Code, "_limit") {
			return &LimitError{e}
		}
		if e.Code == "sb_box_interpreter_unavailable" {
			return &InterpreterUnavailableError{e}
		}
		return &ConflictError{e}
	case 410:
		return &DiskLostError{e}
	case 428:
		return &MemoryExpiredError{e}
	case 499:
		return &ClientDisconnectedError{e}
	case 502:
		if e.Code == "sb_box_interpreter_start_failed" {
			return &InterpreterStartFailedError{e}
		}
		return &ServerError{e}
	}
	if e.Status >= 500 {
		return &ServerError{e}
	}
	return e
}

// Unwrap exposes the embedded *APIError so errors.As(err, &apiErr) works on
// every typed error.
func (e *ValidationError) Unwrap() error             { return e.APIError }
func (e *AuthenticationError) Unwrap() error         { return e.APIError }
func (e *InsufficientCreditsError) Unwrap() error    { return e.APIError }
func (e *PlanRequiredError) Unwrap() error           { return e.APIError }
func (e *ForbiddenError) Unwrap() error              { return e.APIError }
func (e *NotFoundError) Unwrap() error               { return e.APIError }
func (e *RegionRetiredError) Unwrap() error          { return e.NotFoundError }
func (e *CommandTimeoutError) Unwrap() error         { return e.APIError }
func (e *LimitError) Unwrap() error                  { return e.APIError }
func (e *InterpreterUnavailableError) Unwrap() error { return e.APIError }
func (e *ConflictError) Unwrap() error               { return e.APIError }
func (e *DiskLostError) Unwrap() error               { return e.APIError }
func (e *MemoryExpiredError) Unwrap() error          { return e.APIError }
func (e *ClientDisconnectedError) Unwrap() error     { return e.APIError }
func (e *InterpreterStartFailedError) Unwrap() error { return e.APIError }
func (e *ServerError) Unwrap() error                 { return e.APIError }
