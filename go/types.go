package sbbox

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Regions is the answer of GET /regions.
type Regions struct {
	Regions []string `json:"regions"`
	Default string   `json:"default"`
}

// Limits is the answer of GET /limits.
type Limits struct {
	Plan         string `json:"plan"`
	PlanRequired bool   `json:"planRequired"`
	Limits       struct {
		MaxConcurrentSandboxes int `json:"maxConcurrentSandboxes"`
		MaxCPU                 int `json:"maxCpu"`
		MaxMemoryMB            int `json:"maxMemoryMB"`
		MaxDiskGB              int `json:"maxDiskGB"`
		MaxConcurrentBuilds    int `json:"maxConcurrentBuilds"`
	} `json:"limits"`
	Usage struct {
		RunningSandboxes  int `json:"runningSandboxes"`
		BuildingTemplates int `json:"buildingTemplates"`
	} `json:"usage"`
}

// UsageTotals is one bucket of usage counters.
type UsageTotals struct {
	RunSec     float64 `json:"runSec"`
	CPUCoreSec float64 `json:"cpuCoreSec"`
	MemGBSec   float64 `json:"memGBSec"`
	DiskGBSec  float64 `json:"diskGBSec"`
	Credits    float64 `json:"credits"`
}

// usageDaily uses snake_case on the wire.
type usageDailyTotals struct {
	RunSec     float64 `json:"run_sec"`
	CPUCoreSec float64 `json:"cpu_core_sec"`
	MemGBSec   float64 `json:"mem_gb_sec"`
	DiskGBSec  float64 `json:"disk_gb_sec"`
	Credits    float64 `json:"credits"`
}

// UsageDay is one day's usage (Day is YYYYMMDD).
type UsageDay struct {
	Day    string
	Direct UsageTotals
}

func (d *UsageDay) UnmarshalJSON(b []byte) error {
	var w struct {
		Day    string           `json:"day"`
		Direct usageDailyTotals `json:"direct"`
	}
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	d.Day = w.Day
	d.Direct = UsageTotals(w.Direct)
	return nil
}

// Usage is the answer of GET /usage. Raw keeps the full body for fields the
// SDK does not model (rate card details, extra buckets).
type Usage struct {
	Days     int             `json:"days"`
	From     string          `json:"from"`
	To       string          `json:"to"`
	RateCard json.RawMessage `json:"rateCard"`
	Direct   UsageTotals     `json:"direct"`
	Daily    []UsageDay      `json:"daily"`
	Raw      json.RawMessage `json:"-"`
}

