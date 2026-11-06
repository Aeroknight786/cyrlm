package sim

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aeroknight786/cyrlm/internal/catalog"
	"github.com/aeroknight786/cyrlm/internal/llm"
	"github.com/aeroknight786/cyrlm/internal/predict"
	"github.com/aeroknight786/cyrlm/internal/runtime"
	"github.com/aeroknight786/cyrlm/internal/tok"
)

// fakeClock advances instantly on SleepUntil.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) SleepUntil(ctx context.Context, t time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	if t.After(c.now) {
		c.now = t
	}
	c.mu.Unlock()
	return nil
}

func (c *fakeClock) Timer(time.Time) <-chan time.Time { return nil }

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func testModel(swa bool) catalog.Model {
	m, _ := catalog.Lookup("llama3.1-8b")
	if swa {
		m, _ = catalog.Lookup("gpt-oss-20b")
	}
	return m
}

func testTiming(m catalog.Model) *predict.Timing {
	return &predict.Timing{
		Model: m, WeightsBytesPerTok: 1e9, KVElem: 2, EffBandwidth: 100e9, T0: 0,
		MatmulSecPerTok: 0.001, AttnFLOPSEff: 1e18, DecodeMult: 1, PrefillMult: 1,
		LoadColdS: 5, LoadWarmS: 1,
	}
}

func testDevice(t *testing.T, profile string, swa bool, mutate func(*runtime.Settings)) (*Device, *fakeClock) {
	t.Helper()
	p, err := runtime.Get(profile)
	if err != nil {
		t.Fatal(err)
	}
	m := testModel(swa)
	s := p.Resolve(runtime.ResolveInput{GPUMemGiB: 24, NativeCtx: m.NativeCtx, CtxOverride: 65536})
	if mutate != nil {
		mutate(&s)
	}
	clk := newFakeClock()
	d := NewDevice(Config{RuntimeName: p.Name, RuntimeID: p.ID, Settings: s, Timing: testTiming(m), Model: m,
		Render: tok.RenderOptions{StripPastReasoning: true}, Clock: clk})
	return d, clk
}

var bigSystem = strings.Repeat("You are an autonomous coding agent working in a repository. Follow the rules. ", 400)

func baseReq(user string) *llm.Request {
	return &llm.Request{Messages: []llm.Message{{Role: llm.RoleSystem, Content: bigSystem}, {Role: llm.RoleUser, Content: user}}, Stream: true}
}

// feed builds a pre-stamped event channel.
func feed(at time.Time, evs ...llm.Event) <-chan Timed {
	ch := make(chan Timed, len(evs))
	for _, e := range evs {
		ch <- Timed{Ev: e, At: at}
	}
	close(ch)
	return ch
}

func textReply(s string) []llm.Event {
	return []llm.Event{{Type: llm.EventText, Text: s}, {Type: llm.EventDone, FinishReason: llm.FinishStop}}
}

// run executes one request end to end against the device.
func run(t *testing.T, d *Device, clk *fakeClock, req *llm.Request, evs []llm.Event) (*Plan, PaceResult) {
	t.Helper()
	p, err := d.Prepare(req)
	if err != nil {
		t.Fatal(err)
	}
	rel, _, err := d.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer rel()
	pl := d.Plan(p)
	res := d.Pace(context.Background(), pl, p, feed(clk.Now(), evs...), func(llm.Event) error { return nil })
	d.Commit(pl, &res.Resp, res.GeneratedPieces, clk.Now())
	return pl, res
}

func approx(a, b, tol float64) bool { return math.Abs(a-b) <= tol*math.Max(math.Abs(a), math.Abs(b)) }

