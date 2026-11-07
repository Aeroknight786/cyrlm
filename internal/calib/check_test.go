package calib

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aeroknight786/trialm/internal/gguf"
)

// A header and sparse tensor extent exercise matching without real weights.
func checkGGUF(t *testing.T, filename, arch string, layers uint32, split uint32) string {
	t.Helper()
	var b bytes.Buffer
	put := func(v any) {
		t.Helper()
		if e := binary.Write(&b, binary.LittleEndian, v); e != nil {
			t.Fatal(e)
		}
	}
	str := func(s string) { put(uint64(len(s))); b.WriteString(s) }
	b.WriteString("GGUF")
	put(uint32(3))
	put(uint64(1))
	put(uint64(8))
	str("general.architecture")
	put(uint32(gguf.ValueString))
	str(arch)
	for _, kv := range []struct {
		k string
		v uint32
	}{{"general.file_type", 15}, {arch + ".block_count", layers}, {arch + ".attention.head_count", 16}, {arch + ".attention.head_count_kv", 4}, {arch + ".attention.key_length", 256}, {arch + ".embedding_length", 4096}, {"split.count", split}} {
		str(kv.k)
		put(uint32(gguf.ValueUint32))
		put(kv.v)
	}
	str("blk.0.attn_q.weight")
	put(uint32(2))
	put(uint64(256))
	put(uint64(8953803264 / 256))
	put(uint32(gguf.TypeQ4_K))
	put(uint64(0))
	for b.Len()%32 != 0 {
		b.WriteByte(0)
	}
	path := filepath.Join(t.TempDir(), filename)
	if e := os.WriteFile(path, b.Bytes(), 0600); e != nil {
		t.Fatal(e)
	}
	f, e := os.OpenFile(path, os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.Truncate(int64(b.Len()) + 8953803264/256*144); e != nil {
		t.Fatal(e)
	}
	if e = f.Close(); e != nil {
		t.Fatal(e)
	}
	return path
}

func TestMatchFileExactQuantSizes(t *testing.T) {
	path := checkGGUF(t, "Qwen3.5-9B-Q4_K_M.gguf", "qwen35", 32, 1)
	m, q, e := MatchFile(path)
	if e != nil {
		t.Fatal(e)
	}
	st, _ := os.Stat(path)
	if m.ID != "qwen3.5-9b" || q.Name != "Q4_K_M" || q.FileBytes != st.Size() || q.ReadBytesPerToken != 8953803264/256*144 || len(m.Quants) != 1 {
		t.Fatal(m.ID, q)
	}
	for _, tc := range []struct {
		name, arch    string
		layers, split uint32
	}{{"wrong-Q8_0.gguf", "qwen35", 32, 1}, {"wrong-Q4_K_M.gguf", "qwen35", 31, 1}, {"wrong-Q4_K_M.gguf", "unknown", 32, 1}, {"part-Q4_K_M.gguf", "qwen35", 32, 2}} {
		if _, _, e = MatchFile(checkGGUF(t, tc.name, tc.arch, tc.layers, tc.split)); e == nil {
			t.Fatal("mismatched file accepted", tc)
		}
	}
	for _, path := range []string{"https://example.com/model.gguf", t.TempDir(), filepath.Join(t.TempDir(), "absent.gguf")} {
		if _, _, e = MatchFile(path); e == nil {
			t.Fatal("non-local/non-file accepted", path)
		}
	}
}

func TestCheckRealFileWithFakeEngine(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("shell adapter")
	}
	for _, test := range []string{"success", "mlock", "incomplete", "invalid", "short-reps", "changed", "cpu", "duplicate", "bad-json", "stale", "cancel", "prediction-error"} {
		t.Run(test, func(t *testing.T) {
			t.Setenv("TRIALM_SUCCESSFUL_BENCH_HELPER", "1")
			t.Setenv("TRIALM_CHECK_TEST_MODE", test)
			t.Setenv("GORACE", "atexit_sleep_ms=0")
			if test == "mlock" {
				t.Setenv("TRIALM_TEST_MLOCK_WARNING", "1")
			}
			home := t.TempDir()
			c := Calibration{Version: GeneratorVersion, FittingVersion: FittingVersion, Fingerprint: Fingerprint{Hash: "fake"}, Fits: []Fit{{Class: "test", Quant: "untouched", Fixed: 1}}}
			if e := c.Save(home); e != nil {
				t.Fatal(e)
			}
			before, _ := os.ReadFile(filepath.Join(home, "calib.json"))
			path := filepath.Join(home, "real-32.gguf")
			if e := os.WriteFile(path, []byte("local test file"), 0600); e != nil {
				t.Fatal(e)
			}
			exe, _ := os.Executable()
			engine := filepath.Join(home, "bench")
			script := "#!/bin/sh\nexec '" + strings.ReplaceAll(exe, "'", "'\\''") + "' -test.run=TestSuccessfulBenchHelper -- \"$@\"\n"
			if e := os.WriteFile(engine, []byte(script), 0700); e != nil {
				t.Fatal(e)
			}
			o := CheckOptions{Home: home, Engine: engine, Path: path, Model: "matched", Quant: "Q4_K_M", Fingerprint: Fingerprint{Hash: "fake"}, Predict: func(b BenchRow) (CheckPrediction, error) {
				if test == "prediction-error" {
					return CheckPrediction{}, errors.New("prediction failed")
				}
				// Short rows fall inside; deep rows deliberately miss. Both are evidence.
				tps := 1 / (.01 + .002*32 + float64(b.Depth)*32*1e-8)
				if b.Depth > 0 {
					tps *= 2
				}
				return CheckPrediction{TPS: tps, BandPct: 20, Basis: "test evidence"}, nil
			}}
			if test == "stale" {
				o.Fingerprint.Hash = "different"
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if test == "cancel" {
				cancel()
			}
			rows, e := Check(ctx, o)
			if test != "success" && test != "mlock" {
				if e == nil {
					t.Fatal("invalid check accepted", rows)
				}
				after, _ := os.ReadFile(filepath.Join(home, "calib.json"))
				if !bytes.Equal(before, after) {
					t.Fatal("failed check mutated calibration")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			if len(rows) != 4 {
				t.Fatal(rows)
			}
			mode := "mlock"
			if test == "mlock" {
				mode = "mmap"
			}
			for _, r := range rows {
				if r.LoadMode != mode || r.Args[len(r.Args)-1] != mode || r.Basis != "test evidence" || r.SpreadTPS <= 0 || len(r.Raw) == 0 || r.File != path {
					t.Fatal(r)
				}
				if r.Depth == 0 && (!r.InsideBand || r.ErrorPct > 1e-8) {
					t.Fatal(r)
				}
				if r.Depth > 0 && (r.InsideBand || r.ErrorPct < 99) {
					t.Fatal("miss was hidden", r)
				}
			}
			saved, e := Load(home)
			if e != nil || len(saved.Validations) != 4 || len(saved.Rows) != 0 || len(saved.Fits) != 1 || saved.Fits[0].Class != "test" {
				t.Fatal(saved, e)
			}
			if _, e = Check(ctx, o); e != nil {
				t.Fatal(e)
			}
			saved, e = Load(home)
			if e != nil || len(saved.Validations) != 8 {
				t.Fatal("repeat check did not append", saved, e)
			}
			saved.FitRows() // refitting must never train on validation observations
			if len(saved.Fits) != 0 || len(saved.Validations) != 8 {
				t.Fatal("validation entered synthetic fit")
			}
		})
	}
}
