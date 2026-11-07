package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aeroknight786/trialm/internal/calib"
	"github.com/aeroknight786/trialm/internal/catalog"
	"github.com/aeroknight786/trialm/internal/figure"
	"github.com/aeroknight786/trialm/internal/gguf"
	"github.com/aeroknight786/trialm/internal/harness"
	"github.com/aeroknight786/trialm/internal/setup"
	"github.com/aeroknight786/trialm/internal/verify"
)

func writeModelFixture(t *testing.T, path string, b []byte) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
}
func TestCachedModelSources(t *testing.T) {
	model, _ := catalog.Lookup("qwen3.5:9b")
	q, _ := model.Quant("Q4_K_M")
	for _, source := range []string{"hf", "hf-home", "lmstudio", "old-lmstudio", "ollama", "ollama-hf", "ollama-quant", "legacy", "legacy-quant", "broken"} {
		t.Run(source, func(t *testing.T) {
			home, root := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("HF_HUB_CACHE", filepath.Join(home, "hf"))
			t.Setenv("HF_HOME", "")
			t.Setenv("OLLAMA_MODELS", filepath.Join(home, "ollama"))
			name := "Qwen3.5-9B-Q4_K_M.gguf"
			var data bytes.Buffer
			e := gguf.Write(context.Background(), &data, map[string]any{"general.architecture": "qwen3", "general.file_type": uint32(15), "qwen3.block_count": uint32(1)}, []gguf.WriteTensor{{Name: "blk.0.attn_q.weight", Dimensions: []uint64{256, 1}, Type: gguf.TypeQ4_K, Fill: func(ctx context.Context, w io.Writer, n uint64) error { _, e := w.Write(make([]byte, n)); return e }}})
			if e != nil {
				t.Fatal(e)
			}
			sum := fmt.Sprintf("%x", sha256.Sum256(data.Bytes()))
			want := ""
			switch source {
			case "hf", "hf-home", "broken":
				hub := os.Getenv("HF_HUB_CACHE")
				if source == "hf-home" {
					t.Setenv("HF_HUB_CACHE", "")
					t.Setenv("HF_HOME", filepath.Join(home, "custom"))
					hub = filepath.Join(home, "custom", "hub")
				}
				base := filepath.Join(hub, "models--"+strings.ReplaceAll(model.HFGGUFRepo, "/", "--"))
				want = filepath.Join(base, "blobs", sum)
				writeModelFixture(t, want, data.Bytes())
				snap := filepath.Join(base, "snapshots", "revision", name)
				if e = os.MkdirAll(filepath.Dir(snap), 0700); e != nil {
					t.Fatal(e)
				}
				if e = os.Symlink(want, snap); e != nil {
					t.Skip(e)
				}
				if source == "broken" {
					os.Remove(want)
					want = ""
				}
			case "lmstudio", "old-lmstudio":
				base := filepath.Join(home, ".lmstudio", "models")
				if source == "old-lmstudio" {
					base = filepath.Join(home, ".cache", "lm-studio", "models")
				}
				want = filepath.Join(base, model.HFGGUFRepo, name)
				writeModelFixture(t, want, data.Bytes())
			case "ollama", "ollama-hf", "ollama-quant":
				want = filepath.Join(os.Getenv("OLLAMA_MODELS"), "blobs", "sha256-"+sum)
				writeModelFixture(t, want, data.Bytes())
				manifestPath := filepath.Join(os.Getenv("OLLAMA_MODELS"), "manifests", "registry.ollama.ai", "library", "qwen3.5", "9b")
				if source == "ollama-hf" {
					manifestPath = filepath.Join(os.Getenv("OLLAMA_MODELS"), "manifests", "hf.co", model.HFGGUFRepo, q.Name)
				}
				if source == "ollama-quant" {
					manifestPath += "-q4_K_M"
				}
				writeModelFixture(t, manifestPath, []byte(fmt.Sprintf(`{"layers":[{"mediaType":"application/vnd.ollama.image.model","digest":"sha256:%s"}]}`, sum)))
			case "legacy", "legacy-quant":
				want = filepath.Join(home, "weights with spaces", name)
				writeModelFixture(t, want, data.Bytes())
				scriptName := "qwen3.5-9b-cyrlm.sh"
				if source == "legacy-quant" {
					scriptName = "qwen3.5-9b-q4_k_m-cyrlm.sh"
				}
				writeModelFixture(t, filepath.Join(root, "llamacpp", scriptName), []byte("#!/bin/sh\nexit 99\nllama-server -m \""+want+"\"\n"))
			}
			if got := cachedModel(home, root, model, q); got != want {
				t.Fatalf("got %q want %q", got, want)
			}
		})
	}
}
func TestModelsOrderingAndLocalPlan(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TRIALM_HOME", root)
	t.Setenv("HF_HUB_CACHE", filepath.Join(home, "hf"))
	t.Setenv("OLLAMA_MODELS", filepath.Join(home, "ollama"))
	m, _ := catalog.Lookup("qwen3.5:9b")
	local := filepath.Join(home, ".lmstudio", "models", m.HFGGUFRepo, "Qwen3.5-9B-Q4_K_M.gguf")
	writeModelFixture(t, local, []byte("fixture weights"))
	s := Session{}
	o := Options{Device: "Apple M5", RAM: 24, Runtime: "all", Tuned: true, Context: 65536, Use: "agent", QualityFloor: "3bit-ok"}
	d, e := s.Detect(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	h, _ := harness.Get("codex")
	rows, e := s.Models(context.Background(), o, d.Target, h)
	if e != nil {
		t.Fatal(e)
	}
	if len(rows) != 3 || rows[0].Row.LocalPath != local || rows[0].Journal != nil {
		t.Fatalf("%+v", rows)
	}
	for _, r := range rows {
		if r.Speed.Kind == figure.Measured {
			t.Fatal("predicted speed marked measured")
		}
	}
	p, e := s.PlanRecipe(context.Background(), rows[0].Row.Result, o, "codex", local)
	if e != nil {
		t.Fatal(e)
	}
	for _, step := range p.Steps {
		if step.Kind == setup.Fetch || step.ID == "resolve" {
			t.Fatal("local file became a download", step)
		}
	}
	j := setup.Journal{Plan: p, State: "ready", Report: &verify.Report{Ready: true, DecodeTPS: map[int]float64{0: 21}}, Steps: make([]setup.Record, len(p.Steps))}
	b, _ := json.Marshal(j)
	writeModelFixture(t, filepath.Join(root, "setups", p.Slug, "journal.json"), b)
	rows, e = s.Models(context.Background(), o, d.Target, h)
	if e != nil || len(rows) != 3 || rows[0].Journal == nil || rows[0].Speed.Kind != figure.Measured || rows[0].Speed.String() != "21 tok/s" {
		t.Fatal(rows, e)
	}
	// Current journals store measured depth instead of a synthetic zero anchor.
	j.Report.DecodeTPS = map[int]float64{17: 22, 59000: 9}
	j.Report.Samples = []calib.RealSample{{Mode: "decode", Depth: 17, Prompt: 17, Generated: 32, Speed: 22}, {Mode: "decode", Depth: 59000, Prompt: 59000, Generated: 32, Speed: 9}}
	b, _ = json.Marshal(j)
	writeModelFixture(t, filepath.Join(root, "setups", p.Slug, "journal.json"), b)
	rows, e = s.Models(context.Background(), o, d.Target, h)
	if e != nil || rows[0].Speed.String() != "22 tok/s" {
		t.Fatal(rows, e)
	}
	for _, r := range rows[1:] {
		if r.Row.Result.Model.ID == m.ID {
			t.Fatal("ready model duplicated")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = s.Models(ctx, o, d.Target, h); e != context.Canceled {
		t.Fatal(e)
	}
}

func TestCompleteCachedShardsAndMLX(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HF_HUB_CACHE", filepath.Join(home, "hf"))
	t.Setenv("OLLAMA_MODELS", filepath.Join(home, "ollama"))
	model, _ := catalog.Lookup("qwen3.5:9b")
	q, _ := model.Quant("Q4_K_M")
	base := filepath.Join(home, ".lmstudio", "models", model.HFGGUFRepo)
	first := filepath.Join(base, "Qwen3.5-9B-Q4_K_M-00001-of-00002.gguf")
	writeModelFixture(t, first, []byte("part one"))
	if got := cachedModel(home, "", model, q); got != "" {
		t.Fatal("partial model on disk", got)
	}
	writeModelFixture(t, filepath.Join(base, "Qwen3.5-9B-Q4_K_M-00002-of-00002.gguf"), []byte("part two"))
	if got := cachedModel(home, "", model, q); got != first {
		t.Fatal(got)
	}
	model, _ = catalog.Lookup("qwen3-coder:30b")
	if model.HFMLXRepo == "" {
		t.Fatal("missing fixture model")
	}
	q, _ = model.Quant("MLX-4bit")
	base = filepath.Join(home, ".lmstudio", "models", model.HFMLXRepo)
	writeModelFixture(t, filepath.Join(base, "config.json"), []byte(`{"quantization":{"bits":4}}`))
	if got := cachedModel(home, "", model, q); got != "" {
		t.Fatal("no weights", got)
	}
	writeModelFixture(t, filepath.Join(base, "model.safetensors"), []byte("fixture"))
	if got := cachedModel(home, "", model, q); got != base {
		t.Fatal(got)
	}
	s := Session{}
	o := Options{Device: "Apple M5", RAM: 64, Context: 65536, Tuned: true}
	d, e := s.Detect(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	h, _ := harness.Get("codex")
	row, ok, e := BestFileFor(Options{Runtime: "mlx-lm", Quant: q.Name, Context: 65536, Tuned: true}, model, d.Target, h.Scenario, Installed{}, h)
	if !ok || e != nil {
		t.Fatal(e)
	}
	p, e := s.PlanRecipe(context.Background(), row.Result, o, "codex", base)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, step := range p.Steps {
		if step.Kind == setup.Fetch || step.ID == "resolve" {
			t.Fatal("cached MLX became download")
		}
		if step.Kind == setup.Start && strings.Contains(strings.Join(step.Argv, " "), base) {
			found = true
		}
	}
	if !found {
		t.Fatal("MLX launcher lost local path", p)
	}
}