// Template is a template record.
type Template struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Namespace   string `json:"namespace"`
	BaseImage   string `json:"base_image"`
	Status      string `json:"status"`
	BuildError  string `json:"buildError,omitempty"`
	Region      string `json:"region"`
	IsOwner     bool   `json:"is_owner"`
	CanCreate   bool   `json:"can_create"`
	CPUCount    int    `json:"cpuCount,omitempty"`
	MemoryMB    int    `json:"memoryMB,omitempty"`
	DiskSizeGB  int    `json:"diskSizeGB,omitempty"`
	StartCmd    string `json:"startCmd,omitempty"`
	ReadyCmd    string `json:"readyCmd,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// TemplateBuildRequest is the body of POST /templates/build.
type TemplateBuildRequest struct {
	Name        string   `json:"name,omitempty"`
	Image       string   `json:"image,omitempty"`
	Dockerfile  string   `json:"dockerfile,omitempty"`
	CPUCount    int      `json:"cpuCount,omitempty"`
	MemoryMB    int      `json:"memoryMB,omitempty"`
	DiskSizeGB  int      `json:"diskSizeGB,omitempty"`
	StartCmd    string   `json:"startCmd,omitempty"`
	ReadyCmd    string   `json:"readyCmd,omitempty"`
	Region      string   `json:"region,omitempty"`
	LogPaths    []string `json:"logPaths,omitempty"`
	Description string   `json:"description,omitempty"`
}

// TemplateBuild is the answer of POST /templates/build.
type TemplateBuild struct {
	TemplateID string `json:"templateId"`
	Status     string `json:"status"`
	Region     string `json:"region"`
	Owner      string `json:"owner"`
}

// Grant is a template / sandbox access grant.
type Grant struct {
	TemplateID string `json:"template_id,omitempty"`
	SandboxID  string `json:"sandbox_id,omitempty"`
	GranteeID  string `json:"grantee_id"`
	Permission string `json:"permission"`
	GrantedAt  string `json:"granted_at"`
	ExpiresAt  string `json:"expires_at,omitempty"`
}

// GrantOptions are the optional fields of a grant.
type GrantOptions struct {
	Perm      string // "read" (default) or "write"
	ExpiresAt string
	Reason    string
}

// Network is the create-time network policy.
type Network struct {
	AllowInternetAccess *bool    `json:"allowInternetAccess,omitempty"`
	AllowOut            []string `json:"allowOut,omitempty"`
	DenyOut             []string `json:"denyOut,omitempty"`
	AllowPublicTraffic  *bool    `json:"allowPublicTraffic,omitempty"`
	MaskRequestHost     *bool    `json:"maskRequestHost,omitempty"`
}

// CreateOptions are the inputs of POST /sandboxes. Template is a template
// name ("base" = the newest public base); TemplateID wins when both are set.
type CreateOptions struct {
	Template   string
	TemplateID string
	// Timeout in seconds; 0 = no deadline.
	Timeout int
	// OnTimeout is "freeze" (default) or "kill".
	OnTimeout string
	Metadata  map[string]string
	EnvVars   map[string]string
	Network   *Network
}

// SandboxVersion is one save point of a sandbox.
type SandboxVersion struct {
	Version  int    `json:"version"`
	FrozenAt string `json:"frozenAt"`
	Label    string `json:"label"`
}

// SandboxInfo is a sandbox record.
type SandboxInfo struct {
	ID                 string            `json:"id"`
	TemplateID         string            `json:"templateId"`
	State              string            `json:"state"`
	Region             string            `json:"region"`
	PortURL            string            `json:"portUrl"`
	Domain             string            `json:"domain"`
	CreatedAt          string            `json:"createdAt"`
	StartedAt          string            `json:"startedAt"`
	FrozenAt           string            `json:"frozenAt,omitempty"`
	TimeoutAt          string            `json:"timeoutAt,omitempty"`
	CPUCount           int               `json:"cpuCount"`
	MemoryMB           int               `json:"memoryMB"`
	DiskSizeGB         int               `json:"diskSizeGB"`
	IdleTimeout        int               `json:"idleTimeout"`
	Versions           []SandboxVersion  `json:"versions,omitempty"`
	Metadata           map[string]string `json:"metadata,omitempty"`
	TrafficAccessToken string            `json:"trafficAccessToken,omitempty"`
	IsOwner            bool              `json:"is_owner,omitempty"`
	HasWrite           bool              `json:"has_write,omitempty"`
}

func (s *SandboxInfo) UnmarshalJSON(b []byte) error {
	type alias SandboxInfo
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		// Metadata values may be non-strings on legacy records; retry loosely.
		var loose struct {
			alias
			Metadata map[string]any `json:"metadata"`
		}
		if err2 := json.Unmarshal(b, &loose); err2 != nil {
			return err
		}
		a = loose.alias
		a.Metadata = map[string]string{}
		for k, v := range loose.Metadata {
			if str, ok := v.(string); ok {
				a.Metadata[k] = str
			} else {
				raw, _ := json.Marshal(v)
				a.Metadata[k] = string(raw)
			}
		}
	}
	// Legacy snake_case aliases on shared / compact rows.
	var alt struct {
		TemplateID string `json:"template_id"`
		CreatedAt  string `json:"created_at"`
	}
	_ = json.Unmarshal(b, &alt)
	if a.TemplateID == "" {
		a.TemplateID = alt.TemplateID
	}
	if a.CreatedAt == "" {
		a.CreatedAt = alt.CreatedAt
	}
	*s = SandboxInfo(a)
	return nil
}

// PortURLFor substitutes the literal {port} placeholder of PortURL.
func (s *SandboxInfo) PortURLFor(port int) string {
	return strings.ReplaceAll(s.PortURL, "{port}", strconv.Itoa(port))
}

// SandboxSummary is a compact row of GET /sandboxes.
type SandboxSummary struct {
	ID         string `json:"id"`
	TemplateID string `json:"template_id"`
	State      string `json:"state"`
	Region     string `json:"region"`
	PortURL    string `json:"portUrl"`
	Domain     string `json:"domain"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