func TestColdThenWarmPrefixReuse(t *testing.T) {
	d, clk := testDevice(t, "llamacpp", false, nil)
	req := baseReq("fix the failing test")
	pl, res := run(t, d, clk, req, textReply("I will look at the test file first."))
	if !pl.Cold || pl.LoadDur != 5*time.Second {
		t.Fatalf("first request must cold-load 5s, got cold=%v load=%v", pl.Cold, pl.LoadDur)
	}
	if pl.CachedTokens != 0 {
		t.Fatalf("nothing cached on first request, got %d", pl.CachedTokens)
	}
	wantPrefill := testTiming(d.cfg.Model).PrefillSec(0, pl.PromptTokens)
	if !approx(pl.PrefillDur.Seconds(), wantPrefill, 0.01) {
		t.Fatalf("prefill %.3f s, want %.3f", pl.PrefillDur.Seconds(), wantPrefill)
	}
	ttft := res.FirstTokenAt.Sub(pl.Start).Seconds()
	minTTFT := 5 + wantPrefill
	if ttft < minTTFT || ttft > minTTFT+0.1 {
		t.Fatalf("TTFT %.3f, want ≈ %.3f + one decode step", ttft, minTTFT)
	}

	// Turn 2: same history + reply + new user message → only the delta.
	req2 := baseReq("fix the failing test")
	req2.Messages = append(req2.Messages,
		llm.Message{Role: llm.RoleAssistant, Content: "I will look at the test file first."},
		llm.Message{Role: llm.RoleUser, Content: "the test is in pkg/foo_test.go"})
	pl2, _ := run(t, d, clk, req2, textReply("Looking."))
	if pl2.Cold {
		t.Fatal("second request must not reload")
	}
	if pl2.PrefillTokens > 40 || pl2.CachedTokens < pl.PromptTokens {
		t.Fatalf("turn 2 should reuse the whole previous prompt+reply: cached %d (prompt1 %d), prefilled %d", pl2.CachedTokens, pl.PromptTokens, pl2.PrefillTokens)
	}
	if pl2.Miss != nil {
		t.Fatalf("no cache miss expected, got %+v", pl2.Miss)
	}
}

func TestKeepAliveUnload(t *testing.T) {
	d, clk := testDevice(t, "ollama", false, nil) // 5 min keep-alive
	run(t, d, clk, baseReq("hello"), textReply("hi"))
	clk.Advance(6 * time.Minute)
	pl, _ := run(t, d, clk, baseReq("hello"), textReply("hi"))
	if !pl.Cold || pl.LoadDur != time.Second || pl.CachedTokens != 0 {
		t.Fatalf("after keep-alive expiry expect warm reload (1s) and no cache: cold=%v load=%v cached=%d", pl.Cold, pl.LoadDur, pl.CachedTokens)
	}
	if !strings.Contains(pl.LoadReason, "keep-alive") {
		t.Fatalf("load reason should mention keep-alive: %q", pl.LoadReason)
	}
}

func TestCacheMissExplainsSystemPromptChange(t *testing.T) {
	d, clk := testDevice(t, "llamacpp", false, func(s *runtime.Settings) { s.Slots = 1 })
	r1 := baseReq("task")
	r1.Messages[0].Content = "billing-header: cch=aaaa1111\n" + bigSystem
	run(t, d, clk, r1, textReply("ok"))
	r2 := baseReq("task")
	r2.Messages[0].Content = "billing-header: cch=bbbb2222\n" + bigSystem
	pl, _ := run(t, d, clk, r2, textReply("ok"))
	if pl.Miss == nil {
		t.Fatal("expected a cache miss")
	}
	if !strings.Contains(pl.Miss.Reason, "system prompt") {
		t.Fatalf("reason should blame the system prompt: %q", pl.Miss.Reason)
	}
	if pl.CachedTokens > 20 {
		t.Fatalf("almost nothing should be reused, got %d", pl.CachedTokens)
	}
}

func TestContextPolicies(t *testing.T) {
	long := baseReq("x")
	for i := 0; i < 30; i++ {
		long.Messages = append(long.Messages, llm.Message{Role: llm.RoleUser, Content: strings.Repeat("log line output ", 300)},
			llm.Message{Role: llm.RoleAssistant, Content: "noted"})
	}
	long.Messages = append(long.Messages, llm.Message{Role: llm.RoleUser, Content: "now summarize"})

	// llama.cpp rejects.
	d, _ := testDevice(t, "llamacpp", false, func(s *runtime.Settings) { s.ContextTokens = 8192 })
	_, err := d.Prepare(long)
	var ce *ContextError
	if !errors.As(err, &ce) || !strings.Contains(ce.Message, "exceeds the available context size") {
		t.Fatalf("llama.cpp should reject with its context error, got %v", err)
	}
	// Ollama truncates and keeps system + last message.
	o, _ := testDevice(t, "ollama", false, func(s *runtime.Settings) { s.ContextTokens = 8192 })
	p, err := o.Prepare(long)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Truncated || p.PromptTokens >= 8192 {
		t.Fatalf("expected truncation below 8192, got truncated=%v tokens=%d", p.Truncated, p.PromptTokens)
	}
	last := p.Req.Messages[len(p.Req.Messages)-1]
	if p.Req.Messages[0].Role != llm.RoleSystem || last.Content != "now summarize" {
		t.Fatal("truncation must keep the system prompt and the latest message")
	}
	if len(long.Messages) == len(p.Req.Messages) {
		t.Fatal("original request must not be modified / messages must be dropped")
	}
}

