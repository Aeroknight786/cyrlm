package setup

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aeroknight786/trialm/internal/catalog"
	"github.com/aeroknight786/trialm/internal/engine"
	"github.com/aeroknight786/trialm/internal/harness"
	"github.com/aeroknight786/trialm/internal/setup/download"
	"github.com/aeroknight786/trialm/internal/verify"
)

func fixture(t *testing.T) Input {
	t.Helper()
	model, _ := catalog.Lookup("qwen3:8b")
	profile, _ := engine.Get("llamacpp")
	q, _ := model.Quant("Q4_K_M")
	settings := profile.Resolve(engine.ResolveInput{NativeCtx: model.NativeCtx, CtxOverride: 4096, Tuned: true})
	return Input{Model: model, Profile: profile, Quant: q, Settings: settings, OS: "linux", Arch: "amd64", Backend: "cpu", LocalModel: "/existing/model.gguf", Predicted: verify.Predicted{DecodeTPS0: 12, DecodeTPSTarget: 10, PrefillTPS: 100}}
}
func TestTypedPlansAndPrompts(t *testing.T) {
	for _, platform := range []struct{ os, arch, backend string }{{"darwin", "arm64", "metal"}, {"linux", "amd64", "cpu"}, {"linux", "amd64", "vulkan"}, {"linux", "amd64", "cuda"}, {"windows", "amd64", "cpu"}, {"windows", "amd64", "cuda"}} {
		t.Run(platform.os+"-"+platform.backend, func(t *testing.T) {
			in := fixture(t)
			in.OS, in.Arch, in.Backend = platform.os, platform.arch, platform.backend
			p, err := Build(in)
			if err != nil {
				t.Fatal(err)
			}
			if p.Prompts.Approvals != 1 || p.Prompts.Passwords != 0 {
				t.Fatal(p.Prompts)
			}
			if len(p.ID) != 64 {
				t.Fatal(p.ID)
			}
			if p.Steps[0].Kind != Install || len(p.Steps[0].Request.SHA256) != 64 {
				t.Fatal(p.Steps[0])
			}
			last := p.Steps[len(p.Steps)-1]
			if last.Kind != Check || last.ID != "ready" {
				t.Fatal(last)
			}
			p2, _ := Build(in)
			if p2.ID != p.ID {
				t.Fatal("unstable hash")
			}
			in.Settings.ContextTokens++
			p2, _ = Build(in)
			if p2.ID == p.ID {
				t.Fatal("changed settings retained approval")
			}
		})
	}
	in := fixture(t)
	in.OS, in.Arch, in.Backend = "darwin", "arm64", "metal"
	in.RAMBytes, in.DefaultGPUBytes, in.RequiredGPUBytes = 24<<30, 16<<30, 20<<30
	p, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	if p.Prompts.Passwords != 1 || p.Prompts.AfterRestart != "1 more after each restart" {
		t.Fatal(p.Prompts)
	}
	if !reflect.DeepEqual(p.Steps[0].Argv, []string{"sudo", "sysctl", "iogpu.wired_limit_mb=20480"}) {
		t.Fatal(p.Steps[0])
	}
	in.RequiredGPUBytes = 21 << 30
	if _, err = Build(in); err == nil {
		t.Fatal("macOS reserve lost")
	}
	in.RAMBytes, in.RequiredGPUBytes = 64<<30, 57<<30
	if _, err = Build(in); err == nil {
		t.Fatal("64 GB reserve lost")
	}
}
func TestAdaptersAndHarnessFiles(t *testing.T) {
	for _, id := range []string{"ollama", "llamacpp", "lmstudio", "mlx-lm"} {
		in := fixture(t)
		in.Profile, _ = engine.Get(id)
		in.Installed = true
		in.InstalledVersion = "version: 0.9.0-dev (build 12182, commit fd27b2034)"
		if id == "mlx-lm" {
			in.OS, in.Arch, in.Backend = "darwin", "arm64", "mlx"
			in.Quant.Format = "mlx"
		}
		for _, h := range harness.All() {
			in.Harness = &h
			p, err := Build(in)
			if err != nil {
				t.Fatal(id, h.ID, err)
			}
			executor := testExecutor(t)
			if err = executor.validate(p); err != nil {
				t.Fatal(id, h.ID, err)
			}
			for _, step := range p.Steps {
				if step.Kind == Write && !strings.HasPrefix(step.Path, "~/.trialm/") {
					t.Fatal("harness default edited", step)
				}
			}
		}
	}
}
func testExecutor(t *testing.T) *Executor {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("TRIALM_HOME", filepath.Join(home, ".trialm"))
	return &Executor{Root: filepath.Join(home, ".trialm"), Home: home, Ready: func(context.Context, verify.ReadyOptions) (verify.Report, error) {
		return verify.Report{Ready: true, Checks: []verify.Check{{Name: "warmup", OK: true}}}, nil
	}}
}
func basicPlan(steps ...Step) Plan {
	p := Plan{Slug: "test", Endpoint: "http://127.0.0.1:8080", LocalModelName: "test", ContextTokens: 4096, Steps: append(steps, Step{ID: "ready", Kind: Check, Title: "Verify Ready"})}
	p.Seal()
	return p
}

