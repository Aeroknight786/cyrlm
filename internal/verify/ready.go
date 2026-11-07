package verify

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/aeroknight786/trialm/internal/figure"
	"github.com/aeroknight786/trialm/internal/harness"
	"github.com/aeroknight786/trialm/internal/llm"
	"github.com/aeroknight786/trialm/internal/tok"
	"github.com/aeroknight786/trialm/internal/upstream"
)

type Predicted struct {
	DecodeTPS0      float64
	DecodeTPSTarget float64
	PrefillTPS      float64
	// BandPct controls the displayed precision of prediction misses.
	BandPct int
}
type ReadyOptions struct {
	Endpoint      string
	Engine        string // engine base URL for engine-native probes; "" = Endpoint
	Model         string
	APIKey        string
	ContextTokens int
	BitsPerWeight float64
	Predicted     Predicted
	Harness       *harness.Harness
	APIs          []string
	Progress      func(string)
	// UBatch is the configured prompt processing batch size.
	UBatch int
}
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}
type Report struct {
	Ready          bool            `json:"ready"`
	Checks         []Check         `json:"checks"`
	Failing        string          `json:"failing,omitempty"`
	DecodeTPS      map[int]float64 `json:"decode_tps"`
	PrefillTPS     float64         `json:"prefill_tps"`
	FollowUpTTFT   float64         `json:"follow_up_ttft_s"`
	SpecVerifySecs []float64       `json:"spec_verify_s,omitempty"`
	Misses         []string        `json:"misses,omitempty"`
}

func (r *Report) add(o ReadyOptions, name string, ok bool, detail string) {
	r.Checks = append(r.Checks, Check{name, ok, detail})
	if !ok && r.Failing == "" {
		r.Failing = name
	}
	if o.Progress != nil {
		o.Progress(name + ": " + detail)
	}
}
func (r Report) Plain() string {
	var b strings.Builder
	for _, c := range r.Checks {
		mark := "✗"
		if c.OK {
			mark = "✓"
		}
		fmt.Fprintf(&b, "%s %s: %s\n", mark, c.Name, c.Detail)
	}
	for _, m := range r.Misses {
		fmt.Fprintln(&b, m)
	}
	return b.String()
}
func (r *Report) miss(name string, predicted, actual float64, band int) {
	if predicted <= 0 || actual <= 0 {
		return
	}
	pct := 100 * (predicted/actual - 1)
	direction := "optimistic"
	if pct < 0 {
		direction = "pessimistic"
	}
	r.Misses = append(r.Misses, fmt.Sprintf("we were %s%% %s on %s", figure.BandNumber(math.Abs(pct), band), direction, name))
}

func Ready(ctx context.Context, o ReadyOptions) (Report, error) {
	r := Report{DecodeTPS: map[int]float64{}}
	c := &upstream.OpenAICompat{BaseURL: strings.TrimRight(o.Endpoint, "/") + "/v1", APIKey: o.APIKey, Model: o.Model}
	zero := 0.0
	req := func(text string, n int) *llm.Request {
		return &llm.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: text}}, MaxTokens: n, Temperature: &zero, Stream: true}
	}
	warm, _, err := measure(ctx, c, req("Reply OK.", 8))
	if err != nil {
		r.add(o, "warmup", false, err.Error())
		return r, nil
	}
	r.add(o, "warmup", warm.TTFTSec > 0, fmt.Sprintf("first token in %s", figure.BandNumber(warm.TTFTSec, 20)+" s"))
	// Deep requests run last: after one, llama-server saves the long slot to
	// its RAM prompt cache when the next prompt arrives, which would add
	// seconds to the reading and follow-up timings.
	var deep []int
	if o.ContextTokens >= 8192 {
		deep = append(deep, 8192)
	}
	if o.ContextTokens > 0 && o.ContextTokens != 8192 {
		deep = append(deep, o.ContextTokens)
	}
	decodeAt := func(depths []int) bool {
		for _, depth := range depths {
			ask := "Write a detailed explanation of a software cache."
			m, _, e := fitted(ctx, c, depth, func(n int) *llm.Request {
				if n <= 0 {
					return req(ask, 32)
				}
				return req(contextOf(n)+"\n"+ask, 32)
			})
			if e != nil || m.DecodeTPS <= 0 {
				detail := "no decode timing"
				if e != nil {
					detail = e.Error()
				}
				r.add(o, "decode", false, detail)
				return false
			}
			r.DecodeTPS[depth] = m.DecodeTPS
			detail := fmt.Sprintf("writing %s tok/s at %d tokens", figure.BandNumber(m.DecodeTPS, 20), depth)
			if m.TokensEstimated {
				detail += " (token count estimated)"
			}
			r.add(o, "decode", true, detail)
		}
		return true
	}
	if !decodeAt([]int{0}) {
		return r, nil
	}
	n := o.UBatch
	if n <= 0 {
		n = 512
	}
	if o.ContextTokens > 0 {
		n = min(n, max(1, o.ContextTokens-256))
	}
	text := syntheticContext(n)
	m, reply, e := fitted(ctx, c, n+256, func(k int) *llm.Request { text = contextOf(max(1, k)); return req(text, 16) })
	if e != nil || m.TTFTSec <= 0 {
		detail := "no prompt timing"
		if e != nil {
			detail = e.Error()
		}
		r.add(o, "prefill", false, detail)
		return r, nil
	}
	r.PrefillTPS = float64(m.PromptTokens) / m.TTFTSec
	detail := fmt.Sprintf("reading %s tok/s", figure.BandNumber(r.PrefillTPS, 20))
	if m.TokensEstimated {
		detail += " (token count estimated)"
	}
	r.add(o, "prefill", true, detail)
	follow := req(text, 8)
	follow.Messages = append(follow.Messages, llm.Message{Role: llm.RoleAssistant, Content: reply}, llm.Message{Role: llm.RoleUser, Content: "Continue briefly."})
	fm, _, e := measure(ctx, c, follow)
	if e != nil || fm.TTFTSec <= 0 {
		detail := "no follow-up timing"
		if e != nil {
			detail = e.Error()
		}
		r.add(o, "follow_up", false, detail)
		return r, nil
	}
	r.FollowUpTTFT = fm.TTFTSec
	cache := "unknown"
	if fm.CacheKnown {
		cache = "not reused (warning)"
		if fm.CachedTokens > 0 {
			cache = "reused"
		}
	}
	r.add(o, "follow_up", true, fmt.Sprintf("first token in %s s; cache %s", figure.BandNumber(fm.TTFTSec, 20), cache))
	// Each measurement must report both newly processed and cached tokens.
	prefix := strings.Repeat("cache ", 64)
	_, _, e = measure(ctx, c, req(prefix, 1))
	reason := ""
	if e != nil {
		reason = e.Error()
	}
	for i := 1; i <= 4 && reason == ""; i++ {
		// The raw endpoint has no chat template, so the engine processes
		// exactly i new tokens after the cached prefix (reset first).
		raw := func(p string) (map[string]any, error) {
			return postJSON(ctx, strings.TrimRight(cmp.Or(o.Engine, o.Endpoint), "/")+"/completion", o.APIKey, map[string]any{"prompt": p, "n_predict": 1, "cache_prompt": true, "temperature": 0})
		}
		out, err := raw(prefix)
		if err == nil {
			out, err = raw(prefix + strings.Repeat(" x", i))
		}
		if err != nil {
			reason = err.Error()
			break
		}
		t, _ := out["timings"].(map[string]any)
		n, nok := t["prompt_n"].(float64)
		cached, cok := t["cache_n"].(float64)
		ms, mok := t["prompt_ms"].(float64)
		if !nok || !cok || !mok || cached <= 0 || n != float64(i) || ms <= 0 {
			reason = fmt.Sprintf("engine did not confirm %d new tokens and cache reuse", i)
			break
		}
		r.SpecVerifySecs = append(r.SpecVerifySecs, ms/1000)
	}
	if reason != "" {
		r.SpecVerifySecs = nil
		r.add(o, "spec_verify", true, "inconclusive: "+reason)
	} else {
		r.add(o, "spec_verify", true, "measured batches 1–4; engine confirmed new token counts")
	}
	if o.Harness != nil {
		h := HarnessCheckRun(ctx, o.Endpoint, o.Model, o.APIKey, *o.Harness, o.APIs)
		detail := h.Path
		if h.Error != "" {
			detail = h.Error
		}
		r.add(o, "harness", h.Passed, detail)
	}
	if o.BitsPerWeight > 0 && o.BitsPerWeight < 3 {
		ok, detail := toolCalls(ctx, o)
		r.add(o, "tool_calls", ok, detail)
	}
	if !decodeAt(deep) {
		return r, nil
	}
	target := o.Predicted.DecodeTPSTarget
	if target > 0 && o.ContextTokens > 0 {
		actual := r.DecodeTPS[o.ContextTokens]
		ok := actual >= target*.5
		detail := "writing at depth is within the spill threshold"
		if !ok {
			detail = "writing at depth is below 50% of predicted; likely spill or mmap thrash"
		}
		r.add(o, "spill", ok, detail)
	}
	r.miss("writing near depth 0", o.Predicted.DecodeTPS0, r.DecodeTPS[0], o.Predicted.BandPct)
	if o.ContextTokens > 0 {
		r.miss("writing at depth", target, r.DecodeTPS[o.ContextTokens], o.Predicted.BandPct)
	}
	r.miss("prompt reading", o.Predicted.PrefillTPS, r.PrefillTPS, o.Predicted.BandPct)
	r.Ready = r.Failing == ""
	return r, nil
}

