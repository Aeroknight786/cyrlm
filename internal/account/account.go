// Package account stores the user's cyrlm account key and sharing
// preference, keeps verification reports on disk, and uploads them only
// when the user opts in. Reports contain timings, token counts and a coarse
// hardware description — never prompts, file paths, hostnames or usernames.
package account

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Sharing modes.
const (
	ShareAsk    = "ask"    // default: show the payload and ask each time
	ShareAlways = "always" // upload every verification automatically
	ShareNever  = "never"
)

// DefaultAPIURL is the cyrlm account server. Empty until the service is
// deployed; set CYRLM_API_URL or `cyrlm config set api-url` meanwhile.
const DefaultAPIURL = ""

// Config is the local account configuration.
type Config struct {
	APIURL string `json:"api_url,omitempty"`
	Key    string `json:"key,omitempty"`
	Share  string `json:"share,omitempty"`
	// OpenRouterKey is the user's own OpenRouter key for trials, stored
	// here by `cyrlm login --openrouter` so it never passes through a chat.
	OpenRouterKey string `json:"openrouter_key,omitempty"`
}

// Dir returns cyrlm's config directory (CYRLM_HOME overrides).
func Dir() (string, error) {
	if d := os.Getenv("CYRLM_HOME"); d != "" {
		return d, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "cyrlm"), nil
}

func configPath() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "config.json"), nil
}

// Load reads the config; CYRLM_KEY and CYRLM_API_URL override the file.
func Load() Config {
	var c Config
	if p, err := configPath(); err == nil {
		if b, err := os.ReadFile(p); err == nil {
			_ = json.Unmarshal(b, &c)
		}
	}
	if v := os.Getenv("CYRLM_KEY"); v != "" {
		c.Key = v
	}
	if v := os.Getenv("CYRLM_API_URL"); v != "" {
		c.APIURL = v
	}
	if c.APIURL == "" {
		c.APIURL = DefaultAPIURL
	}
	if c.Share == "" {
		c.Share = ShareAsk
	}
	return c
}

// Save writes the config with owner-only permissions.
func (c Config) Save() error {
	p, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o600)
}

// LoggedIn reports whether a key is configured.
func (c Config) LoggedIn() bool { return c.Key != "" }

// CanShare reports whether uploads are possible.
func (c Config) CanShare() bool { return c.Key != "" && c.APIURL != "" }

// MaskedKey shows only the key's ends.
func (c Config) MaskedKey() string { return Mask(c.Key) }

// Mask shows only a secret's ends.
func Mask(k string) string {
	if len(k) <= 8 {
		return strings.Repeat("•", len(k))
	}
	return k[:4] + strings.Repeat("•", 6) + k[len(k)-4:]
}

// ---------------------------------------------------------------- reports

// Machine is the coarse, non-identifying hardware description.
type Machine struct {
	OS         string `json:"os"`
	Arch       string `json:"arch"`
	CPU        string `json:"cpu"`
	CPUCores   int    `json:"cpu_cores"`
	RAMGB      int    `json:"ram_gb"`
	GPU        string `json:"gpu,omitempty"`
	GPUCores   int    `json:"gpu_cores,omitempty"`
	VRAMGB     int    `json:"vram_gb,omitempty"`
	Unified    bool   `json:"unified_memory"`
	Laptop     bool   `json:"laptop,omitempty"`
	DeviceSpec string `json:"device_spec"`
	Segment    string `json:"segment"`
}

// Settings are the runtime settings in effect during the measurement.
type Settings struct {
	ContextTokens  int    `json:"context_tokens"`
	KVCacheType    string `json:"kv_cache_type"`
	FlashAttention bool   `json:"flash_attention"`
	Backend        string `json:"backend"`
}

// Metric pairs a measured and a predicted value.
type Metric struct {
	Measured  float64 `json:"measured"`
	Predicted float64 `json:"predicted,omitempty"`
	// ErrorPct is 100 × (predicted/measured − 1); 0 when not predicted.
	ErrorPct float64 `json:"error_pct,omitempty"`
}

// NewMetric builds a metric (predicted 0 = no prediction).
func NewMetric(measured, predicted float64) Metric {
	m := Metric{Measured: round(measured, 3), Predicted: round(predicted, 3)}
	if predicted > 0 && measured > 0 {
		m.ErrorPct = round(100*(predicted/measured-1), 1)
	}
	return m
}

