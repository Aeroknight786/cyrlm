package sim

import (
	"context"
	"sync"
	"time"

	"github.com/aeroknight786/cyrlm/internal/llm"
	"github.com/aeroknight786/cyrlm/internal/runtime"
	"github.com/aeroknight786/cyrlm/internal/tok"
)

// Timed is an upstream event with its arrival time.
type Timed struct {
	Ev llm.Event
	At time.Time
}

// Buffer drains in without backpressure (the cloud usually finishes long
// before the simulated device would) and stamps arrival times.
func Buffer(ctx context.Context, in <-chan llm.Event, clock Clock) <-chan Timed {
	out := make(chan Timed)
	go func() {
		defer close(out)
		var (
			mu     sync.Mutex
			queue  []Timed
			closed bool
			signal = make(chan struct{}, 1)
		)
		go func() {
			for ev := range in {
				mu.Lock()
				queue = append(queue, Timed{Ev: ev, At: clock.Now()})
				mu.Unlock()
				select {
				case signal <- struct{}{}:
				default:
				}
			}
			mu.Lock()
			closed = true
			mu.Unlock()
			select {
			case signal <- struct{}{}:
			default:
			}
		}()
		for {
			mu.Lock()
			if len(queue) > 0 {
				t := queue[0]
				queue = queue[1:]
				mu.Unlock()
				select {
				case out <- t:
				case <-ctx.Done():
					return
				}
				continue
			}
			done := closed
			mu.Unlock()
			if done {
				return
			}
			select {
			case <-signal:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

// PaceResult is the outcome of pacing one response.
type PaceResult struct {
	Resp            llm.Response
	GeneratedPieces int
	FirstTokenAt    time.Time
	LastTokenAt     time.Time
	// CloudLag is how much later than the simulated schedule the cloud
	// delivered tokens (the simulation cannot be faster than the cloud).
	CloudLag time.Duration
	// UpstreamDoneAt is when the cloud finished.
	UpstreamDoneAt   time.Time
	StoppedAtContext bool
	Cancelled        bool
	Err              error
}

// Emitter receives paced events (text/reasoning/tool start/tool args).
type Emitter func(ev llm.Event) error

// Pace releases upstream events at the simulated local schedule: the first
// token follows load + prefill, then each estimated token takes the decode
// time at the current depth. Events arriving later than scheduled are
// released on arrival and counted as cloud lag.
func (d *Device) Pace(ctx context.Context, pl *Plan, p *Prepared, events <-chan Timed, emit Emitter) PaceResult {
	clock := d.cfg.Clock
	t := d.cfg.Timing
	ratio := d.OutputRatio()
	res := PaceResult{}
	events, ratio = d.readAhead(ctx, pl, events, ratio)
	next := pl.FirstTokenAt
	promptReal := pl.PromptTokens
	ctxLimit := p.Ctx
	stopAtFull := d.cfg.Settings.OnContextFull != runtime.FullShift

	release := func(due time.Time, at time.Time) (time.Time, error) {
		if at.After(due) {
			res.CloudLag += at.Sub(due)
			due = at
		}
		if err := clock.SleepUntil(ctx, due); err != nil {
			return due, err
		}
		return due, nil
	}
	finish := func() PaceResult {
		if res.LastTokenAt.IsZero() {
			res.LastTokenAt = next
		}
		return res
	}

	for {
		var te Timed
		var ok bool
		select {
		case te, ok = <-events:
		case <-ctx.Done():
			res.Cancelled = true
			return finish()
		}
		if !ok {
			// Upstream ended without a Done event.
			if res.Resp.FinishReason == "" {
				res.Resp.FinishReason = llm.FinishStop
			}
			return finish()
		}
		ev := te.Ev
		switch ev.Type {
		case llm.EventText, llm.EventReasoning, llm.EventToolCallArgs:
			for _, piece := range tok.Pieces(ev.Text) {
				depth := promptReal + int(float64(res.GeneratedPieces)*ratio)
				if depth >= ctxLimit && stopAtFull {
					res.StoppedAtContext = true
					res.Resp.FinishReason = llm.FinishLength
					return finish()
				}
				if depth >= ctxLimit {
					depth = ctxLimit - 1
				}
				due := next.Add(secs(t.DecodeSec(depth) * ratio))
				due, err := release(due, te.At)
				if err != nil {
					res.Cancelled = true
					return finish()
				}
				out := ev
				out.Text = piece
				if err := emit(out); err != nil {
					res.Cancelled = true
					return finish()
				}
				res.Resp.Apply(out)
				if res.FirstTokenAt.IsZero() {
					res.FirstTokenAt = due
				}
				res.LastTokenAt = due
				next = due
				res.GeneratedPieces++
			}
		case llm.EventToolCallStart:
			due, err := release(next, te.At)
			if err != nil {
				res.Cancelled = true
				return finish()
			}
			next = due
			if err := emit(ev); err != nil {
				res.Cancelled = true
				return finish()
			}
			res.Resp.Apply(ev)
		case llm.EventDone:
			res.UpstreamDoneAt = te.At
			if te.At.After(next) && res.GeneratedPieces > 0 {
				res.CloudLag += te.At.Sub(next)
			}
			res.Resp.Apply(ev)
			if res.Resp.FinishReason == "" {
				res.Resp.FinishReason = llm.FinishStop
			}
			return finish()
		case llm.EventError:
			res.Err = ev.Err
			res.UpstreamDoneAt = te.At
			return finish()
		}
	}
}

// readAhead collects upstream events until the cloud finishes or the first
// simulated token is due. When the cloud finished first (the common case:
// local prefill is slower than a datacenter), its usage gives exact prompt
// and completion token counts, and the plan is re-timed with them. Returns
// a channel replaying the collected events followed by the rest, and the
// output ratio (real tokens per estimated piece) to pace with.
func (d *Device) readAhead(ctx context.Context, pl *Plan, events <-chan Timed, ratio float64) (<-chan Timed, float64) {
	var got []Timed
	timer := d.cfg.Clock.Timer(pl.FirstTokenAt)
	closed := false
	var done *llm.Event
loop:
	for {
		select {
		case te, ok := <-events:
			if !ok {
				closed = true
				break loop
			}
			got = append(got, te)
			if te.Ev.Type == llm.EventDone || te.Ev.Type == llm.EventError {
				if te.Ev.Type == llm.EventDone {
					done = &got[len(got)-1].Ev
				}
				break loop
			}
		case <-timer:
			break loop
		case <-ctx.Done():
			break loop
		}
	}
	if done != nil && done.Usage != nil {
		u := done.Usage
		t := d.cfg.Timing
		if u.PromptTokens > 0 && pl.PromptTokens > 0 {
			scale := float64(u.PromptTokens) / float64(pl.PromptTokens)
			pl.CachedTokens = int(float64(pl.CachedTokens)*scale + 0.5)
			if pl.CachedTokens >= u.PromptTokens {
				pl.CachedTokens = u.PromptTokens - 1
			}
			pl.PromptTokens = u.PromptTokens
			pl.PrefillTokens = pl.PromptTokens - pl.CachedTokens
			pl.PrefillDur = secs(t.PrefillSec(pl.CachedTokens, pl.PromptTokens))
			pl.Exact = true
		}
		pieces, sawReasoning := 0, false
		for _, te := range got {
			switch te.Ev.Type {
			case llm.EventText, llm.EventReasoning, llm.EventToolCallArgs:
				pieces += len(tok.Pieces(te.Ev.Text))
				if te.Ev.Type == llm.EventReasoning {
					sawReasoning = true
				}
			}
		}
		if u.CompletionTokens > 0 && pieces > 0 {
			visible := u.CompletionTokens
			if !sawReasoning && u.ReasoningTokens > 0 {
				// Reasoning the provider hid still costs local decode time.
				visible -= u.ReasoningTokens
				hidden := 0.0
				for i := 0; i < u.ReasoningTokens; i++ {
					hidden += t.DecodeSec(pl.PromptTokens + i)
				}
				pl.HiddenReasoningDur = secs(hidden)
			}
			if visible > 0 {
				ratio = float64(visible) / float64(pieces)
			}
		}
		pl.FirstTokenAt = pl.Start.Add(pl.LoadDur + pl.RestoreDur + pl.PrefillDur + pl.HiddenReasoningDur)
	}
	out := make(chan Timed)
	go func() {
		defer close(out)
		for _, te := range got {
			select {
			case out <- te:
			case <-ctx.Done():
				return
			}
		}
		if closed {
			return
		}
		for te := range events {
			select {
			case out <- te:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, ratio
}

// Timings converts a plan and pace result into simulated runtime timings.
func Timings(pl *Plan, r PaceResult) llm.Timings {
	tm := llm.Timings{
		Load:             pl.LoadDur,
		PromptEval:       pl.PrefillDur + pl.RestoreDur + pl.HiddenReasoningDur,
		PromptEvalTokens: pl.PrefillTokens,
		EvalTokens:       r.Resp.Usage.CompletionTokens,
	}
	if tm.EvalTokens == 0 {
		tm.EvalTokens = r.GeneratedPieces
	}
	if !r.LastTokenAt.IsZero() {
		tm.Eval = r.LastTokenAt.Sub(pl.FirstTokenAt)
		tm.Total = r.LastTokenAt.Sub(pl.Start)
	}
	return tm
}