type resumeDownloader struct {
	data     []byte
	cancel   context.CancelFunc
	progress func(download.Progress)
	resumed  bool
	requests []download.Request
}

func (d *resumeDownloader) SetProgress(f func(download.Progress)) { d.progress = f }
func (d *resumeDownloader) Fetch(ctx context.Context, r download.Request) (download.Result, error) {
	d.requests = append(d.requests, r)
	if err := os.MkdirAll(filepath.Dir(r.Dest), 0700); err != nil {
		return download.Result{}, err
	}
	part := r.Dest + ".part"
	old, _ := os.ReadFile(part)
	if len(old) > 0 {
		d.resumed = true
	}
	if d.cancel != nil {
		if err := os.WriteFile(part, d.data[:61], 0600); err != nil {
			return download.Result{}, err
		}
		d.progress(download.Progress{Done: 61, Total: 100})
		d.cancel()
		d.cancel = nil
		return download.Result{}, ctx.Err()
	}
	if err := os.WriteFile(part, d.data, 0600); err != nil {
		return download.Result{}, err
	}
	if fmt.Sprintf("%x", sha256.Sum256(d.data)) != r.SHA256 {
		return download.Result{}, download.ErrDigest
	}
	if err := os.Rename(part, r.Dest); err != nil {
		return download.Result{}, err
	}
	d.progress(download.Progress{Done: 100, Total: 100})
	return download.Result{Path: r.Dest, Bytes: int64(len(d.data) - len(old)), Resumed: d.resumed}, nil
}
func TestPauseResumeAndUndoRestoresBytes(t *testing.T) {
	e := testExecutor(t)
	config := filepath.Join(e.Home, ".claude", "settings.json")
	original := []byte("{\r\n  \"secret\": \"unchanged spacing\"\r\n}\n")
	if err := os.MkdirAll(filepath.Dir(config), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, original, 0640); err != nil {
		t.Fatal(err)
	}
	originalInfo, err := os.Stat(config)
	if err != nil {
		t.Fatal(err)
	}
	originalMode := originalInfo.Mode().Perm()
	data := []byte(strings.Repeat("x", 100))
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	p := basicPlan(Step{ID: "harness", Kind: Write, Title: "Write harness config", Path: config, Content: "replacement"}, Step{ID: "model", Kind: Fetch, Title: "Fetch model", Request: &download.Request{URL: "https://huggingface.co/org/repo/resolve/abc/model.gguf", SHA256: digest, Size: 100, Dest: "~/.trialm/models/test.gguf"}})
	ctx, cancel := context.WithCancel(context.Background())
	d := &resumeDownloader{data: data, cancel: cancel}
	e.Downloader = d
	j, err := e.Apply(ctx, p, Policy{PlanID: p.ID})
	if !errors.Is(err, context.Canceled) || j.State != "paused" {
		t.Fatal(j.State, err)
	}
	if j.Steps[0].State != "done" || j.Steps[1].Progress.Done != 61 {
		t.Fatal(j.Steps)
	}
	t.Log("1 of 3 done, download 61%")
	// Simulate another process loading only the durable journal.
	saved, err := e.Status(p.Slug)
	if err != nil {
		t.Fatal(err)
	}
	j, err = e.Apply(context.Background(), saved.Plan, Policy{PlanID: saved.Plan.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !d.resumed || j.State != "ready" || j.Prompts.Approvals != 1 || j.Report == nil || !j.Report.Ready {
		t.Fatal(j)
	}
	if _, err = e.Undo(context.Background(), p.Slug, UndoOptions{Models: true, Engines: true}); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(config)
	if err != nil || !reflect.DeepEqual(restored, original) {
		t.Fatal("config restoration differs", err, string(restored))
	}
	info, _ := os.Stat(config)
	if info.Mode().Perm() != originalMode {
		t.Fatal("config mode lost")
	}
	var files []string
	_ = filepath.WalkDir(e.Root, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(e.Root, path)
			files = append(files, rel)
		}
		return err
	})
	if !reflect.DeepEqual(files, []string{filepath.Join("setups", "test", "journal.json")}) {
		t.Fatal(files)
	}
}
func TestUndoRetainsModelsUntilOptIn(t *testing.T) {
	e := testExecutor(t)
	d := &resumeDownloader{data: []byte(strings.Repeat("x", 100))}
	e.Downloader = d
	p := basicPlan(Step{ID: "model", Kind: Fetch, Request: &download.Request{URL: "https://huggingface.co/o/r/resolve/rev/f", SHA256: fmt.Sprintf("%x", sha256.Sum256(d.data)), Dest: "~/.trialm/models/f", Size: 100}})
	if _, err := e.Apply(context.Background(), p, Policy{PlanID: p.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Undo(context.Background(), p.Slug, UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(e.expand(p.Steps[0].Request.Dest)); err != nil {
		t.Fatal("model removed without opt-in", err)
	}
	if _, err := e.Undo(context.Background(), p.Slug, UndoOptions{Models: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(e.expand(p.Steps[0].Request.Dest)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}
func TestPolicyRejectsChangedAndUnsafePlans(t *testing.T) {
	e := testExecutor(t)
	p := basicPlan(Step{ID: "config", Kind: Write, Path: "~/.trialm/harness/test", Content: "ok"})
	original := p.ID
	p.Steps[0].Content = "changed"
	if _, err := e.Apply(context.Background(), p, Policy{PlanID: original}); err == nil {
		t.Fatal("changed plan accepted")
	}
	p.Seal()
	if _, err := e.Apply(context.Background(), p, Policy{PlanID: p.ID}); err != nil {
		t.Fatal(err)
	}
	for _, step := range []Step{{ID: "bad", Kind: Kind("shell"), Argv: []string{"sh", "-c", "echo bad"}}, {ID: "bad", Kind: Write, Path: filepath.Join(e.Home, ".codex", "config.toml")}, {ID: "bad", Kind: Fetch, Request: &download.Request{URL: "https://huggingface.co/o/r/resolve/main/f", Dest: "~/.trialm/f"}}, {ID: "bad", Kind: Install, Package: "anything", Argv: []string{"sh", "-c", "echo bad"}}, {ID: "bad", Kind: Start, Argv: []string{"sh", "-c", "echo bad"}}} {
		plan := basicPlan(step)
		if err := e.validate(plan); err == nil {
			t.Fatal("unsafe plan accepted", step)
		}
	}
	path := filepath.Join(e.Root, "link")
	_ = os.MkdirAll(e.Root, 0700)
	if err := os.Symlink(e.Home, path); err == nil {
		if err = e.validate(basicPlan(Step{ID: "bad", Kind: Write, Path: path + "/escape"})); err == nil {
			t.Fatal("symlink escape")
		}
	}
}
func TestReadyFailureAndPredictions(t *testing.T) {
	e := testExecutor(t)
	p := basicPlan()
	p.Predicted = verify.Predicted{DecodeTPS0: 10, PrefillTPS: 100}
	p.Seal()
	e.Ready = func(ctx context.Context, o verify.ReadyOptions) (verify.Report, error) {
		if o.Predicted != p.Predicted || o.ContextTokens != p.ContextTokens {
			t.Fatal(o)
		}
		return verify.Report{Failing: "tool_calls", Checks: []verify.Check{{Name: "tool_calls", OK: false}}}, nil
	}
	j, err := e.Apply(context.Background(), p, Policy{PlanID: p.ID})
	var failed *NotReady
	if !errors.As(err, &failed) || failed.Check != "tool_calls" || j.State == "ready" || j.Report == nil {
		t.Fatal(j, err)
	}
	saved, _ := e.Status(p.Slug)
	if saved.Report.Failing != "tool_calls" {
		t.Fatal(saved.Report)
	}
}
func TestAdminIsOnlyRenderedAndCounted(t *testing.T) {
	e := testExecutor(t)
	p := basicPlan(Step{ID: "wired-limit", Kind: Admin, Title: "Wired limit", Argv: []string{"sudo", "sysctl", "iogpu.wired_limit_mb=20480"}, RebootReset: true})
	// Deliberately leave Admin nil. No test executes an OS command.
	_, err := e.Apply(context.Background(), p, Policy{PlanID: p.ID})
	if err == nil || !strings.Contains(err.Error(), "OS password prompt required") {
		t.Fatal(err)
	}
	if p.Prompts.Passwords != 1 || p.Prompts.AfterRestart == "" {
		t.Fatal(p.Prompts)
	}
}
func TestMaterialPolicy(t *testing.T) {
	p := basicPlan(Step{ID: "model", Kind: Fetch, Request: &download.Request{Size: 100}})
	b, _ := json.Marshal(p)
	var after Plan
	_ = json.Unmarshal(b, &after)
	after.Steps[0].Request.Size = 110
	after.Seal()
	if MaterialChange(p, after) {
		t.Fatal("10% asks again")
	}
	after.Steps[0].Request.Size = 111
	after.Seal()
	if !MaterialChange(p, after) {
		t.Fatal("over 10% not counted")
	}
	after = p
	after.Steps = append(append([]Step(nil), p.Steps...), Step{ID: "new", Kind: Install})
	if !MaterialChange(p, after) {
		t.Fatal("new install not counted")
	}
}
func TestLegacyUndo(t *testing.T) {
	e := testExecutor(t)
	config := filepath.Join(e.Home, ".claude", "settings.json")
	_ = os.MkdirAll(filepath.Dir(config), 0700)
	_ = os.WriteFile(config, []byte("new"), 0600)
	_ = os.WriteFile(config+".bak-cyrlm", []byte("original"), 0600)
	script := filepath.Join(e.Root, "llamacpp", "model-cyrlm.sh")
	_ = os.MkdirAll(filepath.Dir(script), 0700)
	_ = os.WriteFile(script, []byte("legacy"), 0600)
	if _, err := e.Undo(context.Background(), "model-cyrlm", UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(config)
	if string(got) != "original" {
		t.Fatal(string(got))
	}
	if _, err := os.Stat(script); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}

type growingDownloader struct{ progress func(download.Progress) }

func (d *growingDownloader) SetProgress(f func(download.Progress)) { d.progress = f }
func (d *growingDownloader) Fetch(ctx context.Context, r download.Request) (download.Result, error) {
	d.progress(download.Progress{Total: 111})
	if err := ctx.Err(); err != nil {
		return download.Result{}, err
	}
	if err := os.MkdirAll(filepath.Dir(r.Dest), 0700); err != nil {
		return download.Result{}, err
	}
	if err := os.WriteFile(r.Dest, []byte(strings.Repeat("x", 111)), 0600); err != nil {
		return download.Result{}, err
	}
	return download.Result{Path: r.Dest, Bytes: 111}, nil
}
func TestDownloadGrowthNeedsConcreteSecondApproval(t *testing.T) {
	e := testExecutor(t)
	e.Downloader = &growingDownloader{}
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Repeat("x", 111))))
	p := basicPlan(Step{ID: "model", Kind: Fetch, Request: &download.Request{URL: "https://huggingface.co/o/r/resolve/rev/model.gguf", SHA256: digest, Size: 100, Dest: "~/.trialm/models/model.gguf"}})
	j, err := e.Apply(context.Background(), p, Policy{PlanID: p.ID})
	var approval *ApprovalRequired
	if !errors.As(err, &approval) || j.State != "needs_approval" || j.PendingPlan == nil {
		t.Fatal(j, err)
	}
	if approval.Plan.ID == p.ID {
		t.Fatal("material plan retained old hash")
	}
	if _, err = e.Resume(context.Background(), p.Slug, p.ID); err == nil {
		t.Fatal("old approval accepted")
	}
	j, err = e.Resume(context.Background(), p.Slug, approval.Plan.ID)
	if err != nil || j.State != "ready" || j.Prompts.Approvals != 2 {
		t.Fatal(j, err)
	}
}
func TestJournalLockIsExclusive(t *testing.T) {
	e := testExecutor(t)
	path, _ := e.journalPath("test")
	release, err := lock(path)
	if err != nil {
		t.Fatal(err)
	}
	if release2, err := lock(path); err == nil {
		release2()
		release()
		t.Fatal("second writer acquired lock")
	}
	release()
	release, err = lock(path)
	if err != nil {
		t.Fatal(err)
	}
	release()
}
func TestRefetchCorruptOwnedModel(t *testing.T) {
	e := testExecutor(t)
	data := []byte(strings.Repeat("x", 100))
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	d := &resumeDownloader{data: data}
	e.Downloader = d
	p := basicPlan(Step{ID: "model", Kind: Fetch, Request: &download.Request{URL: "https://huggingface.co/o/r/resolve/rev/f", SHA256: digest, Size: 100, Dest: "~/.trialm/models/f"}})
	if _, err := e.Apply(context.Background(), p, Policy{PlanID: p.ID}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.expand(p.Steps[0].Request.Dest), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Resume(context.Background(), p.Slug, ""); err != nil {
		t.Fatal(err)
	}
	if len(d.requests) != 2 || !matchesDigest(e.expand(p.Steps[0].Request.Dest), digest) {
		t.Fatal("corrupt model was not refetched")
	}
}
func TestLegacyNameIsRetained(t *testing.T) {
	in := fixture(t)
	in.Profile, _ = engine.Get("ollama")
	in.Installed = true
	in.OwnedFileExists = func(rel string) bool { return rel == ".trialm/ollama/qwen3-8b-cyrlm.Modelfile" }
	p, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	if p.LocalModelName != "qwen3:8b-cyrlm" {
		t.Fatal(p.LocalModelName)
	}
}

func TestOutOfRangeInstalledEngineUsesPinnedBinary(t *testing.T) {
	in := fixture(t)
	in.Installed = true
	in.InstalledVersion = "b10000"
	in.Binary = "/opt/homebrew/bin/llama-server"
	p, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range p.Steps {
		if step.ID == "server" && step.Argv[0] != "~/.trialm/engines/"+p.Slug+"/llama-server" {
			t.Fatal(step.Argv)
		}
	}
}

func TestJSONExpansionEscapesWindowsPaths(t *testing.T) {
	e := &Executor{Root: `C:\Users\Example User\.trialm`, Home: `C:\Users\Example User`}
	b, err := e.content(`{"config":"~/.trialm/harness/config.json"}`, true)
	if err != nil || !json.Valid(b) {
		t.Fatal(string(b), err)
	}
	var parsed map[string]string
	_ = json.Unmarshal(b, &parsed)
	if parsed["config"] != e.Root+"/harness/config.json" {
		t.Fatal(parsed)
	}
}

func TestWindowsLMStudioInstallerIsPinnedAndScoped(t *testing.T) {
	in := fixture(t)
	in.Profile, _ = engine.Get("lmstudio")
	in.OS, in.Arch, in.Backend = "windows", "amd64", "cpu"
	p, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	e := testExecutor(t)
	if err = e.validate(p); err != nil {
		t.Fatal(err)
	}
	installer := p.Steps[0]
	if installer.Kind != RunInstaller || installer.Argv[0] != "powershell" || !strings.HasSuffix(installer.Request.Dest, ".ps1") {
		t.Fatal(installer)
	}
	if fmt.Sprintf("%x", sha256.Sum256([]byte(installer.Content))) != installer.Request.SHA256 {
		t.Fatal("Windows installer source lost its exact digest")
	}
	if !strings.Contains(strings.Join(installer.Env, "\n"), "LMS_NO_MODIFY_PATH=1") {
		t.Fatal(installer.Env)
	}
}
func TestUndoRemovesOnlyPinnedNativeImports(t *testing.T) {
	for _, pointer := range []bool{false, true} {
		t.Run(fmt.Sprint(pointer), func(t *testing.T) {
			e := testExecutor(t)
			root := filepath.Join(e.Home, ".lmstudio")
			if pointer {
				root = filepath.Join(e.Home, "custom-model-home")
				if err := os.WriteFile(filepath.Join(e.Home, ".lmstudio-home-pointer"), []byte(root), 0600); err != nil {
					t.Fatal(err)
				}
			}
			data := []byte("pinned model")
			digest := fmt.Sprintf("%x", sha256.Sum256(data))
			source := filepath.Join(e.Root, "models", "model.gguf")
			native := filepath.Join(root, "models", "trialm", "test", "model.gguf")
			unrelated := filepath.Join(root, "models", "owner", "existing.gguf")
			for _, file := range []string{source, native, unrelated} {
				if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			p := basicPlan(Step{ID: "model", Kind: Fetch, Request: &download.Request{SHA256: digest, Dest: source}}, Step{ID: "import", Kind: Install, Package: "lms-import", NativeRepo: "trialm/test"})
			j := Journal{Plan: p, State: "ready", Steps: []Record{{State: "done", Inverse: Inverse{Category: Fetch, Path: source}}, {State: "done", Inverse: Inverse{Category: Install, Model: true, NativeRepo: "trialm/test"}}, {State: "done", Inverse: Inverse{Category: Check}}}}
			journalPath, _ := e.journalPath("test")
			if err := save(journalPath, &j); err != nil {
				t.Fatal(err)
			}
			if _, err := e.Undo(context.Background(), "test", UndoOptions{Models: true}); err != nil {
				t.Fatal(err)
			}
			for _, file := range []string{source, native} {
				if _, err := os.Stat(file); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("model retained", file, err)
				}
			}
			b, err := os.ReadFile(unrelated)
			if err != nil || string(b) != string(data) {
				t.Fatal("unrelated native model changed", err)
			}
		})
	}
}

func TestReplacementPreservesInverseOwnership(t *testing.T) {
	e := testExecutor(t)
	data := []byte(strings.Repeat("x", 100))
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	e.Downloader = &resumeDownloader{data: data}
	p := basicPlan(Step{ID: "model", Kind: Fetch, Request: &download.Request{URL: "https://huggingface.co/o/r/resolve/rev/a", SHA256: digest, Size: 100, Dest: "~/.trialm/models/a"}})
	if _, err := e.Apply(context.Background(), p, Policy{PlanID: p.ID}); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(p)
	var replacement Plan
	_ = json.Unmarshal(b, &replacement)
	replacement.Steps[0].Request.Dest = "~/.trialm/models/b"
	replacement.Seal()
	if _, err := e.Apply(context.Background(), replacement, Policy{PlanID: replacement.ID}); err == nil || !strings.Contains(err.Error(), "model path") {
		t.Fatal(err)
	}
	if !matchesDigest(e.expand(p.Steps[0].Request.Dest), digest) {
		t.Fatal("original inverse lost its model")
	}
	if _, err := e.Undo(context.Background(), p.Slug, UndoOptions{Models: true}); err != nil {
		t.Fatal(err)
	}
	j, err := e.Apply(context.Background(), replacement, Policy{PlanID: replacement.ID})
	if err != nil || j.Prompts.Approvals != 2 {
		t.Fatal(j.Prompts, err)
	}
	if _, err = e.Undo(context.Background(), p.Slug, UndoOptions{Models: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(e.expand(replacement.Steps[0].Request.Dest)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("replacement model escaped its inverse", err)
	}
}
