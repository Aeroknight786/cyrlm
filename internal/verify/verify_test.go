package verify

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aeroknight786/trialm/internal/api"
	"github.com/aeroknight786/trialm/internal/catalog"
	"github.com/aeroknight786/trialm/internal/predict"
	"github.com/aeroknight786/trialm/internal/runtime"
	"github.com/aeroknight786/trialm/internal/server"
	"github.com/aeroknight786/trialm/internal/sim"
	"github.com/aeroknight786/trialm/internal/tok"
	"github.com/aeroknight786/trialm/internal/upstream"
)

// fakeCloud answers with ~120 words and reports prompt tokens ≈ chars/4.
func fakeCloud() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Content any `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		chars := 0
		for _, m := range body.Messages {
			if s, ok := m.Content.(string); ok {
				chars += len(s)
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		for i := 0; i < 120; i++ {
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"word%d \"}}]}\n\n", i)
		}
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprintf(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":%d,\"completion_tokens\":240}}\n\ndata: [DONE]\n\n", chars/4)
		fl.Flush()
	}))
}

func TestVerifyAgainstSimulatedRuntime(t *testing.T) {
	cloud := fakeCloud()
	defer cloud.Close()
	m, _ := catalog.Lookup("llama3.1:8b")
	prof, _ := runtime.Get("llamacpp")
	timing := &predict.Timing{Model: m, WeightsBytesPerTok: 5e8, KVElem: 2, EffBandwidth: 1e11,
		MatmulSecPerTok: 0.0002, AttnFLOPSEff: 1e18, DecodeMult: 1, PrefillMult: 1, LoadColdS: 0.4, LoadWarmS: 0.1}
	s := prof.Resolve(runtime.ResolveInput{GPUMemGiB: 24, NativeCtx: m.NativeCtx, CtxOverride: 32768})
	dev := sim.NewDevice(sim.Config{RuntimeName: prof.Name, RuntimeID: prof.ID, Settings: s, Timing: timing, Model: m,
		Render: tok.RenderOptions{StripPastReasoning: true}})
	srv := server.New(server.Config{Device: dev, Upstream: &upstream.OpenAICompat{BaseURL: cloud.URL, Model: "x"},
		Model: api.ModelInfo{Name: "llama3.1:8b", ContextLength: 32768}})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	res, err := Run(context.Background(), Options{Endpoint: ts.URL, Model: "llama3.1:8b", PromptTokens: 2000, FollowUpTokens: 300, OutputTokens: 64})
	if err != nil {
		t.Fatal(err)
	}
	// Probe pays the cold load.
	if res.Probe.TTFTSec < 0.4 {
		t.Errorf("probe TTFT %.2f s should include the 0.4 s simulated load", res.Probe.TTFTSec)
	}
	// Cold prompt: TTFT ≈ prefill of all prompt tokens.
	want := timing.PrefillSec(0, res.Cold.PromptTokens)
	if math.Abs(res.Cold.TTFTSec-want) > 0.25*want+0.05 {
		t.Errorf("cold TTFT %.3f s, simulated prefill %.3f s", res.Cold.TTFTSec, want)
	}
	// Decode ≈ 1/(5 ms + KV) ≈ 190 tok/s.
	wantTPS := timing.DecodeTPS(res.Cold.PromptTokens)
	if math.Abs(res.Cold.DecodeTPS-wantTPS) > 0.3*wantTPS {
		t.Errorf("decode %.0f tok/s, simulated %.0f", res.Cold.DecodeTPS, wantTPS)
	}
	if !res.CacheReused || res.FollowUp.TTFTSec > 0.5*res.Cold.TTFTSec {
		t.Errorf("follow-up should reuse the cache: cold %.3f s, follow-up %.3f s, reused=%v", res.Cold.TTFTSec, res.FollowUp.TTFTSec, res.CacheReused)
	}
	if res.FollowUpPrefillTPS <= 0 || res.PrefillTPS <= 0 {
		t.Errorf("rates missing: %+v", res)
	}
}

func TestSyntheticSizes(t *testing.T) {
	for _, n := range []int{500, 4000} {
		got := tok.Count(syntheticContext(n))
		if got < n || got > n+200 {
			t.Errorf("syntheticContext(%d) = %d tokens", n, got)
		}
	}
	if !strings.Contains(syntheticLog(300), "FAIL") {
		t.Error("log should contain a failing test")
	}
}
