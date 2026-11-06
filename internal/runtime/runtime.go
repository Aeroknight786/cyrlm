// Package runtime models local LLM runtimes (Ollama, llama.cpp, LM Studio,
// MLX) as versioned data: which platforms and backends they support, their
// default settings, their prompt-cache behaviour and the APIs they expose.
// Profiles live in profiles.json so they can be updated without code
// changes; every setting carries a confidence level shown to the user.
package runtime

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

//go:embed profiles.json
var profilesJSON []byte

// Overflow policies when a prompt exceeds the context window.
const (
	OverflowError          = "error"
	OverflowTruncateOldest = "truncate_oldest"
	OverflowTruncateMiddle = "truncate_middle"
)

// Behaviour when generation fills the context.
const (
	FullStop  = "stop"
	FullShift = "shift"
)

// Rollback modes for models with sliding-window or recurrent layers, whose
// KV cache cannot simply be truncated to an arbitrary prefix.
const (
	RollbackFree        = "free"        // reuse any common prefix
	RollbackCheckpoints = "checkpoints" // only up to a saved checkpoint
	RollbackNone        = "none"        // any divergence re-processes everything
)

// ContextRule describes how a runtime chooses its default context window.
type ContextRule struct {
	VRAMTiers []struct {
		BelowGiB float64 `json:"below_gib"`
		Tokens   int     `json:"tokens"`
	} `json:"vram_tiers,omitempty"`
	OtherwiseTokens int    `json:"otherwise_tokens,omitempty"`
	FixedTokens     int    `json:"fixed_tokens,omitempty"`
	FitToMemory     bool   `json:"fit_to_memory,omitempty"`
	Native          bool   `json:"native,omitempty"`
	Confidence      string `json:"confidence"`
	Source          string `json:"source,omitempty"`
}

// Defaults are a runtime's out-of-the-box settings.
type Defaults struct {
	Context             ContextRule `json:"context"`
	HonorsRequestNumCtx bool        `json:"honors_request_num_ctx"`
	KeepAliveS          int         `json:"keep_alive_s"` // -1 = never unload
	Slots               int         `json:"slots"`
	SlotSimilarity      float64     `json:"slot_similarity,omitempty"`
	KVCacheType         string      `json:"kv_cache_type"`
	FlashAttention      bool        `json:"flash_attention"`
	Overflow            string      `json:"overflow"`
	OnContextFull       string      `json:"on_context_full"`
	SWARollback         string      `json:"swa_rollback"`
	Checkpoints         int         `json:"checkpoints"`
	RAMCacheGiB         float64     `json:"ram_cache_gib"`
}

// Tuned are the settings cyrlm recommends for agentic use.
type Tuned struct {
	ContextTokens  int      `json:"context_tokens,omitempty"`
	KeepAliveS     *int     `json:"keep_alive_s,omitempty"`
	FlashAttention *bool    `json:"flash_attention,omitempty"`
	Steps          []string `json:"steps"`
}

// Perf are runtime-level multipliers on top of the engine/backend model.
type Perf struct {
	DecodeMult    float64 `json:"decode_mult"`
	PrefillMult   float64 `json:"prefill_mult"`
	LoadOverheadS float64 `json:"load_overhead_s"`
	Confidence    string  `json:"confidence"`
}

// Profile is one runtime.
type Profile struct {
	ID                string              `json:"id"`
	Name              string              `json:"name"`
	Versions          string              `json:"versions"`
	Engine            string              `json:"engine"` // "llamacpp" or "mlx"
	Formats           []string            `json:"formats"`
	Platforms         map[string][]string `json:"platforms"`
	BackendPreference []string            `json:"backend_preference"`
	APIs              []string            `json:"apis"`
	Port              int                 `json:"port"`
	Defaults          Defaults            `json:"defaults"`
	Confidence        map[string]string   `json:"confidence"`
	Tuned             Tuned               `json:"tuned"`
	Perf              Perf                `json:"perf"`
	Notes             []string            `json:"notes"`
}

type file struct {
	SchemaVersion int       `json:"schema_version"`
	Updated       string    `json:"updated"`
	Profiles      []Profile `json:"profiles"`
}

var (
	profiles []Profile
	updated  string
)

