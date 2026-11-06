package hub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRepoFromURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://huggingface.co/huihui-ai/X-GGUF/resolve/main/x.gguf": "huihui-ai/X-GGUF",
		"https://huggingface.co/Qwen/Qwen3.6-35B-A3B":                 "Qwen/Qwen3.6-35B-A3B",
		"https://huggingface.co/org/repo/blob/main/a/b.gguf":          "org/repo",
	} {
		if got, ok := RepoFromURL(in); !ok || got != want {
			t.Errorf("RepoFromURL(%q) = %q, %v", in, got, ok)
		}
	}
	for _, bad := range []string{"https://example.com/a/b/c.gguf", "https://huggingface.co/api/models", "/local/file.gguf"} {
		if _, ok := RepoFromURL(bad); ok {
			t.Errorf("RepoFromURL(%q) should fail", bad)
		}
	}
}

func TestBaseChain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/models/someone/Model-abliterated-GGUF":
			w.Write([]byte(`{"cardData":{"base_model":["someone/Model-abliterated"]},"tags":[]}`))
		case "/api/models/someone/Model-abliterated":
			w.Write([]byte(`{"cardData":{"base_model":"Org/Model-7B"},"tags":[]}`))
		case "/api/models/Org/Model-7B":
			w.Write([]byte(`{"cardData":{},"tags":["base_model:finetune:Org/Model-7B-Base"]}`))
		case "/api/models/Org/Model-7B-Base":
			w.Write([]byte(`{"cardData":{},"tags":[]}`))
		case "/api/models/loop/A":
			w.Write([]byte(`{"cardData":{"base_model":"loop/B"}}`))
		case "/api/models/loop/B":
			w.Write([]byte(`{"cardData":{"base_model":"loop/A"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	API = srv.URL
	chain := BaseChain(context.Background(), "someone/Model-abliterated-GGUF", 5)
	want := []string{"someone/Model-abliterated", "Org/Model-7B", "Org/Model-7B-Base"}
	if len(chain) != len(want) {
		t.Fatalf("chain %v, want %v", chain, want)
	}
	for i := range want {
		if chain[i] != want[i] {
			t.Fatalf("chain %v, want %v", chain, want)
		}
	}
	if c := BaseChain(context.Background(), "loop/A", 10); len(c) > 2 {
		t.Fatalf("cycle not stopped: %v", c)
	}
	if c := BaseChain(context.Background(), "missing/repo", 3); len(c) != 0 {
		t.Fatalf("missing repo: %v", c)
	}
}
