// Package verify measures a real local runtime the way a coding harness
// uses it: a model-load probe, a long uncached prompt (prompt processing and
// generation speed), and a follow-up that extends it (prompt-cache reuse).
// Results are compared with cyrlm's prediction and can be shared, opt-in,
// to improve future predictions. Only timings and token counts are kept —
// never prompt text.
package verify

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aeroknight786/cyrlm/internal/llm"
	"github.com/aeroknight786/cyrlm/internal/tok"
	"github.com/aeroknight786/cyrlm/internal/upstream"
)

// Cache check outcomes.
const (
	CacheReused       = "reused"
	CacheNotReused    = "not_reused"
	CacheInconclusive = "inconclusive"
)

// Options configures a verification run.
type Options struct {
	// Endpoint is the runtime's base URL (e.g. http://127.0.0.1:11434);
	// /v1/chat/completions is appended.
	Endpoint string
	Model    string
	APIKey   string
	// PromptTokens is the size of the uncached first prompt (default 8000).
	PromptTokens int
	// FollowUpTokens is the new input in the follow-up turn (default 1000).
	FollowUpTokens int
	// OutputTokens caps generation per request (default 256).
	OutputTokens int
	Progress     io.Writer
}

// Measurement is one timed request.
type Measurement struct {
	PromptTokens     int     `json:"prompt_tokens"`
	CachedTokens     int     `json:"cached_tokens,omitempty"`
	CompletionTokens int     `json:"completion_tokens"`
	TTFTSec          float64 `json:"ttft_s"`
	DecodeTPS        float64 `json:"decode_tps"`
	TotalSec         float64 `json:"total_s"`
	// TokensEstimated is true when the runtime reported no usage.
	TokensEstimated bool `json:"tokens_estimated,omitempty"`
}

// Result is a full run.
type Result struct {
	Endpoint string      `json:"-"`
	Model    string      `json:"runtime_model"`
	Started  time.Time   `json:"started"`
	Probe    Measurement `json:"probe"`
	Cold     Measurement `json:"cold"`
	FollowUp Measurement `json:"follow_up"`
	// PrefillTPS is cold prompt tokens / cold TTFT.
	PrefillTPS float64 `json:"prefill_tps"`
	// FollowUpPrefillTPS is follow-up new tokens / follow-up TTFT.
	FollowUpPrefillTPS float64 `json:"follow_up_prefill_tps"`
	// CacheCheck is CacheReused, CacheNotReused or CacheInconclusive.
	CacheCheck string `json:"cache_check"`
	// CacheReused is true when the follow-up only processed new tokens.
	CacheReused bool     `json:"cache_reused"`
	Notes       []string `json:"notes,omitempty"`
}

func (o *Options) defaults() {
	if o.PromptTokens <= 0 {
		o.PromptTokens = 8000
	}
	if o.FollowUpTokens <= 0 {
		o.FollowUpTokens = 1000
	}
	if o.OutputTokens <= 0 {
		o.OutputTokens = 256
	}
	if o.Progress == nil {
		o.Progress = io.Discard
	}
}

// Run executes the three requests.
func Run(ctx context.Context, o Options) (*Result, error) {
	o.defaults()
	c := &upstream.OpenAICompat{BaseURL: strings.TrimRight(o.Endpoint, "/") + "/v1", APIKey: o.APIKey, Model: o.Model}
	res := &Result{Endpoint: o.Endpoint, Model: o.Model, Started: time.Now()}
	zero := 0.0

	fmt.Fprintf(o.Progress, "  1/3 loading the model (a short request)…\n")
	probe := &llm.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: "Reply with the single word OK."}}, MaxTokens: 8, Temperature: &zero, Stream: true}
	m, _, err := measure(ctx, c, probe)
	if err != nil {
		return nil, fmt.Errorf("could not reach %s with model %q: %w", o.Endpoint, o.Model, err)
	}
	res.Probe = m

	nonce := make([]byte, 6)
	_, _ = rand.Read(nonce)
	system := fmt.Sprintf("Session %s. You are a coding agent working in the repository below.\n\n", hex.EncodeToString(nonce)) +
		syntheticContext(o.PromptTokens)
	ask := "Explain in detail, step by step, how the request handling, caching and retry logic in this repository work. Write at least 400 words."
	cold := &llm.Request{Messages: []llm.Message{{Role: llm.RoleSystem, Content: system}, {Role: llm.RoleUser, Content: ask}},
		MaxTokens: o.OutputTokens, Temperature: &zero, Stream: true}
	fmt.Fprintf(o.Progress, "  2/3 reading a ~%d-token prompt and writing up to %d tokens…\n", o.PromptTokens, o.OutputTokens)
	m, reply, err := measure(ctx, c, cold)
	if err != nil {
		return nil, err
	}
	res.Cold = m
	if m.TTFTSec > 0 {
		res.PrefillTPS = float64(m.PromptTokens) / m.TTFTSec
	}

	follow := &llm.Request{Messages: append(append([]llm.Message{}, cold.Messages...),
		llm.Message{Role: llm.RoleAssistant, Content: reply},
		llm.Message{Role: llm.RoleUser, Content: "Here is the output of the test run:\n" + syntheticLog(o.FollowUpTokens) + "\nWhich test failed and why? Answer briefly."},
	), MaxTokens: o.OutputTokens, Temperature: &zero, Stream: true}
	fmt.Fprintf(o.Progress, "  3/3 follow-up turn with ~%d new tokens (prompt-cache reuse)…\n", o.FollowUpTokens)
	m, _, err = measure(ctx, c, follow)
	if err != nil {
		return nil, err
	}
	res.FollowUp = m
	newTokens := m.PromptTokens - res.Cold.PromptTokens
	if newTokens <= 0 {
		newTokens = o.FollowUpTokens
	}
	if m.TTFTSec > 0 {
		res.FollowUpPrefillTPS = float64(newTokens) / m.TTFTSec
	}
	// Re-processing the whole conversation would take about the cold
	// prompt time scaled to the longer prompt; a cached follow-up takes a
	// fraction of it. Timings under ~1 s are dominated by fixed overheads,
	// so the check is only conclusive above that.
	full := res.Cold.TTFTSec * float64(m.PromptTokens) / float64(max(1, res.Cold.PromptTokens))
	switch {
	case m.TTFTSec < 0.35*full:
		res.CacheCheck = CacheReused
	case full < 1.0:
		res.CacheCheck = CacheInconclusive
		res.Notes = append(res.Notes, "prompt processing is too fast at this size to tell whether the cache was reused; rerun with --prompt-tokens 16000 or more")
	case m.TTFTSec < 0.6*full:
		res.CacheCheck = CacheReused
	default:
		res.CacheCheck = CacheNotReused
		res.Notes = append(res.Notes, "the follow-up re-processed the whole conversation: the runtime did not reuse its prompt cache")
	}
	res.CacheReused = res.CacheCheck == CacheReused
	if res.Cold.TokensEstimated || res.FollowUp.TokensEstimated {
		res.Notes = append(res.Notes, "the runtime reported no token usage; counts are estimates")
	}
	return res, nil
}