func init() {
	var f file
	if err := json.Unmarshal(profilesJSON, &f); err != nil {
		panic("runtime: invalid embedded profiles.json: " + err.Error())
	}
	profiles = f.Profiles
	updated = f.Updated
}

// All returns every profile.
func All() []Profile { return append([]Profile(nil), profiles...) }

// Updated returns the profile data date.
func Updated() string { return updated }

// Get returns a profile by id (case-insensitive; accepts "llama.cpp",
// "llama-server", "lm-studio", "mlx").
func Get(id string) (Profile, error) {
	n := strings.ToLower(strings.TrimSpace(id))
	switch n {
	case "llama.cpp", "llama-server", "llama_cpp":
		n = "llamacpp"
	case "lm-studio", "lms", "lm studio":
		n = "lmstudio"
	case "mlx", "mlx_lm", "mlxlm":
		n = "mlx-lm"
	}
	for _, p := range profiles {
		if p.ID == n {
			return p, nil
		}
	}
	ids := make([]string, len(profiles))
	for i, p := range profiles {
		ids[i] = p.ID
	}
	return Profile{}, fmt.Errorf("unknown runtime %q (known: %s)", id, strings.Join(ids, ", "))
}

// PlatformKey returns the profile platform key for an OS/arch.
func PlatformKey(goos, goarch string) string {
	if goos == "darwin" {
		return "darwin/" + goarch
	}
	return goos
}

// Backends returns the backends this runtime supports on a platform.
func (p Profile) Backends(goos, goarch string) []string {
	if b, ok := p.Platforms[PlatformKey(goos, goarch)]; ok {
		return b
	}
	return p.Platforms[goos]
}

// ChooseBackend picks the runtime's preferred backend among those the
// platform and hardware support. hwBackends are the device's capabilities
// (e.g. ["cuda","vulkan"]); "cpu" is always possible where listed.
func (p Profile) ChooseBackend(goos, goarch string, hwBackends []string) (string, bool) {
	plat := p.Backends(goos, goarch)
	has := func(list []string, b string) bool {
		for _, x := range list {
			if x == b {
				return true
			}
		}
		return false
	}
	for _, b := range p.BackendPreference {
		if !has(plat, b) {
			continue
		}
		if b == "cpu" || b == "mlx" && has(hwBackends, "metal") || has(hwBackends, b) {
			return b, true
		}
	}
	return "", false
}

// Settings are the resolved settings used by the simulator.
type Settings struct {
	ContextTokens       int
	HonorsRequestNumCtx bool
	// KeepAlive <0 means never unload.
	KeepAlive      time.Duration
	Slots          int
	SlotSimilarity float64
	KVCacheType    string
	FlashAttention bool
	Overflow       string
	OnContextFull  string
	SWARollback    string
	Checkpoints    int
	RAMCacheBytes  float64
	Tuned          bool
	// Assumptions lists low-confidence settings in effect.
	Assumptions []string
	// ContextNote explains how the context window was chosen.
	ContextNote string
}

// KVElemBytes returns bytes per KV element for the cache type.
func KVElemBytes(t string) float64 {
	switch strings.ToLower(t) {
	case "q8_0":
		return 1.0625
	case "q4_0":
		return 0.5625
	case "f32":
		return 4
	}
	return 2
}

// ResolveInput carries what Resolve needs to know about the target.
type ResolveInput struct {
	// GPUMemGiB is the GPU memory that runtime tiers look at (VRAM, or the
	// unified-memory budget).
	GPUMemGiB float64
	// NativeCtx is the model's trained context.
	NativeCtx int
	// MaxFitCtx is the largest context that fits memory (0 = unknown).
	MaxFitCtx int
	// Tuned selects cyrlm's recommended settings.
	Tuned bool
	// CtxOverride forces a context (0 = none).
	CtxOverride int
	// RecommendedCtx is the context a tuned setup should use (0 = profile's).
	RecommendedCtx int
}

