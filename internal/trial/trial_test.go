package trial

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aeroknight786/trialm/internal/catalog"
	"github.com/aeroknight786/trialm/internal/llm"
	"github.com/aeroknight786/trialm/internal/openrouter"
	"github.com/aeroknight786/trialm/internal/predict"
	"github.com/aeroknight786/trialm/internal/sim"
	"github.com/aeroknight786/trialm/internal/upstream"
)

func fixture(t *testing.T) []openrouter.Endpoint {
	t.Helper()
	b, err := os.ReadFile("testdata/endpoints.json")
	if err != nil {
		t.Fatal(err)
	}
	var r struct {
		Data struct {
			Endpoints []openrouter.Endpoint `json:"endpoints"`
		} `json:"data"`
	}
	if err = json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	return r.Data.Endpoints
}
func TestNoLoggingProviderPinned(t *testing.T) {
	eps := fixture(t)
	for _, tc := range []struct {
		p    Policy
		want string
	}{{Policy{Tools: true, Context: 65536, Quant: "Q4_K_M"}, "private/fp8"}, {Policy{Tools: true, Context: 65536, Quant: "Q8_0"}, "zdr/bf16"}, {Policy{Tools: true, ZDR: true, Context: 65536, Quant: "Q4_K_M"}, "zdr/bf16"}, {Policy{Tools: true, Context: 65536, Provider: "logging/fp8"}, ""}, {Policy{Tools: true, Context: 65536, Provider: "unknown/fp8"}, ""}, {Policy{Tools: true, Context: 262144}, ""}, {Policy{Tools: true, Context: 65536, Provider: "private/fp8,zdr/bf16", ZDR: true}, "zdr/bf16"}} {
		e, err := Select(eps, tc.p)
		if tc.want == "" {
			if err == nil {
				t.Fatal("unsafe endpoint selected", e)
			}
			continue
		}
		if err != nil || e.Tag != tc.want {
			t.Fatal(tc, e, err)
		}
		policy := Provider(e, tc.p.ZDR)
		if policy["data_collection"] != "deny" || policy["allow_fallbacks"] != false || policy["require_parameters"] != true {
			t.Fatal(policy)
		}
	}
}
func TestEndpointHTTPShapeAndAttribution(t *testing.T) {
	b, err := os.ReadFile("testdata/endpoints.json")
	if err != nil {
		t.Fatal(err)
	}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/models/test/model/endpoints" || r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("X-Title") != "triaLM" {
			t.Error(r.URL, r.Header)
		}
		_, _ = w.Write(b)
	}))
	defer fake.Close()
	eps, err := (openrouter.Client{BaseURL: fake.URL + "/api/v1", Key: "test-key"}).Endpoints(context.Background(), "test/model")
	if err != nil || len(eps) != 7 {
		t.Fatal(eps, err)
	}
}
func TestBudgetUsageAndConcurrency(t *testing.T) {
	var calls atomic.Int32
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["max_tokens"].(float64) > 2 {
			t.Error("affordable output not clamped", body["max_tokens"])
		}
		p := body["provider"].(map[string]any)
		if p["data_collection"] != "deny" || p["allow_fallbacks"] != false {
			t.Error(p)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":1}}\n\ndata: [DONE]\n\n")
	}))
	defer fake.Close()
	e := fixture(t)[2]
	client := &upstream.OpenAICompat{BaseURL: fake.URL, Model: "test", Extra: map[string]any{"provider": Provider(e, false)}}
	b := NewBudget(client, Rates{Completion: 1}, 2)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			events, err := b.Stream(context.Background(), &llm.Request{MaxTokens: 500})
			if err != nil {
				if err.Error() != "triaLM trial budget reached ($2.00): trialm try --budget 5 to continue" {
					t.Error(err)
				}
				return
			}
			for range events {
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 2 || b.Spent() != 2 {
		t.Fatal("budget overspent", calls.Load(), b.Spent())
	}
}
func TestBudgetCachedUsageAndFailClosed(t *testing.T) {
	c := &scriptClient{events: []llm.Event{{Type: llm.EventDone, Usage: &llm.Usage{PromptTokens: 100, CompletionTokens: 20, CachedPromptTokens: 80}}}}
	b := NewBudget(c, Rates{Prompt: .001, Completion: .002, CacheRead: .0001, Request: .01}, 2)
	events, err := b.Stream(context.Background(), &llm.Request{})
	if err != nil {
		t.Fatal(err)
	}
	for range events {
	}
	if math.Abs(b.Spent()-.078) > 1e-9 {
		t.Fatal(b.Spent())
	}
	unknown := NewBudget(&scriptClient{events: []llm.Event{{Type: llm.EventDone}}}, Rates{}, 2)
	events, err = unknown.Stream(context.Background(), &llm.Request{})
	if err != nil {
		t.Fatal(err)
	}
	for range events {
	}
	if _, err = unknown.Stream(context.Background(), &llm.Request{}); err == nil {
		t.Fatal("missing usage did not stop admission")
	}
	for _, price := range []string{"", "-1", "NaN", "+Inf", "bad"} {
		if _, err := Prices(openrouter.Price{Prompt: price, Completion: "0"}); err == nil {
			t.Fatal(price)
		}
	}
}