func TestSlidingWindowCheckpointRollback(t *testing.T) {
	// gpt-oss has sliding-window layers: after a reply with reasoning, the
	// next prompt diverges inside the cached generation; llama.cpp can only
	// roll back to the checkpoint at the end of the previous prompt.
	d, clk := testDevice(t, "llamacpp", true, func(s *runtime.Settings) { s.Slots = 1 })
	r1 := baseReq("explain the bug")
	pl1, _ := run(t, d, clk, r1, []llm.Event{{Type: llm.EventReasoning, Text: "Let me think about the call graph carefully."}, {Type: llm.EventText, Text: "The bug is in foo."}, {Type: llm.EventDone, FinishReason: "stop"}})
	r2 := baseReq("explain the bug")
	r2.Messages = append(r2.Messages, llm.Message{Role: llm.RoleAssistant, Content: "The bug is in foo.", Reasoning: "Let me think about the call graph carefully."},
		llm.Message{Role: llm.RoleUser, Content: "fix it"})
	pl2, _ := run(t, d, clk, r2, textReply("done"))
	// Checkpoint at the end of prompt 1 → reuse ≈ prompt-1 length.
	if pl2.CachedTokens < pl1.PromptTokens-2 || pl2.CachedTokens > pl1.PromptTokens+2 {
		t.Fatalf("expected reuse up to the previous prompt's checkpoint (%d), got %d", pl1.PromptTokens, pl2.CachedTokens)
	}
	// With rollback "none" the whole prompt is re-processed.
	d2, clk2 := testDevice(t, "llamacpp", true, func(s *runtime.Settings) { s.Slots = 1; s.SWARollback = runtime.RollbackNone })
	run(t, d2, clk2, baseReq("explain the bug"), []llm.Event{{Type: llm.EventReasoning, Text: "thinking"}, {Type: llm.EventText, Text: "x"}, {Type: llm.EventDone}})
	r3 := baseReq("explain the bug")
	r3.Messages = append(r3.Messages, llm.Message{Role: llm.RoleAssistant, Content: "x"}, llm.Message{Role: llm.RoleUser, Content: "go"})
	pl3, _ := run(t, d2, clk2, r3, textReply("ok"))
	if pl3.CachedTokens != 0 {
		t.Fatalf("rollback none: nothing reusable, got %d", pl3.CachedTokens)
	}
}

func TestSingleSlotEvictionByBackgroundRequest(t *testing.T) {
	d, clk := testDevice(t, "ollama", false, nil) // 1 slot
	main := baseReq("refactor the parser")
	run(t, d, clk, main, textReply("ok"))
	bg := &llm.Request{Messages: []llm.Message{{Role: llm.RoleSystem, Content: "Generate a short title for this conversation."}, {Role: llm.RoleUser, Content: "refactor the parser"}}}
	run(t, d, clk, bg, textReply("Parser refactor"))
	pl, _ := run(t, d, clk, main, textReply("ok"))
	if pl.CachedTokens > 20 || pl.Miss == nil || !strings.Contains(pl.Miss.Reason, "only cache slot") {
		t.Fatalf("background request should evict the main conversation from the only slot: cached=%d miss=%+v", pl.CachedTokens, pl.Miss)
	}
	// llama-server's 4 slots keep both.
	d4, clk4 := testDevice(t, "llamacpp", false, nil)
	run(t, d4, clk4, main, textReply("ok"))
	run(t, d4, clk4, bg, textReply("Parser refactor"))
	pl4, _ := run(t, d4, clk4, main, textReply("ok"))
	if pl4.CachedTokens < 1000 {
		t.Fatalf("multi-slot runtime should keep the main conversation cached, got %d", pl4.CachedTokens)
	}
}