func toolCalls(ctx context.Context, o ReadyOptions) (bool, string) {
	tools := []any{map[string]any{"type": "function", "function": map[string]any{"name": "record_result", "parameters": map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}, "required": []string{"value"}}}}}
	for i := 0; i < 20; i++ {
		out, err := postJSON(ctx, strings.TrimRight(o.Endpoint, "/")+"/v1/chat/completions", o.APIKey, map[string]any{"model": o.Model, "messages": []any{map[string]any{"role": "user", "content": checkPrompt}}, "tools": tools, "tool_choice": "auto", "stream": false, "max_tokens": 128})
		if err != nil {
			return false, err.Error()
		}
		calls, _ := chatMessage(out)["tool_calls"].([]any)
		if len(calls) != 1 {
			return false, fmt.Sprintf("call %d: expected one tool call", i+1)
		}
		call, _ := calls[0].(map[string]any)
		fn, _ := call["function"].(map[string]any)
		args, _ := fn["arguments"].(string)
		var parsed map[string]any
		if fn["name"] != "record_result" || json.Unmarshal([]byte(args), &parsed) != nil || parsed["value"] != "ok" {
			return false, fmt.Sprintf("call %d: invalid tool arguments", i+1)
		}
	}
	return true, "20 tool calls parsed"
}

// tooLong reads the token counts from a llama.cpp-style context error:
// "request (1439 tokens) exceeds the available context size (512 tokens)".
var tooLong = regexp.MustCompile(`\((\d+) tokens\)[^(]*\((\d+) tokens\)`)

// fitted measures a prompt aimed at depth tokens. triaLM's token estimate
// can be off for the engine's tokenizer, so it aims at 90% of the depth and,
// when the engine reports the request as too long, rescales once from the
// engine's own counts.
func fitted(ctx context.Context, c upstream.Client, depth int, build func(n int) *llm.Request) (Measurement, string, error) {
	n := 0
	if depth > 0 {
		n = max(1, min(depth-256, depth*9/10))
	}
	m, reply, e := measure(ctx, c, build(n))
	for try := 0; try < 2 && e != nil && n > 1; try++ {
		g := tooLong.FindStringSubmatch(e.Error())
		if g == nil {
			break
		}
		sent, avail := atoi(g[1]), atoi(g[2])
		if sent <= 0 || avail <= 256 {
			break
		}
		n = max(1, int(float64(n)*float64(avail-256)/float64(sent)*0.95))
		m, reply, e = measure(ctx, c, build(n))
	}
	return m, reply, e
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

// contextOf is synthetic code context cut to about n estimated tokens
// (syntheticContext grows in whole ~150-token blocks).
func contextOf(n int) string {
	s := syntheticContext(n)
	if c := tok.Count(s); c > n {
		s = s[:len(s)*n/c]
	}
	return s
}
