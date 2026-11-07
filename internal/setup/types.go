// Package setup plans and executes reviewed, journalled local engine setups.
package setup

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/aeroknight786/trialm/internal/catalog"
	runtime "github.com/aeroknight786/trialm/internal/engine"
	"github.com/aeroknight786/trialm/internal/harness"
	"github.com/aeroknight786/trialm/internal/setup/download"
	"github.com/aeroknight786/trialm/internal/verify"
)

type Kind string

const (
	Install        Kind = "install"
	Fetch          Kind = "fetch"
	RunInstaller   Kind = "run-installer"
	Write          Kind = "write"
	Start          Kind = "start"
	Stop           Kind = "stop"
	EngineDelegate Kind = "engine-delegate"
	Admin          Kind = "admin"
	Check          Kind = "check"
)

type Step struct {
	NativeRepo    string            `json:"native_repo,omitempty"`
	DownloadBytes int64             `json:"download_bytes,omitempty"`
	ID            string            `json:"id"`
	Kind          Kind              `json:"kind"`
	Title         string            `json:"title"`
	Argv          []string          `json:"argv,omitempty"`
	Env           []string          `json:"env,omitempty"`
	Request       *download.Request `json:"request,omitempty"`
	Path          string            `json:"path,omitempty"`
	Content       string            `json:"content,omitempty"`
	Health        string            `json:"health,omitempty"`
	ArchiveRoot   string            `json:"archive_root,omitempty"`
	Package       string            `json:"package,omitempty"`
	RebootReset   bool              `json:"reboot_reset,omitempty"`
	Inverse       []string          `json:"inverse,omitempty"`
}
type Prompts struct {
	Approvals    int    `json:"approvals"`
	Passwords    int    `json:"passwords"`
	AfterRestart string `json:"after_restart,omitempty"`
}
type Plan struct {
	DecodeErrorPct  int                 `json:"decode_error_range_pct"`
	PrefillErrorPct int                 `json:"prefill_error_range_pct"`
	DownloadUnknown bool                `json:"download_unknown,omitempty"`
	ID              string              `json:"plan_id"`
	Slug            string              `json:"slug"`
	Model           string              `json:"model"`
	ModelName       string              `json:"model_name"`
	Quant           string              `json:"quant"`
	Runtime         string              `json:"runtime"`
	RuntimeName     string              `json:"runtime_name"`
	Backend         string              `json:"backend"`
	OS              string              `json:"os"`
	Arch            string              `json:"arch"`
	Harness         string              `json:"harness,omitempty"`
	LocalModelName  string              `json:"local_model_name"`
	ContextTokens   int                 `json:"context_tokens"`
	KVCacheType     string              `json:"kv_cache_type"`
	Tuned           bool                `json:"tuned"`
	Endpoint        string              `json:"endpoint"`
	DownloadBytes   int64               `json:"download_bytes"`
	Prompts         Prompts             `json:"prompts"`
	Steps           []Step              `json:"steps"`
	Untouched       []string            `json:"untouched"`
	Warnings        []string            `json:"warnings,omitempty"`
	ReadyOptions    verify.ReadyOptions `json:"-"`
	// Verification is persisted separately from callback-bearing ReadyOptions.
	Predicted     verify.Predicted `json:"predicted"`
	CheckEndpoint string           `json:"check_endpoint,omitempty"`
	APIs          []string         `json:"apis,omitempty"`
	BitsPerWeight float64          `json:"bits_per_weight"`
}

func (p *Plan) Seal() {
	p.DownloadBytes = 0
	p.DownloadUnknown = false
	p.Prompts = Prompts{Approvals: 1}
	for _, s := range p.Steps {
		p.DownloadBytes += s.DownloadBytes
		if s.Kind == RunInstaller || s.Package == "mlx-lm" {
			p.DownloadUnknown = true
		}
		if s.Request != nil {
			p.DownloadBytes += s.Request.Size
		}
		if s.Kind == Admin {
			p.Prompts.Passwords++
		}
		if s.RebootReset {
			p.Prompts.AfterRestart = "1 more after each restart"
		}
	}
	copy := *p
	copy.ID = ""
	b, err := json.Marshal(copy)
	if err != nil {
		p.ID = ""
		return
	}
	p.ID = fmt.Sprintf("%x", sha256.Sum256(b))
}

type Input struct {
	DecodeErrorPct, PrefillErrorPct             int
	Model                                       catalog.Model
	Quant                                       catalog.Quant
	Profile                                     runtime.Profile
	Settings                                    runtime.Settings
	Backend                                     string
	Harness                                     *harness.Harness
	OS, Arch                                    string
	Installed                                   bool
	InstalledVersion                            string
	Binary                                      string
	Running                                     bool
	Port                                        int
	OwnedFileExists                             func(string) bool
	Assets                                      []download.Request
	LocalModel                                  string
	RAMBytes, DefaultGPUBytes, RequiredGPUBytes float64
	Predicted                                   verify.Predicted
}

func DefaultPort(p runtime.Profile) int { return p.Port }
func slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func modelName(in Input) string {
	base := in.Model.OllamaTag
	if base == "" {
		base = in.Model.ID
	}
	if len(in.Model.Quants) > 0 && !strings.EqualFold(in.Model.Quants[0].Name, in.Quant.Name) {
		base += "-" + strings.ToLower(in.Quant.Name)
	}
	var out strings.Builder
	for _, r := range strings.ToLower(base) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune("._-:", r) {
			out.WriteRune(r)
		} else {
			out.WriteByte('-')
		}
	}
	return out.String() + "-trialm"
}

// Approval accepts the CLI's bare --yes and hash-bound --yes=<plan_id> forms.
type Approval string

func (a *Approval) String() string         { return string(*a) }
func (a *Approval) Set(value string) error { *a = Approval(value); return nil }
func (a *Approval) IsBoolFlag() bool       { return true }

// OnDisk is how many of the plan's download bytes already sit on this
// machine (Hugging Face cache, LM Studio, Ollama) at the right size; the
// fetch step reuses them after checking the digest.
func (p Plan) OnDisk() int64 {
	home, _ := os.UserHomeDir()
	var n int64
	for _, s := range p.Steps {
		if s.Kind != Fetch || s.Request == nil {
			continue
		}
		repo, file := hfLocation(s.Request.URL)
		for _, c := range download.Candidates(home, repo, file, s.Request.SHA256) {
			if st, err := os.Stat(c); err == nil && st.Size() == s.Request.Size {
				n += s.Request.Size
				break
			}
		}
	}
	return n
}
