package harness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestIDESnippets(t *testing.T) {
	for _, id := range []string{"cline", "continue", "zed"} {
		t.Run(id, func(t *testing.T) {
			h, _ := Get(id)
			root := t.TempDir()
			s, err := h.Snippet(Options{Home: root, Model: `local-"model`, ContextTokens: 32768}, "http://127.0.0.1:4321")
			if err != nil {
				t.Fatal(err)
			}
			if err = s.WriteFiles(); err != nil {
				t.Fatal(err)
			}
			var cfg map[string]any
			for path := range s.Files {
				b, err := os.ReadFile(path)
				if err != nil || json.Unmarshal(b, &cfg) != nil {
					t.Fatalf("invalid JSON / YAML 1.2 %s: %s %v", path, b, err)
				}
				rel, _ := filepath.Rel(filepath.Join(root, "harness"), path)
				if rel == ".." || filepath.IsAbs(rel) {
					t.Fatal(path)
				}
			}
			base, model, key := "", "", ""
			switch id {
			case "cline":
				base, _ = cfg["openAiBaseUrl"].(string)
				model, _ = cfg["openAiModelId"].(string)
				key, _ = cfg["openAiApiKey"].(string)
			case "continue":
				if cfg["name"] != "triaLM" || cfg["schema"] != "v1" || cfg["version"] != "0.0.1" {
					t.Fatal(cfg)
				}
				row := cfg["models"].([]any)[0].(map[string]any)
				base = row["apiBase"].(string)
				model = row["model"].(string)
				key = row["apiKey"].(string)
			case "zed":
				provider := cfg["language_models"].(map[string]any)["openai_compatible"].(map[string]any)["triaLM"].(map[string]any)
				base = provider["api_url"].(string)
				model = provider["available_models"].([]any)[0].(map[string]any)["name"].(string)
				if len(s.Env) != 1 || s.Env[0][0] != "TRIALM_API_KEY" {
					t.Fatal(s.Env)
				}
				key = s.Env[0][1]
				if provider["api_key"] != nil {
					t.Fatal("Zed keys belong in the environment")
				}
			}
			if base != "http://127.0.0.1:4321/v1" || model != `local-"model` || key != "trialm" {
				t.Fatalf("%s %s %s", base, model, key)
			}
		})
	}
}
