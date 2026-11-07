package verify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aeroknight786/trialm/internal/harness"
)

func TestChatHarnessStreamingCheck(t *testing.T) {
	var mu sync.Mutex
	count := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		count++
		var b map[string]any
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			t.Error(err)
			return
		}
		if b["stream"] != true || b["stream_options"].(map[string]any)["include_usage"] != true {
			t.Errorf("stream flags %v", b)
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("auth %s", r.Header.Get("Authorization"))
		}
		if count > 1 && b["tool_choice"] != "auto" {
			t.Errorf("tool choice %v", b)
		}
		if count == 3 {
			msgs := b["messages"].([]any)
			assistant := msgs[len(msgs)-2].(map[string]any)
			if assistant["content"] != "" || assistant["reasoning_content"] != "plan" {
				t.Errorf("assistant %v", assistant)
			}
			call := assistant["tool_calls"].([]any)[0].(map[string]any)
			if call["function"].(map[string]any)["arguments"] != `{"value":"ok"}` {
				t.Errorf("call %v", call)
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if count == 2 {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"plan\",\"tool_calls\":[{\"index\":0,\"id\":\"c1\",\"function\":{\"name\":\"record_\",\"arguments\":\"{\\\"value\\\":\"}}]}}]}\n\n")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"result\",\"arguments\":\"\\\"ok\\\"}\"}}]}}]}\n\n")
		} else {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\n")
		}
		fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n")
	}))
	defer ts.Close()
	h, _ := harness.Get("qwen-code")
	hc := HarnessCheckRun(context.Background(), ts.URL, "m", "secret", h, []string{"openai-chat"})
	if !hc.Passed {
		t.Fatalf("check %+v", hc)
	}
	mu.Lock()
	defer mu.Unlock()
	if count != 3 {
		t.Fatalf("requests %d", count)
	}
}

func TestAiderHarnessChecks(t *testing.T) {
	for _, ollama := range []bool{false, true} {
		t.Run(fmt.Sprint(ollama), func(t *testing.T) {
			var mu sync.Mutex
			count := 0
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				count++
				var b map[string]any
				json.NewDecoder(r.Body).Decode(&b)
				path := "/v1/chat/completions"
				if ollama {
					path = "/api/chat"
				}
				if r.URL.Path != path {
					t.Errorf("path %s", r.URL.Path)
				}
				if _, ok := b["tools"]; ok {
					t.Error("aider must use edits")
				}
				if ollama && b["options"].(map[string]any)["num_ctx"] != 8192.0 {
					t.Errorf("options %v", b)
				}
				msgs := b["messages"].([]any)
				if count == 2 {
					first := msgs[0].(map[string]any)
					if !strings.Contains(first["content"].(string), "whole-file") || len(msgs) < 5 || !strings.Contains(msgs[len(msgs)-1].(map[string]any)["content"].(string), "tool.txt") {
						t.Errorf("edit messages %v", msgs)
					}
				}
				if count == 3 {
					if b["stream"] != false || msgs[len(msgs)-1].(map[string]any)["role"] != "system" {
						t.Errorf("follow-up %v", b)
					}
				} else if b["stream"] != true {
					t.Errorf("stream %v", b)
				}
				text := "OK"
				if count == 2 {
					text = "tool.txt\n```text\nok\n```"
				}
				if count == 3 {
					w.Header().Set("Content-Type", "application/json")
					if ollama {
						json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"content": text}})
					} else {
						json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": text}}}})
					}
				} else if ollama {
					w.Header().Set("Content-Type", "application/x-ndjson")
					json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"content": text}, "done": true})
				} else {
					w.Header().Set("Content-Type", "text/event-stream")
					chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": text}}}})
					fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
				}
			}))
			defer ts.Close()
			h, _ := harness.Get("aider")
			apis := []string{"openai-chat"}
			if ollama {
				apis = []string{"ollama", "openai-chat"}
			}
			hc := HarnessCheckRun(context.Background(), ts.URL, "m", "key", h, apis)
			if !hc.Passed {
				t.Fatalf("check %+v", hc)
			}
			mu.Lock()
			defer mu.Unlock()
			if count != 3 {
				t.Fatalf("requests %d", count)
			}
		})
	}
}

func TestCheckStreamFailures(t *testing.T) {
	for _, input := range []string{"", "data: {\n\n", "data: {\"error\":{\"message\":\"bad\"}}\n\n", "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":-1}]}}]}\n\n"} {
		if _, err := readChatStream(strings.NewReader(input)); err == nil {
			t.Errorf("accepted %q", input)
		}
	}
	if _, err := readOllamaCheckStream(strings.NewReader(`{"message":{"content":"partial"}}`)); err == nil {
		t.Error("accepted truncated native stream")
	}
	for _, text := range []string{"tool.txt", "tool.txt\n```text\nok", "other.txt\n```text\nok\n```"} {
		if aiderEditBlock(text) {
			t.Errorf("accepted %q", text)
		}
	}
}
