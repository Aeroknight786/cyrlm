package harness

import (
	"fmt"
	"strings"
)

// Snippet returns a complete private example for an IDE's settings format.
// Continue's fixed YAML is shared with the tested CLI sibling.
func (h Harness) Snippet(o Options, endpoint string) (Setup, error) {
	base := strings.TrimRight(endpoint, "/") + "/v1"
	switch h.ID {
	case "continue":
		cfg := map[string]any{"name": "triaLM", "version": "0.0.1", "schema": "v1", "models": []any{map[string]any{"name": o.Model + " (triaLM)", "provider": "openai", "model": o.Model, "apiBase": base, "apiKey": "trialm", "roles": []string{"chat", "edit", "apply"}, "capabilities": []string{"tool_use"}, "defaultCompletionOptions": map[string]int{"contextLength": o.ContextTokens, "maxTokens": min(4096, o.ContextTokens/8)}}}}
		// JSON is a YAML 1.2 subset, parsed by Continue's YAML loader.
		return Setup{Files: map[string]string{o.ownedPath("continue/ide.config.yaml"): prettyJSON(cfg)}}, nil
	case "cline":
		settings := map[string]any{"apiProvider": "openai", "openAiBaseUrl": base, "openAiModelId": o.Model, "openAiApiKey": "trialm", "openAiModelInfo": map[string]any{"contextWindow": o.ContextTokens, "supportsTools": true}}
		return Setup{Files: map[string]string{o.ownedPath("cline/extension.json"): prettyJSON(settings)}}, nil
	case "zed":
		cfg := map[string]any{"language_models": map[string]any{"openai_compatible": map[string]any{"trialm": map[string]any{"api_url": base, "available_models": []any{map[string]any{"name": o.Model, "display_name": o.Model, "max_tokens": o.ContextTokens, "capabilities": map[string]bool{"tools": true}}}}}}}
		return Setup{Files: map[string]string{o.ownedPath("zed/settings.json"): prettyJSON(cfg)}, Env: [][2]string{{"TRIALM_API_KEY", "trialm"}}, Command: []string{"zed"}}, nil
	case "generic", "":
		// Any other agent: one local route that speaks every protocol and
		// reshapes requests so the model's own chat template accepts them.
		return Setup{Notes: []string{"OpenAI Chat Completions and Responses: base URL " + base, "Anthropic Messages: base URL " + strings.TrimSuffix(base, "/v1") + " (POST /v1/messages)", "Ollama: base URL " + strings.TrimSuffix(base, "/v1") + " (POST /api/chat)", "Model " + o.Model + " · API key: any non-empty value, e.g. trialm"}}, nil
	}
	return Setup{}, fmt.Errorf("--serve has settings for cline, continue and zed; any other agent: trialm launch --serve")
}