// Resolve computes simulator settings for this runtime on a target.
func (p Profile) Resolve(in ResolveInput) Settings {
	d := p.Defaults
	s := Settings{
		HonorsRequestNumCtx: d.HonorsRequestNumCtx,
		KeepAlive:           seconds(d.KeepAliveS),
		Slots:               max(1, d.Slots),
		SlotSimilarity:      d.SlotSimilarity,
		KVCacheType:         d.KVCacheType,
		FlashAttention:      d.FlashAttention,
		Overflow:            d.Overflow,
		OnContextFull:       d.OnContextFull,
		SWARollback:         d.SWARollback,
		Checkpoints:         d.Checkpoints,
		RAMCacheBytes:       d.RAMCacheGiB * (1 << 30),
		Tuned:               in.Tuned,
	}
	if s.KVCacheType == "" {
		s.KVCacheType = "f16"
	}
	// Default context.
	c := d.Context
	switch {
	case len(c.VRAMTiers) > 0:
		s.ContextTokens = c.OtherwiseTokens
		for _, t := range c.VRAMTiers {
			if in.GPUMemGiB < t.BelowGiB {
				s.ContextTokens = t.Tokens
				break
			}
		}
		s.ContextNote = fmt.Sprintf("%s default for %.0f GiB of GPU memory", p.Name, in.GPUMemGiB)
	case c.FixedTokens > 0:
		s.ContextTokens = c.FixedTokens
		s.ContextNote = p.Name + " default load setting"
	case c.FitToMemory:
		s.ContextTokens = in.NativeCtx
		if in.MaxFitCtx > 0 && in.MaxFitCtx < s.ContextTokens {
			s.ContextTokens = in.MaxFitCtx
		}
		s.ContextNote = "model native context, reduced to fit free memory"
	default:
		s.ContextTokens = in.NativeCtx
		s.ContextNote = "model native context (memory-limited)"
	}
	if c.Confidence == "low" {
		s.Assumptions = append(s.Assumptions, "default context: "+c.Source)
	}
	if in.Tuned {
		t := p.Tuned
		ctx := t.ContextTokens
		if in.RecommendedCtx > 0 {
			ctx = in.RecommendedCtx
		}
		if ctx > 0 {
			s.ContextTokens = ctx
			s.ContextNote = "tuned for agentic use"
		}
		if t.KeepAliveS != nil {
			s.KeepAlive = seconds(*t.KeepAliveS)
		}
		if t.FlashAttention != nil {
			s.FlashAttention = *t.FlashAttention
		}
	}
	if in.CtxOverride > 0 {
		s.ContextTokens = in.CtxOverride
		s.ContextNote = "set explicitly"
	}
	if in.NativeCtx > 0 && s.ContextTokens > in.NativeCtx {
		s.ContextTokens = in.NativeCtx
		s.ContextNote += " (capped at the model's native context)"
	}
	if s.ContextTokens <= 0 {
		s.ContextTokens = 4096
	}
	keys := make([]string, 0, len(p.Confidence))
	for k := range p.Confidence {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if p.Confidence[k] == "low" {
			s.Assumptions = append(s.Assumptions, fmt.Sprintf("%s=%v (unverified for this version)", k, p.settingValue(k)))
		}
	}
	return s
}

func (p Profile) settingValue(k string) any {
	d := p.Defaults
	switch k {
	case "keep_alive_s":
		return d.KeepAliveS
	case "slots":
		return d.Slots
	case "slot_similarity":
		return d.SlotSimilarity
	case "kv_cache_type":
		return d.KVCacheType
	case "flash_attention":
		return d.FlashAttention
	case "overflow":
		return d.Overflow
	case "on_context_full":
		return d.OnContextFull
	case "swa_rollback":
		return d.SWARollback
	case "checkpoints":
		return d.Checkpoints
	case "ram_cache_gib":
		return d.RAMCacheGiB
	case "otherwise_tokens":
		return d.Context.OtherwiseTokens
	}
	return "?"
}

func seconds(s int) time.Duration {
	if s < 0 {
		return -1
	}
	return time.Duration(s) * time.Second
}

// TunedSteps returns the tuning steps with {{ctx}} substituted.
func (p Profile) TunedSteps(ctx int) []string {
	out := make([]string, len(p.Tuned.Steps))
	for i, s := range p.Tuned.Steps {
		out[i] = strings.ReplaceAll(s, "{{ctx}}", fmt.Sprint(ctx))
	}
	return out
}

// SupportsAPI reports whether the real runtime exposes a protocol.
func (p Profile) SupportsAPI(protocol string) bool {
	for _, a := range p.APIs {
		if a == protocol {
			return true
		}
	}
	return false
}