// PortURLFor substitutes the literal {port} placeholder of PortURL.
func (s *SandboxSummary) PortURLFor(port int) string {
	return strings.ReplaceAll(s.PortURL, "{port}", strconv.Itoa(port))
}

// Metric is one sample of GET /sandboxes/{id}/metrics.
type Metric struct {
	Timestamp     string  `json:"timestamp"`
	TimestampUnix int64   `json:"timestampUnix"`
	CPUCount      int     `json:"cpuCount"`
	CPUUsedPct    float64 `json:"cpuUsedPct"`
	MemUsed       int64   `json:"memUsed"`
	MemTotal      int64   `json:"memTotal"`
	MemCache      int64   `json:"memCache"`
	DiskUsed      int64   `json:"diskUsed"`
	DiskTotal     int64   `json:"diskTotal"`
}

// ExecOptions are the inputs of exec / sse/exec.
type ExecOptions struct {
	Cwd     string
	Env     map[string]string
	User    string
	Timeout int // seconds, default 60, max 600
}

// CommandResult is the answer of exec. A non-zero ExitCode is data, not an error.
type CommandResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exitCode"`
}

// ExecEvent is one event of a streamed command.
type ExecEvent struct {
	// Kind is "stdout", "stderr", "exit" or "error".
	Kind     string
	Data     string
	ExitCode int
	ExecID   string
	Stage    string
}

// RunCodeOptions are the inputs of run-code.
type RunCodeOptions struct {
	Language  string // default "python"
	ContextID string
	Env       map[string]string
	Timeout   int    // seconds, default 60, max 540
	Images    string // "base64" (default) or "omit"
}

// CodeError is the interpreter-level error of a run-code call.
type CodeError struct {
	Name      string `json:"name"`
	Value     string `json:"value"`
	Traceback string `json:"traceback"`
}

// CodeResult is the answer of run-code. Results holds the raw MIME bundles.
type CodeResult struct {
	Stdout         string            `json:"stdout"`
	Stderr         string            `json:"stderr"`
	ExecutionCount int               `json:"executionCount"`
	Complete       bool              `json:"complete"`
	Error          *CodeError        `json:"error"`
	Results        []json.RawMessage `json:"results"`
}

// StartOptions are the inputs of commands/start and pty.
type StartOptions struct {
	Cwd  string
	Env  map[string]string
	User string
}

// ProcessInfo is one row of GET /sandboxes/{id}/commands.
type ProcessInfo struct {
	ExecID    string `json:"execId"`
	PID       int    `json:"pid"`
	SandboxID string `json:"sandboxId"`
}

// FileEntry is one row of files/list.
type FileEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"isDir"`
	Size  int64  `json:"size"`
}

// PtyOptions are the inputs of POST /sandboxes/{id}/pty.
type PtyOptions struct {
	Command []string // default ["/bin/bash"]
	Rows    int      // default 24
	Cols    int      // default 80
	Cwd     string
	Env     map[string]string
}

// PtyEvent is one event of a connected PTY: Data, or Exit with ExitCode.
type PtyEvent struct {
	Data     string
	Exit     bool
	ExitCode int
	Reason   string
	Error    string
}