func round(v float64, d int) float64 {
	p := 1.0
	for i := 0; i < d; i++ {
		p *= 10
	}
	if v < 0 {
		return -float64(int64(-v*p+0.5)) / p
	}
	return float64(int64(v*p+0.5)) / p
}

// Report is one verification, as stored and (opt-in) shared.
type Report struct {
	Schema       int       `json:"schema"`
	ID           string    `json:"id"`
	CyrlmVersion string    `json:"cyrlm_version"`
	CreatedAt    time.Time `json:"created_at"`
	Machine      Machine   `json:"machine"`
	Runtime      string    `json:"runtime"`
	RuntimeVer   string    `json:"runtime_version,omitempty"`
	Model        string    `json:"model"`
	Quant        string    `json:"quant"`
	Settings     Settings  `json:"settings"`
	// Workload sizes used by the measurement.
	PromptTokens   int `json:"prompt_tokens"`
	FollowUpTokens int `json:"follow_up_new_tokens"`
	OutputTokens   int `json:"output_tokens"`

	LoadSec         Metric `json:"load_s"`
	PromptSec       Metric `json:"prompt_processing_s"`
	DecodeTPS       Metric `json:"decode_tps"`
	FollowUpSec     Metric `json:"follow_up_ttft_s"`
	CacheReused     bool   `json:"cache_reused"`
	CacheCheck      string `json:"cache_check,omitempty"`
	TokensEstimated bool   `json:"tokens_estimated,omitempty"`

	// SharedID is set after a successful upload.
	SharedID string `json:"shared_id,omitempty"`
}

// NewID returns a random report id.
func NewID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func resultsDir() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "results"), nil
}

// SaveReport writes a report and returns its path.
func SaveReport(r Report) (string, error) {
	d, err := resultsDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", err
	}
	name := fmt.Sprintf("%s-%s-%s.json", r.CreatedAt.UTC().Format("20060102-150405"), safe(r.Model), r.ID[:6])
	p := filepath.Join(d, name)
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}
	return p, os.WriteFile(p, b, 0o600)
}

func safe(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	return b.String()
}

// ReadReport reads a report file.
func ReadReport(path string) (Report, error) {
	var r Report
	b, err := os.ReadFile(path)
	if err != nil {
		return r, err
	}
	return r, json.Unmarshal(b, &r)
}

// LastReport returns the newest saved report.
func LastReport() (string, Report, error) {
	d, err := resultsDir()
	if err != nil {
		return "", Report{}, err
	}
	entries, _ := os.ReadDir(d)
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return "", Report{}, errors.New("no verification results yet — run `cyrlm verify` first")
	}
	sort.Strings(names)
	p := filepath.Join(d, names[len(names)-1])
	r, err := ReadReport(p)
	return p, r, err
}

// ---------------------------------------------------------------- server

// Me is the account summary returned by the server.
type Me struct {
	Account    string  `json:"account"`
	Plan       string  `json:"plan,omitempty"`
	CreditsUSD float64 `json:"credits_usd,omitempty"`
}

var client = &http.Client{Timeout: 20 * time.Second}

func (c Config) do(ctx context.Context, method, path string, body any, out any) error {
	if c.APIURL == "" {
		return errors.New("no cyrlm account server is configured yet (set CYRLM_API_URL or `cyrlm config set api-url <url>`)")
	}
	if c.Key == "" {
		return errors.New("not logged in — run `cyrlm login` (get a key from your cyrlm account page)")
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.APIURL, "/")+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "cyrlm")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("cyrlm server: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

// CheckKey validates the key with the server.
func (c Config) CheckKey(ctx context.Context) (Me, error) {
	var me Me
	err := c.do(ctx, http.MethodGet, "/v1/me", nil, &me)
	return me, err
}

// Upload shares a report and returns the server's id for it.
func (c Config) Upload(ctx context.Context, r Report) (string, error) {
	r.SharedID = ""
	var out struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, "/v1/measurements", r, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// MarkShared records the upload id in the saved report.
func MarkShared(path, id string) error {
	r, err := ReadReport(path)
	if err != nil {
		return err
	}
	r.SharedID = id
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}
