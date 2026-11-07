package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aeroknight786/trialm/internal/api"
	"github.com/aeroknight786/trialm/internal/catalog"
	runtime "github.com/aeroknight786/trialm/internal/engine"
	"github.com/aeroknight786/trialm/internal/sim"
	"github.com/aeroknight786/trialm/internal/tok"
	"github.com/aeroknight786/trialm/internal/upstream"
)

func TestBridgeLimitsAndSessionWarnings(t *testing.T) {
	fu := &fakeUpstream{}
	up := httptest.NewServer(fu)
	defer up.Close()
	var log bytes.Buffer
	s := New(Config{Bridge: true, Upstream: &upstream.OpenAICompat{BaseURL: up.URL + "/v1"}, Model: api.ModelInfo{ContextLength: 4096}, Log: &log})
	body := `{"model":"m","messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"hello"},{"role":"system","content":"reminder"}],"options":{"num_ctx":8192,"num_predict":64000}}`
	for range 2 {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest("POST", "http://localhost/api/chat", strings.NewReader(body)))
		if rec.Code != 200 {
			t.Fatal(rec.Body.String())
		}
	}
	fu.mu.Lock()
	defer fu.mu.Unlock()
	if len(fu.bodies) != 2 {
		t.Fatal(fu.bodies)
	}
	for _, b := range fu.bodies {
		if cap := int(b["max_tokens"].(float64)); cap >= 4096 || cap < 256 {
			t.Fatalf("max_tokens %d", cap)
		}
	}
	if strings.Count(log.String(), "options.num_ctx=") != 1 || strings.Count(log.String(), "mid-conversation system message merged") != 1 {
		t.Fatal(log.String())
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("HEAD", "http://localhost/api/hello", nil))
	if rec.Code != 200 || rec.Body.Len() != 0 {
		t.Fatalf("hello: %d %s", rec.Code, rec.Body.String())
	}
}

func TestBridgeClampFloor(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["max_tokens"] != 256.0 {
			t.Errorf("max_tokens %v", body["max_tokens"])
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer up.Close()
	s := New(Config{Bridge: true, Upstream: &upstream.OpenAICompat{BaseURL: up.URL + "/v1"}, Model: api.ModelInfo{ContextLength: 10}})
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("POST", "http://localhost/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"hello"}],"max_tokens":64000}`)))
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
}

func TestOllamaBridgeUsesNativeContext(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprint(explicit), func(t *testing.T) {
			var log bytes.Buffer
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/chat" {
					t.Errorf("path %s", r.URL.Path)
				}
				var b map[string]any
				json.NewDecoder(r.Body).Decode(&b)
				opts := b["options"].(map[string]any)
				if opts["num_ctx"] != 8192.0 || opts["num_predict"].(float64) <= 4096 {
					t.Errorf("options %v", opts)
				}
				fmt.Fprintln(w, `{"message":{"content":"ok"},"done":true,"prompt_eval_count":5,"eval_count":1}`)
			}))
			defer up.Close()
			context := 8192
			options := `"num_predict":64000`
			if explicit {
				context = 4096
				options += `,"num_ctx":8192`
			}
			s := New(Config{Bridge: true, RuntimeName: "Ollama", Upstream: &upstream.OpenAICompat{BaseURL: up.URL + "/v1"}, Model: api.ModelInfo{ContextLength: context}, Log: &log})
			for range 2 {
				rec := httptest.NewRecorder()
				s.ServeHTTP(rec, httptest.NewRequest("POST", "http://localhost/api/chat", strings.NewReader(`{"messages":[{"role":"user","content":"hi"}],"options":{`+options+`}}`)))
				if rec.Code != 200 || strings.Contains(log.String(), "ignored") {
					t.Fatalf("%d %s log %s", rec.Code, rec.Body.String(), log.String())
				}
			}
			wantWarnings := 0
			if explicit {
				wantWarnings = 1
			}
			if strings.Count(log.String(), "differs from the configured context") != wantWarnings {
				t.Fatal(log.String())
			}
		})
	}
}

func TestTrialRequestWarnings(t *testing.T) {
	p, _ := runtime.Get("llamacpp")
	m, _ := catalog.Lookup("qwen3-coder:30b")
	settings := p.Resolve(runtime.ResolveInput{GPUMemGiB: 24, NativeCtx: m.NativeCtx, CtxOverride: 65536})
	timing := fastTiming(m)
	timing.LoadColdS, timing.LoadWarmS = 0, 0
	d := sim.NewDevice(sim.Config{RuntimeID: p.ID, Settings: settings, Timing: timing, Model: m, Render: tok.RenderOptions{}})
	var log bytes.Buffer
	up := httptest.NewServer(&fakeUpstream{})
	defer up.Close()
	s := New(Config{Device: d, Upstream: &upstream.OpenAICompat{BaseURL: up.URL + "/v1"}, Log: &log})
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("POST", "http://localhost/v1/messages", strings.NewReader(`{"messages":[{"role":"user","content":[{"type":"image"},{"type":"text","text":"hi"}]}]}`)))
	if rec.Code != 200 || !strings.Contains(log.String(), "#1 anthropic  ! dropped image") || !strings.Contains(log.String(), "! upstream prompt_tokens=") {
		t.Fatalf("%d %s log %s", rec.Code, rec.Body.String(), log.String())
	}
}