type scriptClient struct{ events []llm.Event }

func (c *scriptClient) Describe() string { return "script" }
func (c *scriptClient) Stream(ctx context.Context, r *llm.Request) (<-chan llm.Event, error) {
	out := make(chan llm.Event, len(c.events))
	for _, e := range c.events {
		out <- e
	}
	close(out)
	return out, nil
}
func TestFidelityPacingAndSummary(t *testing.T) {
	m := catalog.Model{}
	q := catalog.Quant{Name: "MXFP4"}
	e := openrouter.Endpoint{Quantization: "fp4"}
	if !strings.HasPrefix(Fidelity(m, q, e), "exact") {
		t.Fatal(Fidelity(m, q, e))
	}
	q.Name = "IQ3_XXS"
	e.Quantization = "bf16"
	if !strings.Contains(Fidelity(m, q, e), "upper bound") {
		t.Fatal(Fidelity(m, q, e))
	}
	e.Quantization = ""
	if !strings.HasPrefix(Fidelity(m, q, e), "precision undisclosed") {
		t.Fatal(Fidelity(m, q, e))
	}
	m.Generated = true
	m.TrialStandIn = "base"
	if !strings.Contains(Fidelity(m, q, e), "stand-in") {
		t.Fatal(Fidelity(m, q, e))
	}
	timing := &predict.Timing{DecodeMult: 1, PrefillMult: 1}
	slow := Pessimistic(timing, 60, 70)
	if slow.DecodeMult != .4 || math.Abs(slow.PrefillMult-.3) > 1e-9 || timing.DecodeMult != 1 {
		t.Fatal(slow, timing)
	}
	at := time.Now()
	r := Summarize([]sim.Record{{At: at, TotalSec: 10, CloudSec: 2, LoadSec: 4, PrefillSec: 2, Cold: true, PromptTokens: 100, OutputTokens: 10}, {At: at.Add(13 * time.Second), TotalSec: 6, CloudSec: 6, CloudLagSec: 2, PrefillSec: 1, CachedTokens: 90, PromptTokens: 110, OutputTokens: 10}}, .21, "≈ sharper")
	if r.Requests != 2 || r.LocalSeconds != 14 || r.CloudSeconds != 8 || r.CloudBound != 1 || r.FirstTurn != 10 || r.TypicalTurn != 6 || r.SlowestTurn != 10 || r.BetweenTurnsSeconds != 3 || r.ContextPeak != 120 {
		t.Fatal(r)
	}
	text := r.Plain(60)
	for _, word := range []string{"cloud-bound", "reading", "writing", "first turn", "typical", "slowest", "spend", "≈ sharper"} {
		if !strings.Contains(text, word) {
			t.Fatal(text)
		}
	}
	if strings.ContainsRune(text, '\x1b') {
		t.Fatal(text)
	}
}
func TestBudgetCancellation(t *testing.T) {
	b := NewBudget(&scriptClient{}, Rates{}, 2)
	b.gate <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := b.Stream(ctx, &llm.Request{})
	if err != context.Canceled {
		t.Fatal(err)
	}
}

func TestPublicEndpointPrivacyJoin(t *testing.T) {
	endpoints, err := os.ReadFile("testdata/endpoints.json")
	if err != nil {
		t.Fatal(err)
	}
	zdr, err := os.ReadFile("testdata/zdr.json")
	if err != nil {
		t.Fatal(err)
	}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models/test/model/endpoints":
			_, _ = w.Write(endpoints)
		case "/endpoints/zdr":
			_, _ = w.Write(zdr)
		default:
			t.Error(r.URL)
			w.WriteHeader(404)
		}
	}))
	defer fake.Close()
	eps, err := (openrouter.Client{BaseURL: fake.URL}).PrivateEndpoints(context.Background(), "test/model", true)
	if err != nil {
		t.Fatal(err)
	}
	if !eps[1].ZDR || eps[1].DataCollection != "deny" {
		t.Fatal("published ZDR endpoint was not joined", eps[1])
	}
	if eps[0].DataPolicy.DataCollection != "allow" || eps[0].DataCollection != "" {
		t.Fatal("logging policy overwritten", eps[0])
	}
	if eps[2].ZDR {
		t.Fatal("safety retention promoted to ZDR", eps[2])
	}
	if eps[4].ZDR {
		t.Fatal("wrong model joined", eps[4])
	}
	e, err := Select(eps, Policy{Tools: true, Context: 65536, ZDR: true, Provider: "logging/fp8"})
	if err == nil {
		t.Fatal("logging endpoint pinned", e)
	}
	e, err = Select(eps, Policy{Tools: true, Context: 65536, ZDR: true, Provider: "unknown/fp8"})
	if err != nil || e.Tag != "unknown/fp8" {
		t.Fatal(e, err)
	}
}