func TestCloudLagAndPacing(t *testing.T) {
	d, _ := testDevice(t, "llamacpp", false, nil)
	p, _ := d.Prepare(baseReq("hi"))
	rel, _, _ := d.Acquire(context.Background())
	defer rel()
	pl := d.Plan(p)
	// Cloud delivers the second chunk 30 s after the request started — far
	// later than the device would.
	ch := make(chan Timed, 3)
	ch <- Timed{Ev: llm.Event{Type: llm.EventText, Text: "first part "}, At: pl.Start}
	ch <- Timed{Ev: llm.Event{Type: llm.EventText, Text: "second part"}, At: pl.Start.Add(30 * time.Second)}
	ch <- Timed{Ev: llm.Event{Type: llm.EventDone, FinishReason: "stop"}, At: pl.Start.Add(30 * time.Second)}
	close(ch)
	var emitted []string
	res := d.Pace(context.Background(), pl, p, ch, func(ev llm.Event) error { emitted = append(emitted, ev.Text); return nil })
	if strings.Join(emitted, "") != "first part second part" {
		t.Fatalf("emitted %q", strings.Join(emitted, ""))
	}
	if res.CloudLag < 10*time.Second {
		t.Fatalf("expected cloud lag to be recorded, got %v", res.CloudLag)
	}
	// Decode pacing: 10 ms/token at 1e9 bytes / 100e9 B/s plus KV.
	per := res.LastTokenAt.Sub(res.FirstTokenAt).Seconds()
	if per <= 0 {
		t.Fatal("tokens must be spread over time")
	}
}

func TestStopsWhenContextFull(t *testing.T) {
	req := baseReq("write a lot")
	d, clk := testDevice(t, "llamacpp", false, nil)
	p, _ := d.Prepare(req)
	p.Ctx = p.PromptTokens + 5
	rel, _, _ := d.Acquire(context.Background())
	defer rel()
	pl := d.Plan(p)
	res := d.Pace(context.Background(), pl, p, feed(clk.Now(), textReply(strings.Repeat("word ", 50))...), func(llm.Event) error { return nil })
	if !res.StoppedAtContext || res.Resp.FinishReason != llm.FinishLength || res.GeneratedPieces > 8 {
		t.Fatalf("expected stop at context with ~5 tokens, got stopped=%v finish=%s pieces=%d", res.StoppedAtContext, res.Resp.FinishReason, res.GeneratedPieces)
	}
}

func TestReadAheadUsesExactUsage(t *testing.T) {
	d, clk := testDevice(t, "llamacpp", false, nil)
	p, _ := d.Prepare(baseReq("hi"))
	rel, _, _ := d.Acquire(context.Background())
	defer rel()
	pl := d.Plan(p)
	est := pl.PromptTokens
	real := est * 2
	evs := []llm.Event{{Type: llm.EventText, Text: "one two three four"}, {Type: llm.EventDone, FinishReason: "stop",
		Usage: &llm.Usage{PromptTokens: real, CompletionTokens: 40, ReasoningTokens: 30}}}
	res := d.Pace(context.Background(), pl, p, feed(clk.Now(), evs...), func(llm.Event) error { return nil })
	if !pl.Exact || pl.PromptTokens != real {
		t.Fatalf("plan should be re-timed with the upstream's prompt count: exact=%v prompt=%d want %d", pl.Exact, pl.PromptTokens, real)
	}
	want := d.cfg.Timing.PrefillSec(0, real)
	if !approx(pl.PrefillDur.Seconds(), want, 0.01) {
		t.Fatalf("prefill %.3f want %.3f", pl.PrefillDur.Seconds(), want)
	}
	if pl.HiddenReasoningDur <= 0 {
		t.Fatal("30 hidden reasoning tokens should add a pause before the first token")
	}
	if res.FirstTokenAt.Before(pl.Start.Add(pl.LoadDur + pl.PrefillDur + pl.HiddenReasoningDur)) {
		t.Fatal("first token released before load + prefill + hidden reasoning")
	}
}

func TestBufferPreservesOrder(t *testing.T) {
	in := make(chan llm.Event)
	clk := newFakeClock()
	out := Buffer(context.Background(), in, clk)
	go func() {
		for i := 0; i < 100; i++ {
			in <- llm.Event{Type: llm.EventText, Text: string(rune('a' + i%26))}
		}
		close(in)
	}()
	n := 0
	for range out {
		n++
	}
	if n != 100 {
		t.Fatalf("got %d events", n)
	}
}
