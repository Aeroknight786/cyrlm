package setup

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/aeroknight786/trialm/internal/engine"
	"github.com/aeroknight786/trialm/internal/harness"
	"github.com/aeroknight786/trialm/internal/setup/download"
)

func Build(in Input) (Plan, error) {
	p := Plan{
		Model:           in.Model.ID,
		ModelName:       in.Model.Name,
		Quant:           in.Quant.Name,
		Runtime:         in.Profile.ID,
		RuntimeName:     in.Profile.Name,
		OS:              in.OS,
		Arch:            in.Arch,
		Backend:         in.Backend,
		ContextTokens:   in.Settings.ContextTokens,
		KVCacheType:     in.Settings.KVCacheType,
		Tuned:           in.Settings.Tuned,
		Predicted:       in.Predicted,
		DecodeErrorPct:  in.DecodeErrorPct,
		PrefillErrorPct: in.PrefillErrorPct,
	}
	if p.Model == "" || p.Runtime == "" || in.OS == "" {
		return p, errors.New("setup requires model, engine and machine")
	}
	if !slices.Contains(in.Profile.Formats, in.Quant.Format) {
		return p, fmt.Errorf("%s cannot load %s", p.Runtime, in.Quant.Format)
	}
	if !slices.Contains(in.Profile.Backends(in.OS, in.Arch), in.Backend) {
		return p, fmt.Errorf("unsupported %s backend on %s/%s", in.Backend, in.OS, in.Arch)
	}
	p.Slug = slug(p.Model + "-" + p.Quant + "-" + p.Runtime)
	p.LocalModelName = modelName(in)
	if in.OwnedFileExists != nil {
		legacy := strings.TrimSuffix(p.LocalModelName, "-trialm") + "-cyrlm"
		file := strings.ReplaceAll(legacy, ":", "-")
		for _, rel := range []string{"ollama/" + file + ".Modelfile", "llamacpp/" + file + ".sh", "llamacpp/" + file + ".ps1", "mlx-lm/" + file + ".sh"} {
			if in.OwnedFileExists(".trialm/" + rel) {
				p.LocalModelName = legacy
				break
			}
		}
	}
	port := in.Port
	if port == 0 {
		port = in.Profile.Port
	}
	p.Endpoint = fmt.Sprintf("http://127.0.0.1:%d", port)
	p.Untouched = []string{"drivers and OS startup services", "existing model caches", "harness defaults"}
	if in.OS == "darwin" && in.RequiredGPUBytes > in.DefaultGPUBytes && in.DefaultGPUBytes > 0 {
		reserve := float64(4 << 30)
		if in.RAMBytes >= 64<<30 {
			reserve = 8 << 30
		}
		if in.RequiredGPUBytes > in.RAMBytes-reserve {
			return p, errors.New("wired limit would leave too little memory for macOS")
		}
		mb := int64(math.Ceil(in.RequiredGPUBytes / (1 << 20)))
		p.Steps = append(p.Steps, Step{
			ID:          "wired-limit",
			Kind:        Admin,
			Title:       "Raise GPU wired limit",
			Argv:        []string{"sudo", "sysctl", fmt.Sprintf("iogpu.wired_limit_mb=%d", mb)},
			RebootReset: true,
			Inverse:     []string{"sudo", "sysctl", "iogpu.wired_limit_mb=0"},
		})
	}
	values := map[string]string{
		"port":   fmt.Sprint(port),
		"ctx":    fmt.Sprint(p.ContextTokens),
		"kv":     p.KVCacheType,
		"name":   p.LocalModelName,
		"ngl":    "999",
		"fa":     "off",
		"output": fmt.Sprint(min(32768, max(4096, p.ContextTokens/4))),
	}
	if in.Backend == "cpu" {
		values["ngl"] = "0"
	}
	if in.Settings.FlashAttention {
		values["fa"] = "on"
	}
	var engineEnv []string
	binary := "llama-server"
	if in.OS == "windows" {
		binary += ".exe"
	}
	if in.Installed && in.Binary != "" {
		binary = in.Binary
	}
	if p.Runtime == "llamacpp" && !(in.Installed && engine.CompatibleLlama(in.InstalledVersion)) {
		a, err := engine.ReleaseAsset(in.OS, in.Arch, in.Backend)
		if err != nil {
			return p, err
		}
		dest := "~/.trialm/engines/" + p.Slug
		p.Steps = append(p.Steps, Step{
			ID:          "engine",
			Kind:        Install,
			Title:       "Install llama.cpp " + engine.Release(),
			Path:        dest,
			ArchiveRoot: a.Root,
			Request:     &download.Request{URL: a.URL, SHA256: a.SHA256, Size: a.Size, Dest: dest + ".archive"},
		})
		for i, support := range a.Support {
			supportDir := fmt.Sprintf("%s-runtime-%d", dest, i)
			p.Steps = append(p.Steps, Step{
				ID: fmt.Sprintf("engine-runtime-%d",
					i),
				Kind:        Install,
				Title:       "Install pinned CUDA user-space libraries",
				Path:        supportDir,
				ArchiveRoot: support.Root,
				Request:     &download.Request{URL: support.URL, SHA256: support.SHA256, Size: support.Size, Dest: supportDir + ".archive"},
			})
			if in.OS == "windows" {
				engineEnv = append(engineEnv, "PATH="+supportDir+";{PATH}")
			} else {
				engineEnv = append(engineEnv, "LD_LIBRARY_PATH="+supportDir+":{LD_LIBRARY_PATH}")
			}
		}
		binary = dest + "/llama-server"
		if in.OS == "windows" {
			binary += ".exe"
		}
	}
	adapterBinary := ""
	if in.Installed && (p.Runtime == "ollama" || p.Runtime == "mlx-lm") {
		adapterBinary = in.Binary
	}
	var adapterEnv []string
	if p.Runtime != "llamacpp" && !in.Installed {
		if p.Runtime == "mlx-lm" {
			argv, _ := engine.Args("mlx-install", values)
			dest := "~/.trialm/engines/" + p.Slug
			adapterEnv = []string{"UV_TOOL_DIR=" + dest + "/tools", "UV_TOOL_BIN_DIR=" + dest + "/bin", "UV_CACHE_DIR=" + dest + "/cache"}
			p.Steps = append(p.Steps, Step{ID: "engine", Kind: Install, Title: "Install allowlisted mlx-lm with uv", Package: "mlx-lm", Argv: argv, Env: adapterEnv, Path: dest})
			adapterBinary = dest + "/bin/mlx_lm.server"
		} else if p.Runtime == "ollama" {
			a, err := engine.OllamaAsset(in.OS, in.Arch)
			if err != nil {
				return p, err
			}
			if strings.HasSuffix(a.URL, ".tar.zst") {
				p.Steps = append(p.Steps, Step{ID: "decompressor", Kind: Check, Title: "Check zstd is installed for the Ollama archive", Argv: []string{"zstd", "--version"}})
			}
			dest := "~/.trialm/engines/" + p.Slug
			p.Steps = append(p.Steps, Step{
				ID:      "engine",
				Kind:    Install,
				Title:   "Install pinned user-space Ollama " + engine.OllamaRelease(),
				Path:    dest,
				Request: &download.Request{URL: a.URL, SHA256: a.SHA256, Size: a.Size, Dest: dest + ".archive"},
			})
			adapterBinary = dest + "/" + a.Entry
		} else {
			installer := engine.LMSInstaller()
			dest := "~/.trialm/engines/" + p.Slug
			adapterEnv = []string{"HOME=" + dest + "/home"}
			script := dest + "/install.sh"
			installerArgs := []string{"sh", script, "--no-modify-path", "--quiet"}
			if in.OS == "windows" {
				installer = engine.LMSWindowsInstaller()
				script = dest + "/install.ps1"
				installerArgs = []string{"powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script}
				adapterEnv = append(adapterEnv, "USERPROFILE="+dest+"/home", "LOCALAPPDATA="+dest+"/home/AppData/Local", "APPDATA="+dest+"/home/AppData/Roaming", "LMS_NO_MODIFY_PATH=1", "LMS_PRINT_QUIET=1")
			}
			p.Steps = append(p.Steps, Step{
				ID:      "engine",
				Kind:    RunInstaller,
				Title:   "Install pinned LM Studio daemon",
				Path:    dest,
				Content: installer.Source,
				Argv:    installerArgs,
				Env:     adapterEnv,
				Request: &download.Request{URL: installer.URL, SHA256: installer.SHA256, Size: installer.Size, Dest: script},
			})
			adapterBinary = dest + "/home/.lmstudio/bin/lms"
			if in.OS == "windows" {
				adapterBinary += ".exe"
			}
			p.Warnings = append(p.Warnings, "The pinned LM Studio installer fetches its platform runtime. Its additional bytes are unknown.")
		}
	}
	model := in.LocalModel
	for i, asset := range in.Assets {
		asset.Token = ""
		asset.Reuse = nil
		asset.Dest = "~/.trialm/models/" + p.Slug + "/" + asset.Dest
		p.Steps = append(p.Steps, Step{ID: fmt.Sprintf("model-%d", i), Kind: Fetch, Title: "Fetch " + path.Base(asset.Dest), Request: &asset})
		if model == "" {
			model = asset.Dest
		}
	}
	if model == "" {
		// Offline what-if plans describe the pending resolution and cannot execute.
		model = "~/.trialm/models/" + p.Slug + "/" + in.Quant.HFTag() + ".gguf"
		p.Warnings = append(p.Warnings, "Resolve Hugging Face file digests on this machine before applying this what-if plan.")
		p.Steps = append(p.Steps, Step{ID: "resolve", Kind: Check, Title: "Resolve pinned model files", Content: "model file digests are unresolved", DownloadBytes: in.Quant.FileBytes})
	}
	if in.Quant.Format == "mlx" {
		if in.LocalModel == "" {
			model = "~/.trialm/models/" + p.Slug
		}
		p.LocalModelName = model
		values["name"] = model
	}
	if in.Model.TotalParamsB > 0 {
		p.BitsPerWeight = float64(in.Quant.FileBytes) * 8 / (in.Model.TotalParamsB * 1e9)
	}
	values["model"], values["binary"] = model, binary
	switch p.Runtime {
	case "llamacpp", "mlx-lm":
		template := p.Runtime
		if template == "llamacpp" && in.Backend == "cpu" {
			template = "llamacpp-cpu"
		}
		argv, err := engine.Args(template, values)
		if err != nil {
			return p, err
		}
		if p.Runtime == "mlx-lm" && p.KVCacheType != "f16" {
			bits := map[string]string{"q8_0": "8", "q4_0": "4"}[p.KVCacheType]
			if bits == "" {
				return p, fmt.Errorf("unsupported MLX KV cache %s", p.KVCacheType)
			}
			values["kvbits"] = bits
			kvArgs, _ := engine.Args("mlx-kv", values)
			argv = append(argv, kvArgs...)
		}
		p.Steps = append(p.Steps, Step{ID: "server", Kind: Start, Title: "Start " + p.RuntimeName, Argv: argv, Env: engineEnv, Health: p.Endpoint + "/health"})
	case "ollama":
		values["config"] = "~/.trialm/ollama/" + p.LocalModelName + ".Modelfile"
		content := fmt.Sprintf("FROM %s\nPARAMETER num_ctx %d\n", model, p.ContextTokens)
		p.Steps = append(p.Steps, Step{ID: "modelfile", Kind: Write, Title: "Write Ollama Modelfile", Path: values["config"], Content: content})
		keepAlive := int64(in.Settings.KeepAlive.Seconds())
		if in.Settings.KeepAlive < 0 {
			keepAlive = -1
		}
		serverArgs, _ := engine.Args("ollama", values)
		p.Steps = append(p.Steps, Step{
			ID:    "server",
			Kind:  Start,
			Title: "Start Ollama",
			Argv:  serverArgs,
			Env: []string{
				"OLLAMA_HOST=" + p.Endpoint,
				"OLLAMA_MODELS=~/.trialm/models/ollama",
				"OLLAMA_FLASH_ATTENTION=" + fmt.Sprint(in.Settings.FlashAttention),
				"OLLAMA_KV_CACHE_TYPE=" + p.KVCacheType,
				"OLLAMA_KEEP_ALIVE=" + fmt.Sprint(keepAlive),
				"OLLAMA_NUM_PARALLEL=1",
			},
			Health: p.Endpoint + "/api/tags",
		})
		argv, _ := engine.Args("ollama-create", values)
		p.Steps = append(p.Steps, Step{
			ID:      "register",
			Kind:    Install,
			Title:   "Register Ollama model",
			Package: "ollama-model",
			Argv:    argv,
			Env:     []string{"OLLAMA_HOST=" + p.Endpoint, "OLLAMA_MODELS=~/.trialm/models/ollama"},
			Inverse: []string{"ollama", "rm", p.LocalModelName},
		})
	case "lmstudio", "lmstudio-mlx":
		if p.KVCacheType != "f16" || in.Settings.FlashAttention != in.Profile.Defaults.FlashAttention {
			p.Warnings = append(p.Warnings, "Set the selected KV cache and flash attention in LM Studio's model load settings. Its CLI does not expose these switches.")
		}
		values["namespace"] = "trialm/" + p.Slug
		importArgs, _ := engine.Args("lmstudio-import", values)
		p.Steps = append(p.Steps, Step{ID: "import", Kind: Install, Title: "Import model into LM Studio", Package: "lms-import", Argv: importArgs, NativeRepo: values["namespace"]})
		values["model"] = values["namespace"] + "/" + path.Base(model)
		argv, _ := engine.Args("lmstudio-load", values)
		p.Steps = append(p.Steps, Step{ID: "load", Kind: Install, Title: "Load LM Studio model", Package: "lms-model", Argv: argv, Inverse: []string{"lms", "unload", p.LocalModelName}})
		argv, _ = engine.Args("lmstudio", values)
		p.Steps = append(p.Steps, Step{
			ID:      "server",
			Kind:    Start,
			Title:   "Start LM Studio server",
			Argv:    argv,
			Inverse: []string{"lms", "server", "stop"},
			Health:  p.Endpoint + "/v1/models",
		})
	default:
		return p, fmt.Errorf("no adapter for %s", p.Runtime)
	}
	p.ReadyOptions.Endpoint = p.Endpoint
	p.ReadyOptions.Model = p.LocalModelName
	p.ReadyOptions.ContextTokens = p.ContextTokens
	p.ReadyOptions.Predicted = p.Predicted
	p.ReadyOptions.Harness = in.Harness
	p.ReadyOptions.APIs = in.Profile.APIs
	p.APIs = in.Profile.APIs
	p.CheckEndpoint = p.Endpoint
	if in.Harness != nil {
		h := in.Harness
		p.Harness = h.ID
		bridge := h.NeedsBridge(in.Profile.APIs)
		harnessPort := port
		if bridge {
			harnessPort = 11535
			p.Steps = append(p.Steps, Step{
				ID:    "bridge",
				Kind:  Start,
				Title: "Start harness bridge",
				Argv:  []string{"trialm", "bridge", "--endpoint", p.Endpoint, "--model", p.LocalModelName, "--harness", h.ID, "--ctx", fmt.Sprint(p.ContextTokens), "--port", fmt.Sprint(harnessPort)},
				Health: fmt.Sprintf("http://127.0.0.1:%d/health",
					harnessPort),
			})
			p.ReadyOptions.Endpoint = fmt.Sprintf("http://127.0.0.1:%d", harnessPort)
			p.CheckEndpoint = p.ReadyOptions.Endpoint
			p.APIs = []string{"openai-chat", "anthropic", "responses"}
		}
		wiring := h.SetupFor(harness.Options{
			Port:          harnessPort,
			Model:         p.LocalModelName,
			Tuned:         p.Tuned,
			ContextTokens: p.ContextTokens,
			Protocol:      h.ProtocolFor(in.Profile.APIs),
			Bridge:        true,
			Home:          "~/.trialm",
		})
		keys := make([]string, 0, len(wiring.Files))
		for key := range wiring.Files {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			p.Steps = append(p.Steps, Step{ID: fmt.Sprintf("harness-%d", len(p.Steps)), Kind: Write, Title: "Write " + h.Name + " config", Path: key, Content: wiring.Files[key]})
		}
	}
	for i := range p.Steps {
		step := &p.Steps[i]
		if adapterBinary != "" && len(step.Argv) > 0 && (step.Argv[0] == "ollama" || step.Argv[0] == "lms" || step.Argv[0] == "mlx_lm.server") {
			step.Argv[0] = adapterBinary
			step.Env = append(step.Env, adapterEnv...)
		}
		if adapterBinary != "" && len(step.Inverse) > 0 {
			step.Inverse[0] = adapterBinary
		}
	}
	// Persist launch argv and verification inputs for the launch client and resume.
	config, _ := json.Marshal(struct {
		Steps           []Step
		Endpoint, Model string
		Context         int
	}{p.Steps, p.Endpoint, p.LocalModelName, p.ContextTokens})
	p.Steps = append(p.Steps, Step{ID: "recipe", Kind: Write, Title: "Save setup recipe", Path: "~/.trialm/setups/" + p.Slug + "/recipe.json", Content: string(config)}, Step{ID: "ready", Kind: Check, Title: "Verify Ready"})
	p.Seal()
	return p, nil
}
