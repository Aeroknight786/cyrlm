package setup

import (
	"fmt"
	"strings"
)

const (
	docLMSCLI      = "https://lmstudio.ai/docs/cli"
	docLMSRepo     = "https://github.com/lmstudio-ai/lms"
	docLMSDownload = "https://lmstudio.ai/download"
	docLMSHeadless = "https://lmstudio.ai/docs/developer/core/headless"
	docLMSPerModel = "https://lmstudio.ai/docs/app/advanced/per-model"
)

// lmsWinApp is where the per-user LM Studio installer puts the app.
const lmsWinApp = `$env:LOCALAPPDATA\Programs\LM Studio\LM Studio.exe`

func (b *builder) lmstudio() error {
	pl := b.plan
	m, q := b.in.Model, b.in.Quant
	mlx := b.p.Engine == "mlx"
	repo := m.HFGGUFRepo
	if mlx {
		repo = mlxRepo(m, q)
	}
	if repo == "" {
		return fmt.Errorf("no Hugging Face %s repo is known for %s", map[bool]string{true: "MLX", false: "GGUF"}[mlx], m.Name)
	}
	lms := b.lmsBin()

	b.checkStep([]Command{
		b.lmsCmd(lms + " version"),
		b.any(b.curl() + " -s " + pl.Endpoint + "/v1/models"),
	}, docLMSCLI)

	// install the app
	inst := Step{ID: "install-lmstudio", Kind: KindInstall, Title: "Install LM Studio", Docs: []string{docLMSDownload}}
	switch b.in.OS {
	case "darwin":
		inst.Commands = []Command{b.os("brew install --cask lm-studio"), b.os(`open -a "LM Studio"`)}
		inst.Undo = []Command{b.os("brew uninstall --cask lm-studio")}
		inst.Why = "Installs the LM Studio app with Homebrew and opens it once (first start creates ~/.lmstudio and the lms CLI). Without Homebrew, download it from https://lmstudio.ai/download."
		inst.Confidence = High
	case "windows":
		inst.Commands = []Command{
			b.os("winget install --id ElementLabs.LMStudio -e --accept-package-agreements --accept-source-agreements"),
			b.os(`Start-Process "` + lmsWinApp + `"`),
		}
		inst.Undo = []Command{b.os("winget uninstall --id ElementLabs.LMStudio -e")}
		inst.Why = "Installs LM Studio with winget and opens it once (first start creates ~/.lmstudio and the lms CLI). If the app is not at the usual per-user location, start it from the Start menu. Without winget, download it from https://lmstudio.ai/download."
		inst.Confidence = Medium
	default:
		inst.Why = "Manual: download the Linux AppImage from https://lmstudio.ai/download, make it executable (chmod +x LM-Studio-*.AppImage) and start it once (first start creates ~/.lmstudio and the lms CLI). For a server without a desktop, install llmster, LM Studio's headless core, with `curl -fsSL https://lmstudio.ai/install.sh | bash` and start it with `lms daemon up` (load settings that need the app must then be skipped)."
		inst.Confidence = Medium
		inst.Docs = append(inst.Docs, docLMSHeadless)
	}
	b.markInstalled(&inst)
	b.add(inst)

	// put lms on PATH
	if !b.in.Installed {
		boot := Step{
			ID: "install-lms-cli", Kind: KindInstall,
			Title:      "Add the lms command-line tool to PATH",
			Why:        "lms ships with LM Studio (in ~/.lmstudio/bin) once the app has been started. `bootstrap` adds it to PATH for new terminals; the following steps call it by its full path, so they work in this terminal too. If it fails, try `npx lmstudio install-cli`.",
			Commands:   []Command{b.lmsCmd(lms + " bootstrap --yes")},
			Confidence: Medium,
			Docs:       []string{docLMSRepo},
		}
		b.add(boot)
	}

	// download
	var get string
	if mlx {
		get = lms + " get " + b.q("https://huggingface.co/"+repo) + " --mlx -y"
	} else {
		get = lms + " get " + b.q("https://huggingface.co/"+repo+"@"+q.Name) + " --gguf -y"
	}
	dl := Step{
		ID: "download-model", Kind: KindDownload,
		Title:         fmt.Sprintf("Download %s %s (%s)", m.Name, q.Name, gbString(q.FileBytes)),
		Commands:      []Command{b.lmsCmd(get)},
		DownloadBytes: q.FileBytes,
		Confidence:    High,
		Docs:          []string{docLMSCLI, docLMSRepo},
	}
	if mlx {
		dl.Why = fmt.Sprintf("Downloads the MLX build %s from Hugging Face (lms get accepts a full huggingface.co URL; --mlx restricts it to MLX files, -y skips prompts). lms starts LM Studio in the background if it is not running.", repo)
	} else {
		dl.Why = fmt.Sprintf("Downloads the exact %s GGUF from %s (lms get takes a full huggingface.co URL, with the quantization after @; --gguf restricts it to GGUF files, -y skips prompts). lms starts LM Studio in the background if it is not running.", q.Name, repo)
	}
	b.add(dl)

	// configure: settings lms load has no flags for.
	var manual []string
	if !mlx && pl.FlashAttention && !b.p.Defaults.FlashAttention {
		manual = append(manual, "Flash Attention: On")
	}
	if !mlx && !pl.FlashAttention && b.p.Defaults.FlashAttention {
		manual = append(manual, "Flash Attention: Off")
	}
	if pl.KVCacheType != "f16" {
		if mlx {
			manual = append(manual, "KV Cache Quantization: "+kvBits(pl.KVCacheType)+"-bit")
		} else {
			kv := strings.ToUpper(pl.KVCacheType)
			manual = append(manual, "K Cache Quantization Type: "+kv, "V Cache Quantization Type: "+kv)
		}
	}
	if len(manual) > 0 {
		b.add(Step{
			ID: "configure-load-settings", Kind: KindConfigure,
			Title: "Set " + strings.Join(manual, ", ") + " in LM Studio's load settings",
			Why: "Manual: `lms load` has no flags for these settings, so set them as this model's default load settings in the app: My Models → " + repoBase(repo) + " → gear icon → " +
				strings.Join(manual, "; ") + ". Per-model defaults also apply when lms or the API loads the model. Leave Context Length alone: the start step sets it.",
			Confidence: Low,
			Docs:       []string{docLMSPerModel},
		})
		b.warn("LM Studio: " + strings.Join(manual, ", ") + " must be set by hand in the app (no lms flag); until then the model runs without " + map[bool]string{true: "them", false: "it"}[len(manual) > 1] + ".")
	}

	// service: API server
	b.add(Step{
		ID: "service-lms-server", Kind: KindService,
		Title:      "Start LM Studio's API server on port " + fmt.Sprint(b.port),
		Why:        "Serves the OpenAI-compatible (Chat Completions and Responses) and Anthropic-compatible APIs at " + pl.Endpoint + ".",
		Commands:   []Command{b.lmsCmd(lms + " server start --port " + fmt.Sprint(b.port))},
		Undo:       []Command{b.lmsCmd(lms + " server stop")},
		Confidence: High,
		Docs:       []string{docLMSCLI, docLMSRepo},
	})

	// start: load with the context and an identifier the harness uses.
	key := strings.ToLower(repoBase(repo))
	load := lms + " load " + b.q(key) + " --context-length " + fmt.Sprint(pl.ContextTokens)
	if !mlx {
		if pl.Backend == "cpu" {
			load += " --gpu off"
		} else {
			load += " --gpu max"
		}
	}
	if pl.KeepAliveS > 0 {
		load += " --ttl " + fmt.Sprint(pl.KeepAliveS)
	}
	load += " --identifier " + b.q(pl.LocalModelName) + " -y"
	why := fmt.Sprintf("Loads the model with a %d-token context (LM Studio's default is 4096, far below coding-agent prompts)", pl.ContextTokens)
	if !mlx {
		why += ", fully offloaded to the GPU"
		if pl.Backend == "cpu" {
			why = strings.Replace(why, ", fully offloaded to the GPU", ", on the CPU", 1)
		}
	}
	why += ", under the API name " + pl.LocalModelName + "."
	if pl.KeepAliveS < 0 {
		why += " Without --ttl an explicitly loaded model stays loaded until unloaded."
	}
	why += " lms matches the model key loosely; if it picks the wrong model (or several quantizations are downloaded), run `lms ls` and pass the exact key."
	b.add(Step{
		ID: "start-model", Kind: KindStart,
		Title:      "Load " + pl.LocalModelName + " with a " + fmt.Sprint(pl.ContextTokens) + "-token context",
		Why:        why,
		Commands:   []Command{b.lmsCmd(load), b.lmsCmd(lms + " ps")},
		Undo:       []Command{b.lmsCmd(lms + " unload " + b.q(pl.LocalModelName))},
		Confidence: Medium,
		Docs:       []string{docLMSCLI, docLMSRepo},
	})
	return nil
}

// lmsBin is how to call lms: plainly when LM Studio was already installed
// (assumed on PATH), else by its full path in ~/.lmstudio/bin.
func (b *builder) lmsBin() string {
	if b.in.Installed {
		return "lms"
	}
	if b.win {
		return `& "$HOME\.lmstudio\bin\lms.exe"`
	}
	return `"$HOME/.lmstudio/bin/lms"`
}

func (b *builder) lmsCmd(run string) Command {
	if b.in.Installed {
		return b.any(run)
	}
	return b.os(run)
}

// repoBase is the repo name without owner and a -GGUF suffix.
func repoBase(repo string) string {
	if i := strings.LastIndex(repo, "/"); i >= 0 {
		repo = repo[i+1:]
	}
	for _, suf := range []string{"-GGUF", "-gguf", "_GGUF"} {
		repo = strings.TrimSuffix(repo, suf)
	}
	return repo
}

func kvBits(kv string) string {
	switch strings.ToLower(kv) {
	case "q4_0", "q4_1":
		return "4"
	case "q5_0", "q5_1":
		return "5"
	}
	return "8"
}
