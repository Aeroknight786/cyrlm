package server

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aeroknight786/trialm/internal/api"
	"github.com/aeroknight786/trialm/internal/harness"
	"github.com/aeroknight786/trialm/internal/llm"
	"github.com/aeroknight786/trialm/internal/store"
	"github.com/aeroknight786/trialm/internal/tok"
	"github.com/aeroknight786/trialm/internal/upstream"
)

func TestBridgeOpeningCounts(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Mac opening evidence only")
	}
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			t.Setenv("TRIALM_HOME", t.TempDir())
			var reported atomic.Int64
			var failed atomic.Bool
			reported.Store(20200)
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if failed.Load() {
					http.Error(w, "bad prompt", 400)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":%d,\"completion_tokens\":1}}\n\ndata: [DONE]\n\n", reported.Load())
			}))
			defer up.Close()
			s := New(Config{Bridge: true, Upstream: &upstream.OpenAICompat{BaseURL: up.URL + "/v1"}, Model: api.ModelInfo{Name: "local"}})
			ts := httptest.NewServer(s)
			defer ts.Close()
			post := func(messages string) {
				t.Helper()
				resp, e := http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(fmt.Sprintf(`{"model":"local","stream":%t,"messages":%s}`, stream, messages)))
				if e != nil {
					t.Fatal(e)
				}
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
			first := `[{"role":"system","content":"private tool definitions"},{"role":"user","content":"hello"}]`
			post(first) // verification has no harness label
			s.ObserveHarness("claude-code")
			post(first)
			post(`[{"role":"user","content":"hi"},{"role":"assistant","content":"hello"},{"role":"user","content":"again"}]`)
			failed.Store(true)
			post(first)
			failed.Store(false)
			reported.Store(0)
			post(`[{"role":"user","content":"hello"}]`)
			fallback := tok.Render(&llm.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: "hello"}}}, tok.RenderOptions{}).Len()
			var counts map[string][]int
			if e := store.Default().Read("opening-prompts.json", &counts); e != nil {
				t.Fatal(e)
			}
			if len(counts["claude-code"]) != 2 || counts["claude-code"][0] != 20200 || counts["claude-code"][1] != fallback {
				t.Fatal(counts)
			}
			cc, _ := harness.Get("claude-code")
			if cc.Scenario.BasePromptTokens != (20200+fallback)/2 {
				t.Fatal(cc.Scenario)
			}
		})
	}
}
