// Package sim simulates a local runtime on the user's device: model
// residency and keep-alive, context-window policy, prompt-cache slots with
// prefix reuse (and the rollback limits of sliding-window models), a RAM
// prompt cache, serial execution, and per-token pacing of the cloud's
// output to the predicted local speed.
package sim

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/aeroknight786/cyrlm/internal/catalog"
	"github.com/aeroknight786/cyrlm/internal/llm"
	"github.com/aeroknight786/cyrlm/internal/predict"
	"github.com/aeroknight786/cyrlm/internal/runtime"
	"github.com/aeroknight786/cyrlm/internal/tok"
)

// Clock abstracts time for tests.
type Clock interface {
	Now() time.Time
	// SleepUntil blocks until t or ctx is done.
	SleepUntil(ctx context.Context, t time.Time) error
	// Timer fires at t (nil channel = never, for virtual clocks).
	Timer(t time.Time) <-chan time.Time
}

// RealClock uses wall time.
type RealClock struct{}

// Now returns time.Now().
func (RealClock) Now() time.Time { return time.Now() }

// Timer fires at t.
func (RealClock) Timer(t time.Time) <-chan time.Time { return time.After(time.Until(t)) }

// SleepUntil sleeps until t.
func (RealClock) SleepUntil(ctx context.Context, t time.Time) error {
	d := time.Until(t)
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Config configures a simulated device.
type Config struct {
	RuntimeName string
	RuntimeID   string
	Settings    runtime.Settings
	Timing      *predict.Timing
	Model       catalog.Model
	Render      tok.RenderOptions
	Clock       Clock
	// InitialPromptRatio / InitialOutputRatio seed the token calibrators
	// (real tokens per estimated piece).
	InitialPromptRatio float64
	InitialOutputRatio float64
}

type slot struct {
	id       int
	seq      *tok.Seq
	promptN  int // length of the prompt part of seq (for explanations)
	lastUsed time.Time
	ckpts    []int
}

type ramEntry struct {
	seq   *tok.Seq
	bytes float64
}

// Device is the simulated local runtime state.
type Device struct {
	cfg Config
	sem chan struct{}

	mu         sync.Mutex
	slots      []*slot
	ram        []ramEntry
	loaded     bool
	everLoaded bool
	loadedCtx  int
	lastActive time.Time
	promptCal  *tok.Calibrator
	outCal     *tok.Calibrator
	stats      *Stats
	// evicted keeps recently overwritten conversations, only to explain
	// misses (they cannot be reused).
	evicted []*tok.Seq
}

// NewDevice creates a device. The model starts unloaded.
func NewDevice(cfg Config) *Device {
	if cfg.Clock == nil {
		cfg.Clock = RealClock{}
	}
	// The piece estimator over-splits relative to BPE tokenizers by ~25%;
	// the per-request read-ahead in Pace replaces this with exact counts
	// whenever the cloud finishes before the simulated first token.
	if cfg.InitialPromptRatio <= 0 {
		cfg.InitialPromptRatio = 0.8
	}
	if cfg.InitialOutputRatio <= 0 {
		cfg.InitialOutputRatio = 0.8
	}
	d := &Device{
		cfg:       cfg,
		sem:       make(chan struct{}, 1),
		promptCal: tok.NewCalibrator(cfg.InitialPromptRatio),
		outCal:    tok.NewCalibrator(cfg.InitialOutputRatio),
		stats:     newStats(),
	}
	for i := 0; i < max(1, cfg.Settings.Slots); i++ {
		d.slots = append(d.slots, &slot{id: i})
	}
	return d
}

// Config returns the device configuration.
func (d *Device) Config() Config { return d.cfg }

// Stats returns the session statistics.
func (d *Device) Stats() *Stats { return d.stats }

// ContextError is returned when the runtime would reject a prompt.
type ContextError struct {
	PromptTokens int
	Ctx          int
	Message      string
}

func (e *ContextError) Error() string { return e.Message }

// Prepared is a request after the runtime's context policy was applied.
type Prepared struct {
	Req          *llm.Request
	Seq          *tok.Seq
	PromptTokens int // calibrated real-token estimate
	Ctx          int
	Truncated    bool
	TruncNote    string
}

// Prepare applies the context window policy. It may return a modified copy
// of the request (truncation) so the cloud model sees exactly what the local
// runtime would have fed the model.
func (d *Device) Prepare(req *llm.Request) (*Prepared, error) {
	d.mu.Lock()
	ratio := d.promptCal.Ratio
	d.mu.Unlock()
	ctxN := d.contextFor(req)
	seq := tok.Render(req, d.cfg.Render)
	p := &Prepared{Req: req, Seq: seq, Ctx: ctxN, PromptTokens: int(float64(seq.Len()) * ratio)}
	if p.PromptTokens < ctxN {
		p.Req = clampMaxTokens(req, ctxN-p.PromptTokens)
		return p, nil
	}
	s := d.cfg.Settings
	switch s.Overflow {
	case runtime.OverflowError:
		return nil, &ContextError{PromptTokens: p.PromptTokens, Ctx: ctxN, Message: d.contextErrorMessage(p.PromptTokens, ctxN)}
	case runtime.OverflowTruncateMiddle, runtime.OverflowTruncateOldest:
		reserve := min(1024, ctxN/8)
		limit := int(float64(ctxN-reserve) / ratio)
		nreq, note := truncate(req, d.cfg.Render, limit, s.Overflow == runtime.OverflowTruncateMiddle)
		nseq := tok.Render(nreq, d.cfg.Render)
		p.PromptTokens = int(float64(nseq.Len()) * ratio)
		p.Req, p.Seq = clampMaxTokens(nreq, ctxN-p.PromptTokens), nseq
		p.Truncated = true
		p.TruncNote = fmt.Sprintf("%s truncated the prompt from ~%d to ~%d tokens to fit its %d-token context (%s). The model never saw the removed text.",
			d.cfg.RuntimeName, int(float64(seq.Len())*ratio), p.PromptTokens, ctxN, note)
		return p, nil
	}
	return p, nil
}

// clampMaxTokens caps the output request to the room left in the context,
// as local runtimes do (n_predict); cloud providers reject prompt +
// max_tokens > context.
func clampMaxTokens(req *llm.Request, room int) *llm.Request {
	if req.MaxTokens <= 0 || req.MaxTokens <= room {
		return req
	}
	r := *req
	r.MaxTokens = max(256, room)
	return &r
}

func (d *Device) contextErrorMessage(n, ctx int) string {
	switch d.cfg.RuntimeID {
	case "llamacpp":
		return fmt.Sprintf("the request exceeds the available context size, try increasing it (n_prompt_tokens=%d, n_ctx=%d)", n, ctx)
	}
	return fmt.Sprintf("prompt is too long: %d tokens > %d maximum", n, ctx)
}

func (d *Device) contextFor(req *llm.Request) int {
	c := d.cfg.Settings.ContextTokens
	if d.cfg.Settings.HonorsRequestNumCtx && req.NumCtx > 0 {
		c = req.NumCtx
		if n := d.cfg.Model.NativeCtx; n > 0 && c > n {
			c = n
		}
	}
	return c
}

// truncate drops conversation turns until the rendering fits limit
// (estimated tokens), keeping the result a valid conversation: leading
// system messages, the first user message (the task) and the latest turn
// are kept; an assistant message is dropped together with its tool results
// so no tool result is ever left without its tool call. If that is still
// too long, the system text is cut from the end, and only as a last resort
// the tool definitions.
func truncate(req *llm.Request, opt tok.RenderOptions, limit int, middle bool) (*llm.Request, string) {
	r := *req
	msgs := append([]llm.Message(nil), req.Messages...)
	nsys := 0
	for nsys < len(msgs) && msgs[nsys].Role == llm.RoleSystem {
		nsys++
	}
	keep := nsys
	if keep < len(msgs)-1 && msgs[keep].Role == llm.RoleUser {
		keep++ // the task statement
	}
	_ = middle // both policies keep the head and drop from the middle
	fits := func() bool {
		r.Messages = msgs
		return tok.Render(&r, opt).Len() <= limit
	}
	dropped := 0
	for !fits() {
		i := keep
		if i >= len(msgs)-1 {
			break
		}
		j := i + 1
		if msgs[i].Role == llm.RoleAssistant && len(msgs[i].ToolCalls) > 0 {
			for j < len(msgs) && msgs[j].Role == llm.RoleTool {
				j++
			}
		}
		if j >= len(msgs) {
			break // never drop the latest turn
		}
		msgs = append(msgs[:i:i], msgs[j:]...)
		dropped += j - i
		// A tool result must follow its tool call.
		for i < len(msgs)-1 && msgs[i].Role == llm.RoleTool {
			msgs = append(msgs[:i:i], msgs[i+1:]...)
			dropped++
		}
	}
	note := fmt.Sprintf("dropped %d earlier messages", dropped)
	if !fits() && nsys > 0 {
		over := tok.Render(&r, opt).Len() - limit
		sys := append([]llm.Message(nil), msgs[:nsys]...)
		for i := len(sys) - 1; i >= 0 && over > 0; i-- {
			pieces := tok.Pieces(sys[i].Content)
			cut := min(over, len(pieces))
			sys[i].Content = strings.Join(pieces[:len(pieces)-cut], "")
			over -= cut
		}
		msgs = append(sys, msgs[nsys:]...)
		note += " and cut the end of the system prompt"
		if !fits() && len(r.Tools) > 0 {
			r.Tools = nil
			note += " and the tool definitions"
		}
	}
	r.Messages = msgs
	return &r, note
}

// Acquire waits for the device (requests execute one at a time, like a
// single-GPU runtime with one active sequence). It returns how long the
// request queued.
func (d *Device) Acquire(ctx context.Context) (release func(), queued time.Duration, err error) {
	start := d.cfg.Clock.Now()
	select {
	case d.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	}
	return func() { <-d.sem }, d.cfg.Clock.Now().Sub(start), nil
}

// CacheMiss explains a prompt-cache miss.
type CacheMiss struct {
	At       int // token index of divergence
	Previous int // previous prompt length in the slot
	Reason   string
	Before   string // text just before the divergence
	Was      string // previous text at the divergence
	Now      string // new text at the divergence
}

// Plan is the simulated execution plan of one request.
type Plan struct {
	Start         time.Time
	Cold          bool
	LoadDur       time.Duration
	LoadReason    string
	Slot          int
	CachedTokens  int // real tokens reused
	PrefillTokens int // real tokens processed
	RestoreDur    time.Duration
	PrefillDur    time.Duration
	FirstTokenAt  time.Time
	PromptTokens  int
	DepthPieces   int // prompt length in estimated pieces
	Miss          *CacheMiss
	Notes         []string

	// Exact is true when token counts came from the upstream's usage
	// (read ahead before the first token) rather than the estimator.
	Exact bool
	// HiddenReasoningDur is decode time for reasoning tokens the provider
	// did not stream (simulated as a pause before the first token).
	HiddenReasoningDur time.Duration

	slot *slot
	seq  *tok.Seq
}

func (d *Device) hasRollbackLimits() bool {
	return d.cfg.Model.SWALayers > 0
}

// Plan computes load and prefill for a prepared request. Call after Acquire.
func (d *Device) Plan(p *Prepared) *Plan {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.cfg.Clock.Now()
	pl := &Plan{Start: now, seq: p.Seq, DepthPieces: p.Seq.Len()}
	s := d.cfg.Settings
	t := d.cfg.Timing

	// Residency.
	if d.loaded && s.KeepAlive >= 0 && now.Sub(d.lastActive) > s.KeepAlive {
		d.unload()
		pl.LoadReason = fmt.Sprintf("model was unloaded after %s idle (keep-alive)", s.KeepAlive)
	}
	if d.loaded && d.cfg.Settings.HonorsRequestNumCtx && p.Ctx != d.loadedCtx {
		d.unload()
		pl.LoadReason = fmt.Sprintf("context changed to %d: %s reloads the model", p.Ctx, d.cfg.RuntimeName)
	}
	if !d.loaded {
		pl.Cold = true
		load := t.LoadColdS
		if d.everLoaded {
			load = t.LoadWarmS
		}
		if pl.LoadReason == "" {
			pl.LoadReason = "first request: model loads from disk"
		}
		pl.LoadDur = secs(load)
		d.loaded, d.everLoaded, d.loadedCtx = true, true, p.Ctx
	}

	// Slot selection: best common prefix; below the similarity threshold
	// use the least-recently-used slot (llama-server behaviour).
	best, bestLCP := d.slots[0], -1
	for _, sl := range d.slots {
		if l := tok.LCP(sl.seq, p.Seq); l > bestLCP {
			best, bestLCP = sl, l
		}
	}
	if len(d.slots) > 1 && s.SlotSimilarity > 0 && float64(bestLCP) < s.SlotSimilarity*float64(p.Seq.Len()) {
		lru := d.slots[0]
		for _, sl := range d.slots {
			if sl.seq == nil {
				lru = sl
				break
			}
			if sl.lastUsed.Before(lru.lastUsed) {
				lru = sl
			}
		}
		if lru != best {
			best, bestLCP = lru, tok.LCP(lru.seq, p.Seq)
		}
	}
	pl.slot, pl.Slot = best, best.id
	reuse := d.rollback(best, bestLCP)

	// RAM prompt cache (llama-server --cache-ram).
	if s.RAMCacheBytes > 0 {
		for _, e := range d.ram {
			l := tok.LCP(e.seq, p.Seq)
			if l > reuse+256 {
				reuse = l
				kv := d.cfg.Model.KVCacheBytes(d.realTokens(l), t.KVElem)
				pl.RestoreDur = secs(kv / 20e9)
				pl.Notes = append(pl.Notes, "restored prompt cache from RAM")
			}
		}
	}
	if reuse >= p.Seq.Len() {
		reuse = p.Seq.Len() - 1
	}
	if reuse < 0 {
		reuse = 0
	}

	// Explain misses that throw away a large cached prefix.
	if best.seq != nil && best.promptN > 512 && reuse < best.promptN && float64(reuse) < 0.9*float64(min(best.promptN, p.Seq.Len())) {
		pl.Miss = d.explain(best, p.Seq, bestLCP, reuse)
	}
	// A conversation evicted by another one (background task, subagent).
	if pl.Miss == nil && p.Seq.Len() > 512 && float64(reuse) < 0.5*float64(p.Seq.Len()) {
		for _, ev := range d.evicted {
			if l := tok.LCP(ev, p.Seq); float64(l) > 0.5*float64(p.Seq.Len()) {
				reason := "a different request (background task or subagent) took the only cache slot and evicted this conversation"
				if len(d.slots) > 1 {
					reason = fmt.Sprintf("all %d cache slots were taken by other conversations, evicting this one", len(d.slots))
				}
				pl.Miss = &CacheMiss{At: reuse, Previous: l, Reason: reason}
				break
			}
		}
	}

	pl.PromptTokens = d.realTokens(p.Seq.Len())
	pl.CachedTokens = d.realTokens(reuse)
	pl.PrefillTokens = pl.PromptTokens - pl.CachedTokens
	pl.PrefillDur = secs(t.PrefillSec(pl.CachedTokens, pl.PromptTokens))
	pl.FirstTokenAt = now.Add(pl.LoadDur + pl.RestoreDur + pl.PrefillDur)
	return pl
}

// rollback applies sliding-window rollback limits to a common prefix.
func (d *Device) rollback(sl *slot, lcp int) int {
	if sl.seq == nil || lcp <= 0 {
		return 0
	}
	if lcp >= sl.seq.Len() || !d.hasRollbackLimits() {
		return lcp
	}
	switch d.cfg.Settings.SWARollback {
	case runtime.RollbackNone:
		return 0
	case runtime.RollbackCheckpoints:
		best := 0
		for _, c := range sl.ckpts {
			if c <= lcp && c > best {
				best = c
			}
		}
		return best
	}
	return lcp
}

func (d *Device) explain(sl *slot, seq *tok.Seq, lcp, reuse int) *CacheMiss {
	m := &CacheMiss{At: lcp, Previous: sl.promptN}
	before, was := sl.seq.Context(lcp, 12)
	_, now := seq.Context(lcp, 12)
	m.Before, m.Was, m.Now = before, was, now
	sysEnd := 0
	for i, pc := range seq.Pieces {
		if pc == "<|end|>" {
			sysEnd = i
			break
		}
	}
	sameDoc := resync(sl.seq, seq, lcp) || float64(lcp) >= 0.9*float64(min(sl.seq.Len(), seq.Len()))
	switch {
	case reuse < lcp:
		m.Reason = fmt.Sprintf("the first %d tokens matched, but this model's sliding-window layers can only roll back to a saved checkpoint, and none is at or before the divergence (%s keeps %d per slot) — the whole prompt is re-processed", lcp, d.cfg.RuntimeName, d.cfg.Settings.Checkpoints)
	case !sameDoc && len(d.slots) == 1:
		m.Reason = "a different conversation used the only cache slot (e.g. a background or subagent request) and evicted this one"
	case !sameDoc:
		m.Reason = "a different conversation replaced this one in the cache"
	case lcp <= sysEnd:
		m.Reason = "the system prompt or tool definitions changed (a per-request value such as a timestamp or attribution header breaks the cache)"
	default:
		m.Reason = "earlier conversation history was rewritten (e.g. compaction or edited messages)"
	}
	return m
}

// resync reports whether a and b continue to match shortly after diverging
// at i (same prompt with a local change) rather than being different texts.
func resync(a, b *tok.Seq, i int) bool {
	const probe, window = 64, 48
	start := i + 8
	if start+probe > a.Len() || start+probe > b.Len() {
		return false
	}
	for off := -window; off <= window; off++ {
		if start+off < 0 || start+off+probe > b.Len() {
			continue
		}
		hits := 0
		for j := 0; j < probe; j++ {
			if a.IDs[start+j] == b.IDs[start+off+j] {
				hits++
			}
		}
		if hits >= probe*3/4 {
			return true
		}
	}
	return false
}

// realTokens converts estimated pieces to calibrated real tokens. Caller
// holds d.mu.
func (d *Device) realTokens(pieces int) int {
	return int(float64(pieces)*d.promptCal.Ratio + 0.5)
}

func (d *Device) unload() {
	d.loaded = false
	for _, sl := range d.slots {
		sl.seq, sl.promptN, sl.ckpts = nil, 0, nil
	}
	d.ram = nil
	d.evicted = nil
}

// Commit records the outcome of a request in the device state.
func (d *Device) Commit(pl *Plan, resp *llm.Response, generatedPieces int, end time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	sl := pl.slot
	if sl == nil {
		return
	}
	// An evicted conversation goes to the RAM cache (when the runtime has
	// one) and to the explanation history.
	if sl.seq != nil && tok.LCP(sl.seq, pl.seq) < sl.seq.Len()/2 {
		if d.cfg.Settings.RAMCacheBytes > 0 {
			d.pushRAM(sl.seq)
		}
		d.evicted = append(d.evicted, sl.seq)
		if len(d.evicted) > 4 {
			d.evicted = d.evicted[1:]
		}
	}
	seq := pl.seq.Clone()
	ckpts := sl.ckpts
	if tok.LCP(sl.seq, pl.seq) < pl.seq.Len()/2 {
		ckpts = nil
	}
	if resp != nil {
		tok.RenderAssistantBody(seq, resp.Reasoning, resp.Content, resp.ToolCalls, true)
		seq.AppendSpecial("<|end|>")
		if resp.Usage.PromptTokens > 0 {
			d.promptCal.Observe(resp.Usage.PromptTokens, pl.seq.Len())
		}
		if resp.Usage.CompletionTokens > 0 && generatedPieces > 0 {
			d.outCal.Observe(resp.Usage.CompletionTokens, generatedPieces)
		}
	}
	ckpts = append(ckpts, pl.seq.Len())
	if n := d.cfg.Settings.Checkpoints; n > 0 && len(ckpts) > n {
		ckpts = ckpts[len(ckpts)-n:]
	}
	sl.seq, sl.promptN, sl.ckpts, sl.lastUsed = seq, pl.seq.Len(), ckpts, end
	d.lastActive = end
}

func (d *Device) pushRAM(seq *tok.Seq) {
	b := d.cfg.Model.KVCacheBytes(d.realTokens(seq.Len()), d.cfg.Timing.KVElem)
	d.ram = append(d.ram, ramEntry{seq: seq, bytes: b})
	total := 0.0
	for _, e := range d.ram {
		total += e.bytes
	}
	for total > d.cfg.Settings.RAMCacheBytes && len(d.ram) > 0 {
		total -= d.ram[0].bytes
		d.ram = d.ram[1:]
	}
}

// OutputRatio returns the current real-tokens-per-piece ratio for output.
func (d *Device) OutputRatio() float64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.outCal.Ratio
}

// PromptRatio returns the current prompt calibration ratio.
func (d *Device) PromptRatio() float64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.promptCal.Ratio
}

// Loaded reports residency and when the model would unload.
func (d *Device) Loaded() (loaded bool, expires time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.loaded {
		return false, time.Time{}
	}
	if d.cfg.Settings.KeepAlive < 0 {
		return true, time.Time{}
	}
	exp := d.lastActive.Add(d.cfg.Settings.KeepAlive)
	if d.cfg.Clock.Now().After(exp) {
		return false, time.Time{}
	}
	return true, exp
}

func secs(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }
