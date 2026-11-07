package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/aeroknight786/trialm/internal/catalog"
	"github.com/aeroknight786/trialm/internal/engine"
	"github.com/aeroknight786/trialm/internal/figure"
	"github.com/aeroknight786/trialm/internal/harness"
	"github.com/aeroknight786/trialm/internal/paths"
	"github.com/aeroknight786/trialm/internal/predict"
	"github.com/aeroknight786/trialm/internal/setup"
	"github.com/aeroknight786/trialm/internal/setup/download"
)

// ModelChoice keeps launcher ordering and provenance out of terminal clients.
type ModelChoice struct {
	Row      Row
	Journal  *setup.Journal
	Speed    figure.Figure
	Reply    figure.Figure
	Download figure.Figure
}

func (s Session) Models(ctx context.Context, o Options, t predict.Target, h harness.Harness) ([]ModelChoice, error) {
	h.Scenario = harness.OpeningScenario(h.Scenario, t.OS)
	ready, err := s.ReadySetups(ctx)
	if err != nil {
		return nil, err
	}
	listing, err := s.Find(ctx, o, t, h.Scenario, h, "", 0)
	if err != nil {
		return nil, err
	}
	choices := []ModelChoice{}
	seen := map[string]bool{}
	for _, j := range ready {
		model, ok := catalog.Lookup(j.Plan.Model)
		if !ok {
			model = catalog.Model{ID: j.Plan.Model, Name: j.Plan.ModelName}
		}
		row := Row{}
		row.Result.Model = model
		for _, r := range listing.Rows {
			if r.Result.Model.ID == model.ID {
				row = r
				break
			}
		}
		// Trials of a saved setup retain its actual quant and engine.
		row.Result.Quant, _ = model.Quant(j.Plan.Quant)
		row.Result.Runtime, _ = engine.Get(j.Plan.Runtime)
		if t.OS == "darwin" {
			row.Result = predict.Predict(predict.Input{Model: model, QuantName: j.Plan.Quant, Runtime: row.Result.Runtime, Target: t, Tuned: j.Plan.Tuned, CtxOverride: j.Plan.ContextTokens, KVCacheType: j.Plan.KVCacheType, Scenario: h.Scenario, BackendOverride: j.Plan.Backend})
		}
		// Preserve the short-context writing metric on non-Mac launchers.
		// Mac launchers use the calibrated scenario above.
		speed, depth := j.Report.DecodeTPS[0], -1
		for _, sample := range j.Report.Samples {
			if sample.Mode == "decode" && (depth < 0 || sample.Depth < depth) {
				speed, depth = sample.Speed, sample.Depth
			}
		}
		choices = append(choices, ModelChoice{Row: row, Journal: &j, Reply: replyFigure(row.Result), Speed: figure.Band(speed, 20, "tok/s", figure.Measured, "setup:"+j.Plan.Slug+"/ready")})
		seen[model.ID] = true
	}
	home, _ := os.UserHomeDir()
	root, _ := paths.Home()
	for _, model := range catalog.All() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if seen[model.ID] {
			continue
		}
		for _, q := range model.Quants {
			local := cachedModel(home, root, model, q)
			if local == "" {
				continue
			}
			opts := o
			opts.Runtime, opts.Quant = "llamacpp", q.Name
			if q.Format == "mlx" {
				opts.Runtime = "mlx-lm"
			}
			row, ok, e := BestFileFor(opts, model, t, h.Scenario, listing.Installed, h)
			if e != nil || !ok {
				continue
			}
			row.LocalPath = local
			choices = append(choices, modelChoice(row))
			seen[model.ID] = true
			break
		}
	}
	added := 0
	for _, row := range listing.Rows {
		if seen[row.Result.Model.ID] {
			continue
		}
		choices = append(choices, modelChoice(row))
		seen[row.Result.Model.ID] = true
		added++
		if added == 2 {
			break
		}
	}
	return choices, nil
}
func modelChoice(r Row) ModelChoice {
	p := r.Result
	speed := figure.Band(p.DecodeTPS0, p.Accuracy.DecodePct, "tok/s", figure.Kind(p.Kind), "prediction:"+p.Model.ID)
	return ModelChoice{Row: r, Reply: replyFigure(p), Speed: speed, Download: figure.Bytes(p.Quant.FileBytes, "catalog:"+p.Model.ID)}
}

// Discovery only reads filenames, manifests and launcher text. It never sources
// old launchers or hashes multi-gigabyte weights on the UI's critical path.
func cachedModel(home, root string, m catalog.Model, q catalog.Quant) string {
	hub := os.Getenv("HF_HUB_CACHE")
	if hub == "" {
		base := os.Getenv("HF_HOME")
		if base == "" {
			base = filepath.Join(home, ".cache", "huggingface")
		}
		hub = filepath.Join(base, "hub")
	}
	repo := m.HFGGUFRepo
	if q.Format == "mlx" {
		repo = m.HFMLXRepo
	}
	if repo == "" {
		return ""
	}
	roots := []string{filepath.Join(hub, "models--"+strings.ReplaceAll(repo, "/", "--"), "snapshots"), filepath.Join(home, ".lmstudio", "models", repo), filepath.Join(home, ".cache", "lm-studio", "models", repo)}
	found := ""
	for _, base := range roots {
		_ = filepath.WalkDir(base, func(p string, d fs.DirEntry, e error) error {
			if e != nil || d.IsDir() {
				return nil
			}
			if q.Format == "mlx" {
				if d.Name() != "config.json" {
					return nil
				}
				b, _ := os.ReadFile(p)
				var cfg struct {
					Quantization       struct{ Bits int }
					QuantizationConfig struct{ Bits int } `json:"quantization_config"`
				}
				_ = json.Unmarshal(b, &cfg)
				bits := max(cfg.Quantization.Bits, cfg.QuantizationConfig.Bits)
				files, _ := filepath.Glob(filepath.Join(filepath.Dir(p), "*.safetensors"))
				if len(files) == 0 || q.Name != fmt.Sprintf("MLX-%dbit", bits) {
					return nil
				}
				var index struct {
					Weights map[string]string `json:"weight_map"`
				}
				b, _ = os.ReadFile(filepath.Join(filepath.Dir(p), "model.safetensors.index.json"))
				if len(files) > 1 && (json.Unmarshal(b, &index) != nil || len(index.Weights) == 0) {
					return nil
				}
				for _, name := range index.Weights {
					if st, e := os.Stat(filepath.Join(filepath.Dir(p), name)); e != nil || !st.Mode().IsRegular() {
						return nil
					}
				}
				for _, file := range files {
					if st, e := os.Stat(file); e != nil || !st.Mode().IsRegular() || st.Size() == 0 {
						return nil
					}
				}
				found = filepath.Dir(p)
				return fs.SkipAll
			}
			if !strings.HasSuffix(strings.ToLower(p), ".gguf") || !strings.EqualFold(catalog.FileTag(p), q.HFTag()) {
				return nil
			}
			st, e := os.Stat(p)
			if e != nil || !st.Mode().IsRegular() || st.Size() == 0 {
				return nil
			}
			if split := modelShard.FindStringSubmatch(p); len(split) > 0 {
				count, _ := strconv.Atoi(split[2])
				if count < 1 {
					return nil
				}
				for i := 1; i <= count; i++ {
					part := modelShard.ReplaceAllString(p, fmt.Sprintf("-%05d-of-%05d", i, count))
					if st, e := os.Stat(part); e != nil || !st.Mode().IsRegular() || st.Size() == 0 {
						return nil
					}
				}
				found = modelShard.ReplaceAllString(p, fmt.Sprintf("-%05d-of-%05d", 1, count))
				return fs.SkipAll
			}
			resolved, _ := filepath.EvalSymlinks(p)
			digest := filepath.Base(resolved)
			for _, c := range download.Candidates(home, m.HFGGUFRepo, filepath.Base(p), digest) {
				found = c
				return fs.SkipAll
			}
			found = p
			return fs.SkipAll
		})
		if found != "" {
			return found
		}
	}
	if q.Format != "gguf" {
		return ""
	}
	ollama := os.Getenv("OLLAMA_MODELS")
	if ollama == "" {
		ollama = filepath.Join(home, ".ollama", "models")
	}
	_ = filepath.WalkDir(filepath.Join(ollama, "manifests"), func(p string, d fs.DirEntry, e error) error {
		if e != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(filepath.Join(ollama, "manifests"), p)
		tag := filepath.ToSlash(rel)
		parts := strings.Split(tag, "/")
		if len(parts) < 2 {
			return nil
		}
		modelTag := parts[len(parts)-2] + ":" + parts[len(parts)-1]
		named, ok := catalog.Lookup(modelTag)
		if (!ok || named.ID != m.ID) && !strings.HasPrefix(modelTag, m.OllamaTag+"-") && !strings.Contains(strings.ToLower(tag), strings.ToLower(m.HFGGUFRepo+"/")) {
			return nil
		}
		b, _ := os.ReadFile(p)
		var manifest struct {
			Layers []struct{ MediaType, Digest string }
		}
		if json.Unmarshal(b, &manifest) != nil {
			return nil
		}
		for _, layer := range manifest.Layers {
			if layer.MediaType != "application/vnd.ollama.image.model" {
				continue
			}
			digest := strings.TrimPrefix(layer.Digest, "sha256:")
			for _, c := range download.Candidates(home, m.HFGGUFRepo, "model.gguf", digest) {
				f, e := ModelFromGGUF(c)
				if e == nil && len(f.Quants) > 0 && strings.EqualFold(f.Quants[0].Name, q.Name) {
					found = c
					return fs.SkipAll
				}
			}
		}
		return nil
	})
	if found != "" {
		return found
	}
	for _, base := range []string{root, filepath.Join(home, ".trialm")} {
		scripts, _ := filepath.Glob(filepath.Join(base, "llamacpp", "*-cyrlm.sh"))
		for _, script := range scripts {
			name := strings.TrimSuffix(filepath.Base(script), "-cyrlm.sh")
			if name != strings.ReplaceAll(m.OllamaTag, ":", "-") && name != m.ID && !strings.HasPrefix(name, strings.ReplaceAll(m.OllamaTag, ":", "-")+"-") {
				continue
			}
			b, _ := os.ReadFile(script)
			match := legacyModel.FindSubmatch(b)
			if len(match) < 2 {
				continue
			}
			local := strings.Trim(string(match[1]), "\"'")
			local = strings.ReplaceAll(local, "$HOME", home)
			local = strings.ReplaceAll(local, "${HOME}", home)
			if strings.HasPrefix(local, "~/") {
				local = filepath.Join(home, local[2:])
			}
			if !strings.EqualFold(catalog.FileTag(local), q.HFTag()) {
				continue
			}
			if st, e := os.Stat(local); e == nil && st.Mode().IsRegular() && st.Size() > 0 {
				return local
			}
		}
	}
	return ""
}

var legacyModel = regexp.MustCompile(`(?:-m|--model)\s+("[^"\n]+"|'[^'\n]+'|[^\s\\]+)`)

var modelShard = regexp.MustCompile(`-(\d{5})-of-(\d{5})`)

func replyFigure(p predict.Result) figure.Figure {
	return PredictionMetric(p, p.Scenario.FollowUpTurn.Seconds(), "s", false)
}

// PredictionMetric carries the same band and source on every interactive screen.
func PredictionMetric(r predict.Result, v float64, unit string, reading bool) figure.Figure {
	band := r.Accuracy.DecodePct
	if reading {
		band = r.Accuracy.PrefillPct
	}
	return figure.Band(v, band, unit, figure.Kind(r.Kind), "prediction:"+r.Model.ID+"/"+r.Runtime.ID)
}

// OpeningPromptFigure also supplies the header while a new tool is still ranking.
func OpeningPromptFigure(sc predict.Scenario, tool string) figure.Figure {
	if h, err := harness.Get(tool); err == nil && (sc.BasePromptTokens == 0 || sc.Harness != h.ID) {
		sc = h.Scenario
	}
	return figure.Band(float64(sc.BasePromptTokens)/1000, 20, "", figure.Measured, "harness:opening/"+sc.Harness)
}
