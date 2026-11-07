package setup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aeroknight786/trialm/internal/calib"
	"github.com/aeroknight786/trialm/internal/setup/download"
	"github.com/aeroknight786/trialm/internal/setup/proc"
	"github.com/aeroknight786/trialm/internal/verify"
)

// releaseFetcher stands in for the parallel downloader agent. Only the pinned
// ggml-org engine asset is allowed here. Switch with TRIALM_E2E_REAL_DOWNLOAD=1.
type releaseFetcher struct{ archive string }

func (f releaseFetcher) Fetch(ctx context.Context, r download.Request) (download.Result, error) {
	if !strings.HasPrefix(r.URL, "https://github.com/ggml-org/llama.cpp/releases/download/") {
		return download.Result{}, fmt.Errorf("e2e refuses non-engine download %s", r.URL)
	}
	var body io.ReadCloser
	if f.archive != "" {
		file, err := os.Open(f.archive)
		if err != nil {
			return download.Result{}, err
		}
		body = file
	} else {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, r.URL, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return download.Result{}, err
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			return download.Result{}, fmt.Errorf("engine asset: %s", resp.Status)
		}
		body = resp.Body
	}
	defer body.Close()
	if err := os.MkdirAll(filepath.Dir(r.Dest), 0700); err != nil {
		return download.Result{}, err
	}
	file, err := os.Create(r.Dest + ".part")
	if err != nil {
		return download.Result{}, err
	}
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(file, hash), body)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return download.Result{}, fmt.Errorf("copy asset: %v %v", err, closeErr)
	}
	if fmt.Sprintf("%x", hash.Sum(nil)) != r.SHA256 {
		return download.Result{}, download.ErrDigest
	}
	if err = os.Rename(r.Dest+".part", r.Dest); err != nil {
		return download.Result{}, err
	}
	return download.Result{Path: r.Dest, Bytes: size}, nil
}
func engineReadyFake(ctx context.Context, o verify.ReadyOptions) (verify.Report, error) {
	if !proc.Health(ctx, o.Endpoint+"/health") {
		return verify.Report{Failing: "warmup"}, nil
	}
	payload, _ := json.Marshal(map[string]any{"model": o.Model, "messages": []map[string]string{{"role": "user", "content": "Say OK"}}, "max_tokens": 1, "temperature": 0})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, o.Endpoint+"/v1/chat/completions", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return verify.Report{}, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return verify.Report{}, err
	}
	if resp.StatusCode != 200 {
		return verify.Report{}, fmt.Errorf("synthetic inference %s: %s", resp.Status, b)
	}
	var result struct {
		Choices []json.RawMessage `json:"choices"`
		Timings struct {
			TPS float64 `json:"predicted_per_second"`
		} `json:"timings"`
	}
	if err = json.Unmarshal(b, &result); err != nil || len(result.Choices) == 0 {
		return verify.Report{}, fmt.Errorf("missing synthetic completion: %s", b)
	}
	return verify.Report{Ready: true, Checks: []verify.Check{{Name: "warmup", OK: true, Detail: "CPU engine answers health"}, {Name: "decode", OK: true, Detail: "synthetic completion returned"}}, DecodeTPS: map[int]float64{0: result.Timings.TPS}}, nil
}
func TestEngineE2E(t *testing.T) {
	if os.Getenv("TRIALM_ENGINE_E2E") != "1" {
		t.Skip("set TRIALM_ENGINE_E2E=1 for pinned CPU engine setup → verify → undo")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	e := testExecutor(t)
	if os.Getenv("TRIALM_E2E_REAL_DOWNLOAD") != "1" {
		e.Downloader = releaseFetcher{archive: os.Getenv("TRIALM_ENGINE_ARCHIVE")}
	}
	if os.Getenv("TRIALM_E2E_REAL_READY") == "1" {
		e.Ready = verify.Ready
	} else {
		e.Ready = engineReadyFake
	}
	synthetic, err := calib.Generate(ctx, filepath.Join(e.Home, "synthetic"), calib.Probe{Shape: calib.Shapes[0], Quant: "Q4_K_M", Layers: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(filepath.Dir(synthetic))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	in := fixture(t)
	in.OS, in.Arch, in.Backend = runtime.GOOS, runtime.GOARCH, "cpu"
	in.LocalModel = synthetic
	in.Port = port
	in.Settings.ContextTokens = 512
	p, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = e.Undo(context.Background(), p.Slug, UndoOptions{Models: true, Engines: true}) })
	j, err := e.Apply(ctx, p, Policy{PlanID: p.ID})
	if err != nil {
		_ = filepath.WalkDir(filepath.Join(e.Root, "logs"), func(path string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				b, _ := os.ReadFile(path)
				t.Log(string(b))
			}
			return nil
		})
		t.Fatal(err)
	}
	if j.Prompts.Approvals != 1 || j.Prompts.Passwords != 0 || j.State != "ready" || j.Report == nil || !j.Report.Ready {
		t.Fatalf("not ready: %+v", j)
	}
	if _, err = e.Undo(ctx, p.Slug, UndoOptions{Models: true, Engines: true}); err != nil {
		t.Fatal(err)
	}
	var remaining []string
	_ = filepath.WalkDir(e.Root, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(e.Root, path)
			remaining = append(remaining, rel)
		}
		return err
	})
	if len(remaining) != 1 || remaining[0] != filepath.Join("setups", p.Slug, "journal.json") {
		t.Fatal("undo left files", remaining)
	}
	t.Logf("%s/%s pinned CPU release, one approval, zero passwords, Ready, undo retained only journal", runtime.GOOS, runtime.GOARCH)
}
