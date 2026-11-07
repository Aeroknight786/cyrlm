package verify

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/aeroknight786/trialm/internal/calib"
)

func TestReadyFake(t *testing.T) {
	for _, bad := range []bool{false, true} {
		t.Run(fmt.Sprint(bad), func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/health" {
					fmt.Fprint(w, `{"status":"ok"}`)
					return
				}
				var b map[string]any
				_ = json.NewDecoder(r.Body).Decode(&b)
				if b["tools"] != nil {
					args := `{\"value\":\"ok\"}`
					if bad {
						args = "bad"
					}
					fmt.Fprintf(w, `{"choices":[{"message":{"tool_calls":[{"id":"c","function":{"name":"record_result","arguments":"%s"}}]}}]}`, args)
					return
				}
				if b["stream"] == true {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n")
					if f, ok := w.(http.Flusher); ok {
						f.Flush()
					}
					time.Sleep(time.Millisecond)
					fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\" world\"}}],\"usage\":{\"prompt_tokens\":128,\"completion_tokens\":5}}\n\ndata: [DONE]\n\n")
					return
				}
				fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}],"timings":{"prompt_ms":2}}`)
			}))
			defer s.Close()
			r, err := Ready(context.Background(), ReadyOptions{Endpoint: s.URL, Model: "m", BitsPerWeight: 2, Predicted: Predicted{DecodeTPS0: 100, PrefillTPS: 100}})
			if err != nil {
				t.Fatal(err)
			}
			if r.Ready == bad {
				t.Fatalf("%+v", r)
			}
			if bad && r.Failing != "tool_calls" {
				t.Fatal(r.Failing)
			}
			if len(r.Misses) != 2 || !strings.Contains(r.Plain(), "spec_verify") {
				t.Fatal(r)
			}
			if len(r.SpecVerifySecs) != 0 || !strings.Contains(r.Plain(), "inconclusive") {
				t.Fatal(r.SpecVerifySecs)
			}
		})
	}
}

func TestRealEngine(t *testing.T) {
	if os.Getenv("TRIALM_REAL_ENGINE") != "1" {
		t.Skip("set TRIALM_REAL_ENGINE=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	dir := t.TempDir()
	path, err := calib.Generate(ctx, dir, calib.Probe{Shape: calib.Shapes[0], Quant: "Q4_K_M", Layers: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	cmd := exec.CommandContext(ctx, "/opt/homebrew/bin/llama-server", "-m", path, "-ngl", "0", "-dev", "none", "--no-op-offload", "--port", fmt.Sprint(port), "-c", "2048")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	endpoint := fmt.Sprintf("http://127.0.0.1:%d", port)
	for {
		resp, e := http.Get(endpoint + "/health")
		if e == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		time.Sleep(200 * time.Millisecond)
	}
	r, err := Ready(ctx, ReadyOptions{Endpoint: endpoint, Model: "test", UBatch: 128})
	if err != nil || !r.Ready {
		t.Fatalf("%+v %v", r, err)
	}
	t.Logf("decode=%v prefill=%g followup=%g spec=%v", r.DecodeTPS, r.PrefillTPS, r.FollowUpTTFT, r.SpecVerifySecs)
}

func TestReadyMissingUsageAndSlowDepth(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		if b["stream"] == true {
			w.Header().Set("Content-Type", "text/event-stream")
			time.Sleep(3 * time.Millisecond) // a nonzero first-token time on Windows' coarse clock
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello world\"}}]}\n\n")
			w.(http.Flusher).Flush()
			time.Sleep(3 * time.Millisecond)
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\" more words\"}}]}\n\ndata: [DONE]\n\n")
			return
		}
		fmt.Fprint(w, `{}`)
	}))
	defer s.Close()
	r, err := Ready(context.Background(), ReadyOptions{Endpoint: s.URL, ContextTokens: 512, Predicted: Predicted{DecodeTPSTarget: 1e9}})
	if err != nil || r.Ready || r.Failing != "spill" || !strings.Contains(r.Plain(), "below 50%") || !strings.Contains(r.Plain(), "estimated") || !strings.Contains(r.Plain(), "cache unknown") || !strings.Contains(r.Plain(), "inconclusive") {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestSweepCountsAndCache(t *testing.T) {
	for _, cache := range []int{0, 7} {
		t.Run(fmt.Sprint(cache), func(t *testing.T) {
			n := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var b map[string]any
				_ = json.NewDecoder(r.Body).Decode(&b)
				if b["stream"] == true {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n")
					w.(http.Flusher).Flush()
					time.Sleep(time.Millisecond)
					fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\" world\"}}],\"usage\":{\"prompt_tokens\":128,\"completion_tokens\":5,\"prompt_tokens_details\":{\"cached_tokens\":%d}}}\n\ndata: [DONE]\n\n", cache)
					return
				}
				n++
				fmt.Fprintf(w, `{"timings":{"prompt_n":%d,"cache_n":64,"prompt_ms":2}}`, n)
			}))
			defer s.Close()
			r, err := Ready(context.Background(), ReadyOptions{Endpoint: s.URL})
			want := "cache reused"
			if cache == 0 {
				want = "cache not reused"
			}
			if err != nil || !r.Ready || len(r.SpecVerifySecs) != 4 || !strings.Contains(r.Plain(), want) {
				t.Fatalf("%+v %v", r, err)
			}
		})
	}
}
