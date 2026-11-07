package engine

import "testing"

func TestProfilesLoadAndAliases(t *testing.T) {
	if len(All()) < 5 {
		t.Fatalf("expected ≥5 profiles, got %d", len(All()))
	}
	for _, alias := range []string{"llama.cpp", "llama-server", "lm-studio", "mlx", "Ollama"} {
		if _, err := Get(alias); err != nil {
			t.Errorf("Get(%q): %v", alias, err)
		}
	}
	if _, err := Get("nope"); err == nil {
		t.Error("unknown runtime should error")
	}
}

func TestChooseBackend(t *testing.T) {
	ol, _ := Get("ollama")
	if b, ok := ol.ChooseBackend("linux", "amd64", []string{"cuda", "vulkan"}); !ok || b != "cuda" {
		t.Errorf("ollama on NVIDIA linux: %s %v", b, ok)
	}
	if b, ok := ol.ChooseBackend("darwin", "arm64", []string{"metal"}); !ok || b != "metal" {
		t.Errorf("ollama on Mac: %s %v", b, ok)
	}
	mlx, _ := Get("mlx-lm")
	if _, ok := mlx.ChooseBackend("linux", "amd64", []string{"cuda"}); ok {
		t.Error("mlx-lm must not run on Linux")
	}
	if b, ok := mlx.ChooseBackend("darwin", "arm64", []string{"metal"}); !ok || b != "mlx" {
		t.Errorf("mlx-lm on Mac: %s %v", b, ok)
	}
	lc, _ := Get("llamacpp")
	if b, _ := lc.ChooseBackend("linux", "amd64", []string{"rocm", "vulkan"}); b != "vulkan" {
		t.Errorf("llama.cpp on AMD should default to vulkan, got %s", b)
	}
}

func TestResolve(t *testing.T) {
	ol, _ := Get("ollama")
	s := ol.Resolve(ResolveInput{GPUMemGiB: 16, NativeCtx: 131072})
	if s.ContextTokens != 4096 || s.KeepAlive.Minutes() != 5 || s.Slots != 1 {
		t.Errorf("ollama 16 GiB defaults: %+v", s)
	}
	if len(s.Assumptions) == 0 {
		t.Error("low-confidence settings must be surfaced as assumptions")
	}
	tuned := ol.Resolve(ResolveInput{GPUMemGiB: 16, NativeCtx: 131072, Tuned: true, RecommendedCtx: 65536})
	if tuned.ContextTokens != 65536 || tuned.KeepAlive >= 0 {
		t.Errorf("ollama tuned: ctx %d keepalive %v", tuned.ContextTokens, tuned.KeepAlive)
	}
	lc, _ := Get("llamacpp")
	s = lc.Resolve(ResolveInput{GPUMemGiB: 24, NativeCtx: 131072, MaxFitCtx: 50000})
	if s.ContextTokens != 50000 || s.Overflow != OverflowError || s.Slots != 4 {
		t.Errorf("llama.cpp defaults: %+v", s)
	}
	unknown := lc.Resolve(ResolveInput{NativeCtx: 131072})
	if unknown.ContextTokens != 4096 {
		t.Errorf("unknown fit: got %d", unknown.ContextTokens)
	}
	capped := lc.Resolve(ResolveInput{NativeCtx: 32768, CtxOverride: 131072})
	if capped.ContextTokens != 32768 {
		t.Errorf("context must be capped at native, got %d", capped.ContextTokens)
	}
	if got := ol.TunedSteps(65536)[0]; got != "Set the context length: OLLAMA_CONTEXT_LENGTH=65536 (or num_ctx in a Modelfile)" {
		t.Errorf("tuned step %q", got)
	}
}

func TestInstalledLlamaVersionRange(t *testing.T) {
	for version, want := range map[string]bool{"b11499": false, "b11500": true, "b12182": true, "b12183": false, "unknown": false, "version: 0.9.0-dev (build 12182, commit fd27b2034)": true} {
		if got := CompatibleLlama(version); got != want {
			t.Errorf("%s compatible=%v, want %v", version, got, want)
		}
	}
}

func TestTemplateValuesRemainLiteralArguments(t *testing.T) {
	values := map[string]string{"model": "literal-{ctx}-model", "name": "test", "ctx": "4096"}
	argv, err := Args("lmstudio-load", values)
	if err != nil || argv[2] != values["model"] {
		t.Fatal(argv, err)
	}
}