// measure streams one request and times it.
func measure(ctx context.Context, c upstream.Client, req *llm.Request) (Measurement, string, error) {
	start := time.Now()
	req.IncludeUsage = true
	events, err := c.Stream(ctx, req)
	if err != nil {
		return Measurement{}, "", err
	}
	var m Measurement
	var first, last time.Time
	var out strings.Builder
	pieces := 0
	var usage *llm.Usage
	for ev := range events {
		switch ev.Type {
		case llm.EventText, llm.EventReasoning, llm.EventToolCallArgs:
			now := time.Now()
			if first.IsZero() {
				first = now
			}
			last = now
			pieces += tok.Count(ev.Text)
			if ev.Type == llm.EventText {
				out.WriteString(ev.Text)
			}
		case llm.EventDone:
			usage = ev.Usage
		case llm.EventError:
			return m, "", ev.Err
		}
	}
	end := time.Now()
	m.TotalSec = end.Sub(start).Seconds()
	if !first.IsZero() {
		m.TTFTSec = first.Sub(start).Seconds()
	}
	if usage != nil && usage.PromptTokens > 0 {
		m.PromptTokens, m.CompletionTokens, m.CachedTokens = usage.PromptTokens, usage.CompletionTokens, usage.CachedPromptTokens
	} else {
		m.TokensEstimated = true
		m.PromptTokens = int(float64(tok.Render(req, tok.RenderOptions{StripPastReasoning: true}).Len()) * 0.8)
		m.CompletionTokens = int(float64(pieces) * 0.8)
	}
	if gen := last.Sub(first).Seconds(); gen > 0 && m.CompletionTokens > 1 {
		m.DecodeTPS = float64(m.CompletionTokens-1) / gen
	}
	return m, out.String(), nil
}

// syntheticContext builds deterministic code-like text of about n tokens.
func syntheticContext(n int) string {
	var b strings.Builder
	for i := 0; tok.Count(b.String()) < n; i++ {
		fmt.Fprintf(&b, "// file: internal/service/handler_%03d.go\n", i)
		fmt.Fprintf(&b, "func (s *Server) handleRequest%d(ctx context.Context, req *Request) (*Response, error) {\n", i)
		fmt.Fprintf(&b, "\tkey := fmt.Sprintf(\"req:%%s:%%d\", req.UserID, %d)\n", i*7+3)
		b.WriteString("\tif cached, ok := s.cache.Get(key); ok {\n\t\treturn cached.(*Response), nil\n\t}\n")
		fmt.Fprintf(&b, "\tfor attempt := 0; attempt < %d; attempt++ {\n", i%5+2)
		b.WriteString("\t\tresp, err := s.backend.Call(ctx, req)\n\t\tif err == nil {\n\t\t\ts.cache.Set(key, resp, time.Minute)\n\t\t\treturn resp, nil\n\t\t}\n")
		fmt.Fprintf(&b, "\t\ttime.Sleep(time.Duration(attempt*%d) * time.Millisecond)\n\t}\n", 50+i%9*10)
		fmt.Fprintf(&b, "\treturn nil, fmt.Errorf(\"handler %d: backend unavailable after retries\")\n}\n\n", i)
	}
	return b.String()
}

func syntheticLog(n int) string {
	var b strings.Builder
	for i := 0; tok.Count(b.String()) < n; i++ {
		status := "ok  "
		if i == 3 {
			status = "FAIL"
		}
		fmt.Fprintf(&b, "%s internal/service/handler_%03d_test.go:%d TestHandleRequest%d (%d.%02ds)\n", status, i, 40+i, i, i%3, i*7%100)
		if i == 3 {
			b.WriteString("    expected cached response, got backend call: cache key mismatch\n")
		}
	}
	return b.String()
}
