package sim

import (
	"context"
	"strings"
	"testing"

	"github.com/aeroknight786/trialm/internal/llm"
)

func TestPromptUsageSanity(t *testing.T) {
	for _, tc := range []struct {
		tokens int
		accept bool
	}{{-1, false}, {29, false}, {30, true}, {100, true}, {300, true}, {301, false}} {
		pl := &Plan{DepthPieces: 100, PromptTokens: 120}
		original := &llm.Usage{PromptTokens: tc.tokens, CompletionTokens: 42}
		got := sanePromptUsage(pl, original)
		if (got.PromptTokens > 0) != tc.accept || got.CompletionTokens != 42 || original.PromptTokens != tc.tokens {
			t.Fatalf("%d: %+v", tc.tokens, got)
		}
		if !tc.accept && (len(pl.Notes) != 1 || !strings.Contains(pl.Notes[0], "keeping the estimate")) {
			t.Fatalf("notes %v", pl.Notes)
		}
	}
}

func TestBadPromptUsageDoesNotRetimeOrCalibrate(t *testing.T) {
	d, clk := testDevice(t, "llamacpp", false, nil)
	before := d.PromptRatio()
	p, err := d.Prepare(baseReq("hello"))
	if err != nil {
		t.Fatal(err)
	}
	pl := d.Plan(p)
	estimate, prefill := pl.PromptTokens, pl.PrefillDur
	res := d.Pace(context.Background(), pl, p, feed(clk.Now(), llm.Event{Type: llm.EventText, Text: "ok"}, llm.Event{Type: llm.EventDone, FinishReason: llm.FinishStop, Usage: &llm.Usage{PromptTokens: 1, CompletionTokens: 1}}), func(llm.Event) error { return nil })
	d.Commit(pl, &res.Resp, res.GeneratedPieces, clk.Now())
	if pl.Exact || pl.PromptTokens != estimate || pl.PrefillDur != prefill || d.PromptRatio() != before || len(pl.Notes) != 1 {
		t.Fatalf("plan %+v ratio %v", pl, d.PromptRatio())
	}
}

func TestLatePromptUsageIsAlsoRejected(t *testing.T) {
	pl := &Plan{DepthPieces: 100, PromptTokens: 100}
	d, clk := testDevice(t, "llamacpp", false, nil)
	// A timer that fires immediately ends read-ahead before Done arrives.
	d.cfg.Clock = RealClock{}
	p, _ := d.Prepare(baseReq("hello"))
	pl.Start, pl.FirstTokenAt = clk.Now(), clk.Now()
	events := make(chan Timed)
	go func() {
		events <- Timed{Ev: llm.Event{Type: llm.EventDone, Usage: &llm.Usage{PromptTokens: 1}}, At: clk.Now()}
		close(events)
	}()
	res := d.Pace(context.Background(), pl, p, events, func(llm.Event) error { return nil })
	if res.Resp.Usage.PromptTokens != 0 || len(pl.Notes) != 1 {
		t.Fatalf("result %+v notes %v", res, pl.Notes)
	}
}
