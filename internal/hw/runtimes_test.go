package hw

import (
	"context"
	"reflect"
	"testing"
)

func TestParseOllamaVersion(t *testing.T) {
	for _, f := range []string{"ollama-version.txt", "ollama-version-noserver.txt"} {
		if got := parseOllamaVersion(fixture(t, f)); got != "0.12.6" {
			t.Errorf("%s: got %q", f, got)
		}
	}
	if got := parseOllamaVersion("command not found"); got != "" {
		t.Errorf("garbage: %q", got)
	}
}

func TestParseLlamaCppVersion(t *testing.T) {
	cases := []struct{ file, ver, backend string }{
		{"llama-server-version-cuda.txt", "b6765", "cuda"},
		{"llama-server-version-metal.txt", "b6765", "metal"},
		{"llama-server-version-vulkan.txt", "b6710", "vulkan"},
	}
	for _, c := range cases {
		v, b := parseLlamaCppVersion(fixture(t, c.file))
		if v != c.ver || b != c.backend {
			t.Errorf("%s: got (%q, %q), want (%q, %q)", c.file, v, b, c.ver, c.backend)
		}
	}
	if v, b := parseLlamaCppVersion("version: 4567 (abc1234)\nbuilt with cc 13 for x86_64-linux-gnu\n"); v != "b4567" || b != "" {
		t.Errorf("cpu build: %q %q", v, b)
	}
	if got := llamaBuildInfoVersion("b6765-5cca2542"); got != "b6765" {
		t.Errorf("build_info: %q", got)
	}
}

func TestRuntimeHelpers(t *testing.T) {
	if got := parsePlistVersion([]byte(fixture(t, "lmstudio-info.plist"))); got != "0.3.30" {
		t.Errorf("plist: %q", got)
	}
	if m := dockerModelVersionRe.FindStringSubmatch(fixture(t, "docker-model-version.txt")); m == nil || m[1] != "0.1.44" {
		t.Errorf("docker: %v", m)
	}
	shebangs := map[string]string{
		"#!/home/dev/.venv/bin/python3.12\nimport sys\n":                              "/home/dev/.venv/bin/python3.12",
		"#!/usr/bin/env python3\n":                                                    "",
		"#!/bin/sh\n'''exec' /very/long/path/venv/bin/python3 \"$0\" \"$@\"\n' '''\n": "/very/long/path/venv/bin/python3",
		"\x7fELF\x02\x01": "",
	}
	for in, want := range shebangs {
		if got := shebangInterpreter([]byte(in)); got != want {
			t.Errorf("shebangInterpreter(%q) = %q, want %q", in, got, want)
		}
	}
	if got := distInfoVersion("/x/lib/python3.12/site-packages/mlx_lm-0.28.2.dist-info", "mlx_lm"); got != "0.28.2" {
		t.Errorf("dist-info: %q", got)
	}
	if got := distInfoVersion("/x/mlx-0.29.dist-info", "mlx_lm"); got != "" {
		t.Errorf("wrong dist matched: %q", got)
	}
}

