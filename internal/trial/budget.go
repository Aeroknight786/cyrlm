package trial

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sync"

	"github.com/aeroknight786/trialm/internal/llm"
	"github.com/aeroknight786/trialm/internal/tok"
	"github.com/aeroknight786/trialm/internal/upstream"
)

// Budget serializes cloud admissions so simultaneous harness side requests
// cannot all spend the same remaining balance. Pacing can continue in parallel.
type Budget struct {
	Client  upstream.Client
	Rates   Rates
	Limit   float64
	gate    chan struct{}
	mu      sync.Mutex
	spent   float64
	unknown bool
}

func NewBudget(c upstream.Client, r Rates, limit float64) *Budget {
	return &Budget{Client: c, Rates: r, Limit: limit, gate: make(chan struct{}, 1)}
}
func (b *Budget) Describe() string { return b.Client.Describe() }
func (b *Budget) Spent() float64   { b.mu.Lock(); defer b.mu.Unlock(); return b.spent }
func (b *Budget) Error() error {
	return fmt.Errorf("triaLM trial budget reached ($%.2f): trialm try --budget 5 to continue", b.Limit)
}
func (b *Budget) Stream(ctx context.Context, req *llm.Request) (<-chan llm.Event, error) {
	select {
	case b.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	release := func() { <-b.gate }
	b.mu.Lock()
	remaining, unknown := b.Limit-b.spent, b.unknown
	b.mu.Unlock()
	if remaining <= 0 || unknown {
		release()
		return nil, b.Error()
	}
	clone := *req
	// Reserve prompt and request charges and clamp the generation to the
	// affordable output. Usage remains the authority after every response.
	prompt := tok.Render(req, tok.RenderOptions{}).Len()
	// A token estimate can undercount the cloud tokenizer. Serialized request
	// bytes give a conservative byte-token bound, including tool schemas and
	// message framing. Do not spend the same room on prompt and completion.
	wire, err := json.Marshal(req)
	if err != nil {
		release()
		return nil, err
	}
	prompt = max(prompt, len(wire))
	room := remaining - float64(prompt)*b.Rates.Prompt - b.Rates.Request
	if room < 0 {
		release()
		return nil, b.Error()
	}
	if b.Rates.Completion > 0 {
		affordable := int(math.Min(float64(math.MaxInt32), math.Floor(room/b.Rates.Completion)))
		if affordable < 1 {
			release()
			return nil, b.Error()
		}
		if clone.MaxTokens <= 0 || clone.MaxTokens > affordable {
			clone.MaxTokens = affordable
		}
	}
	in, err := b.Client.Stream(ctx, &clone)
	if err != nil {
		release()
		return nil, err
	}
	out := make(chan llm.Event, 64)
	go func() {
		defer close(out)
		defer release()
		accounted := false
		defer func() {
			if !accounted {
				b.mu.Lock()
				b.unknown = true
				b.mu.Unlock()
			}
		}()
		for ev := range in {
			if ev.Type == llm.EventDone && ev.Usage == nil {
				ev = llm.Event{Type: llm.EventError, Err: fmt.Errorf("OpenRouter omitted usage; trial stopped because spend cannot be checked")}
			}
			if ev.Type == llm.EventDone && ev.Usage != nil {
				u := ev.Usage
				cached := max(0, min(u.CachedPromptTokens, u.PromptTokens))
				cost := float64(max(0, u.PromptTokens-cached))*b.Rates.Prompt + float64(cached)*b.Rates.CacheRead + float64(max(0, u.CompletionTokens))*b.Rates.Completion + b.Rates.Request
				b.mu.Lock()
				b.spent += cost
				over := b.spent >= b.Limit
				b.mu.Unlock()
				accounted = true
				if over {
					ev = llm.Event{Type: llm.EventError, Err: b.Error()}
				}
			}
			select {
			case out <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}