func TestDetectRuntimesLinux(t *testing.T) {
	f := &fakeRunner{
		env: map[string]string{"HOME": "/home/dev"},
		paths: map[string]string{
			"ollama":       "/usr/local/bin/ollama",
			"llama-server": "/usr/local/bin/llama-server",
			"vllm":         "/home/dev/.venv/bin/vllm",
			"docker":       "/usr/bin/docker",
		},
		exists: map[string]bool{"/home/dev/.lmstudio/bin/lms": true},
		files: map[string]string{
			"/home/dev/.venv/bin/vllm": "#!/home/dev/.venv/bin/python3.12\n# -*- coding: utf-8 -*-\nimport re\nimport sys\nfrom vllm.entrypoints.cli.main import main\n",
			"/home/dev/.venv/lib/python3.12/site-packages/vllm-0.11.0.dist-info/METADATA": "Metadata-Version: 2.4\nName: vllm\nVersion: 0.11.0\n",
		},
		cmds: map[string]fakeOut{
			"/usr/local/bin/ollama --version":       {out: fixture(t, "ollama-version.txt")},
			"/usr/local/bin/llama-server --version": {out: fixture(t, "llama-server-version-cuda.txt")},
			"/usr/bin/docker model version":         {out: fixture(t, "docker-model-version.txt")},
		},
		http: map[string]fakeHTTP{
			ollamaURL + "/api/version":     {200, `{"version":"0.12.6"}`},
			llamaCppURL + "/health":        {200, `{"status":"ok"}`},
			llamaCppURL + "/props":         {200, `{"default_generation_settings":{"n_ctx":4096},"total_slots":1,"build_info":"b6765-5cca2542"}`},
			lmStudioURL + "/v1/models":     {200, `{"data":[{"id":"qwen/qwen3-8b","object":"model","owned_by":"organization_owner"}],"object":"list"}`},
			vllmURL + "/v1/models":         {200, `{"object":"list","data":[{"id":"Qwen/Qwen3-8B","object":"model","created":1762400000,"owned_by":"vllm","root":"Qwen/Qwen3-8B","max_model_len":32768}]}`},
			vllmURL + "/version":           {200, `{"version":"0.11.0"}`},
			lemonadeURL + "/api/v1/health": {404, `{"detail":"Not Found"}`},
		},
	}
	got := detectRuntimes(context.Background(), f, "linux")
	want := []InstalledRuntime{
		{ID: RuntimeOllama, Version: "0.12.6", Path: "/usr/local/bin/ollama", Running: true, Endpoint: ollamaURL, Source: "PATH"},
		{ID: RuntimeLMStudio, Path: "/home/dev/.lmstudio/bin/lms", Running: true, Endpoint: lmStudioURL, Source: "install path"},
		{ID: RuntimeLlamaCpp, Version: "b6765", Path: "/usr/local/bin/llama-server", Running: true, Endpoint: llamaCppURL, Backend: "cuda", Source: "PATH"},
		{ID: RuntimeVLLM, Version: "0.11.0", Path: "/home/dev/.venv/bin/vllm", Running: true, Endpoint: vllmURL, Source: "PATH"},
		{ID: RuntimeDocker, Version: "0.1.44", Path: "/usr/bin/docker", Source: "PATH"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got\n%+v\nwant\n%+v", got, want)
	}
}

func TestDetectRuntimesDarwin(t *testing.T) {
	f := &fakeRunner{
		env: map[string]string{"HOME": "/Users/dev"},
		paths: map[string]string{
			"mlx_lm.server": "/opt/homebrew/bin/mlx_lm.server",
			"llama-server":  "/opt/homebrew/bin/llama-server",
		},
		exists: map[string]bool{
			"/Applications/Ollama.app":                           true,
			"/Applications/Ollama.app/Contents/Resources/ollama": true,
			"/Applications/LM Studio.app":                        true,
			"/Applications/Jan.app":                              true,
		},
		files: map[string]string{
			"/Applications/LM Studio.app/Contents/Info.plist":                             fixture(t, "lmstudio-info.plist"),
			"/opt/homebrew/bin/mlx_lm.server":                                             "#!/opt/homebrew/opt/python@3.12/bin/python3.12\nimport sys\nfrom mlx_lm.server import main\n",
			"/opt/homebrew/lib/python3.12/site-packages/mlx_lm-0.28.2.dist-info/METADATA": "Name: mlx-lm\nVersion: 0.28.2\n",
		},
		cmds: map[string]fakeOut{
			"/Applications/Ollama.app/Contents/Resources/ollama --version": {out: fixture(t, "ollama-version-noserver.txt")},
			"/opt/homebrew/bin/llama-server --version":                     {out: fixture(t, "llama-server-version-metal.txt")},
		},
		http: map[string]fakeHTTP{
			janURL + "/v1/models": {200, `{"object":"list","data":[]}`},
			// /props unavailable (e.g. --api-key), but llama-server is installed.
			llamaCppURL + "/health": {200, `{"status":"ok"}`},
		},
	}
	got := detectRuntimes(context.Background(), f, "darwin")
	want := []InstalledRuntime{
		{ID: RuntimeOllama, Version: "0.12.6", Path: "/Applications/Ollama.app/Contents/Resources/ollama", Source: "install path"},
		{ID: RuntimeLMStudio, Version: "0.3.30", Path: "/Applications/LM Studio.app", Source: "app"},
		{ID: RuntimeLlamaCpp, Version: "b6765", Path: "/opt/homebrew/bin/llama-server", Backend: "metal", Running: true, Endpoint: llamaCppURL, Source: "PATH"},
		{ID: RuntimeMLXLM, Version: "0.28.2", Path: "/opt/homebrew/bin/mlx_lm.server", Backend: "metal", Source: "PATH"},
		{ID: RuntimeJan, Path: "/Applications/Jan.app", Running: true, Endpoint: janURL, Source: "app"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got\n%+v\nwant\n%+v", got, want)
	}
}

func TestDetectRuntimesWindows(t *testing.T) {
	const lad = `C:\Users\dev\AppData\Local`
	f := &fakeRunner{
		env:    map[string]string{"USERPROFILE": `C:\Users\dev`, "LOCALAPPDATA": lad},
		paths:  map[string]string{"lemonade-server": lad + `\lemonade_server\bin\lemonade-server.exe`},
		exists: map[string]bool{lad + `\Programs\Ollama\ollama.exe`: true, lad + `\Programs\LM Studio\LM Studio.exe`: true},
		files: map[string]string{
			lad + `\Microsoft\WinGet\Links\llama-server.exe`: "MZ",
			lad + `\Microsoft\WinGet\Links\ggml-vulkan.dll`:  "MZ",
		},
		cmds: map[string]fakeOut{
			lad + `\Programs\Ollama\ollama.exe --version`:              {out: "ollama version is 0.12.6\n"},
			lad + `\lemonade_server\bin\lemonade-server.exe --version`: {out: "lemonade-server 8.1.12\n"},
			lad + `\Microsoft\WinGet\Links\llama-server.exe --version`: {out: "version: 6800 (0123abc)\nbuilt with MSVC 19.44 for x64\n"},
		},
		http: map[string]fakeHTTP{
			ollamaURL + "/api/version":     {200, `{"version":"0.12.6"}`},
			lemonadeURL + "/api/v1/health": {200, `{"status":"ok","checkpoint_loaded":"unsloth/Qwen3-0.6B-GGUF:Q4_0","model_loaded":"Qwen3-0.6B-GGUF"}`},
		},
	}
	got := detectRuntimes(context.Background(), f, "windows")
	want := []InstalledRuntime{
		{ID: RuntimeOllama, Version: "0.12.6", Path: lad + `\Programs\Ollama\ollama.exe`, Running: true, Endpoint: ollamaURL, Source: "install path"},
		{ID: RuntimeLMStudio, Path: lad + `\Programs\LM Studio\LM Studio.exe`, Source: "app"},
		{ID: RuntimeLlamaCpp, Version: "b6800", Path: lad + `\Microsoft\WinGet\Links\llama-server.exe`, Backend: "vulkan", Source: "install path"},
		{ID: RuntimeLemonade, Version: "8.1.12", Path: lad + `\lemonade_server\bin\lemonade-server.exe`, Running: true, Endpoint: lemonadeURL, Source: "PATH"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got\n%+v\nwant\n%+v", got, want)
	}
}

func TestDetectRuntimesHTTPOnly(t *testing.T) {
	// Servers in containers: nothing on disk, but distinctive APIs answer.
	f := &fakeRunner{http: map[string]fakeHTTP{
		ollamaURL + "/api/version": {200, `{"version":"0.12.3"}`},
		llamaCppURL + "/health":    {200, `{"status":"ok"}`},
		llamaCppURL + "/props":     {200, `{"default_generation_settings":{},"build_info":"b6700-deadbee"}`},
		lmStudioURL + "/v1/models": {200, `{"data":[],"object":"list"}`}, // LM Studio needs an install
		vllmURL + "/v1/models":     {200, `{"object":"list","data":[{"id":"m","owned_by":"vllm"}]}`},
	}}
	got := detectRuntimes(context.Background(), f, "linux")
	want := []InstalledRuntime{
		{ID: RuntimeOllama, Version: "0.12.3", Running: true, Endpoint: ollamaURL, Source: "http"},
		{ID: RuntimeLlamaCpp, Version: "b6700", Running: true, Endpoint: llamaCppURL, Source: "http"},
		{ID: RuntimeVLLM, Running: true, Endpoint: vllmURL, Source: "http"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got\n%+v\nwant\n%+v", got, want)
	}
	// A generic /health on 8080 without llama.cpp's /props and no binary.
	f = &fakeRunner{http: map[string]fakeHTTP{llamaCppURL + "/health": {200, `{"status":"ok"}`}}}
	if got := detectRuntimes(context.Background(), f, "linux"); len(got) != 0 {
		t.Fatalf("unconfirmed llama.cpp reported: %+v", got)
	}
	if got := detectRuntimes(context.Background(), &fakeRunner{}, "linux"); got == nil || len(got) != 0 {
		t.Fatalf("empty machine: %#v", got)
	}
}

func TestDetectRuntimesAPIKeyServers(t *testing.T) {
	f := &fakeRunner{
		env:   map[string]string{"HOME": "/home/dev"},
		paths: map[string]string{"vllm": "/home/dev/.venv/bin/vllm", "jan": "/usr/bin/jan"},
		http: map[string]fakeHTTP{
			vllmURL + "/v1/models": {401, `{"error":"Unauthorized"}`},
			vllmURL + "/version":   {200, `{"version":"0.11.0"}`},
			janURL + "/v1/models":  {401, `{"message":"Unauthorized"}`},
		},
	}
	got := detectRuntimes(context.Background(), f, "linux")
	want := []InstalledRuntime{
		{ID: RuntimeVLLM, Version: "0.11.0", Path: "/home/dev/.venv/bin/vllm", Running: true, Endpoint: vllmURL, Source: "PATH"},
		{ID: RuntimeJan, Path: "/usr/bin/jan", Running: true, Endpoint: janURL, Source: "PATH"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got\n%+v\nwant\n%+v", got, want)
	}
}
