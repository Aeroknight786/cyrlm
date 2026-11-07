package setup

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aeroknight786/trialm/internal/harness"
	"github.com/aeroknight786/trialm/internal/setup/download"
	"github.com/aeroknight786/trialm/internal/setup/proc"
	"github.com/aeroknight786/trialm/internal/verify"
)

type Fetcher interface {
	Fetch(context.Context, download.Request) (download.Result, error)
}
type ReadyFunc func(context.Context, verify.ReadyOptions) (verify.Report, error)
type Update struct {
	Index    int                `json:"index"`
	Total    int                `json:"total"`
	Kind     Kind               `json:"kind"`
	Title    string             `json:"title"`
	State    string             `json:"state"`
	Progress *download.Progress `json:"progress,omitempty"`
	Lines    []string           `json:"lines,omitempty"`
}
type Executor struct {
	Root, Home string
	Downloader Fetcher
	Ready      ReadyFunc
	Event      func(string, any)
	Admin      func(context.Context, []string) error
	mu         sync.Mutex
}
type Inverse struct {
	NativeRepo string       `json:"native_repo,omitempty"`
	Stopped    *proc.Record `json:"stopped,omitempty"`
	Env        []string     `json:"env,omitempty"`
	Model      bool         `json:"model,omitempty"`
	Path       string       `json:"path,omitempty"`
	Data       []byte       `json:"data,omitempty"`
	Mode       os.FileMode  `json:"mode,omitempty"`
	Existed    bool         `json:"existed,omitempty"`
	PIDFile    string       `json:"pid_file,omitempty"`
	Argv       []string     `json:"argv,omitempty"`
	Category   Kind         `json:"category"`
}
type Record struct {
	State    string            `json:"state"`
	Bytes    int64             `json:"bytes"`
	Progress download.Progress `json:"progress"`
	Inverse  Inverse           `json:"inverse"`
	Error    string            `json:"error,omitempty"`
}
type Journal struct {
	Running     map[string]bool   `json:"running,omitempty"`
	PendingPlan *Plan             `json:"pending_plan,omitempty"`
	Addresses   map[string]string `json:"addresses,omitempty"`
	Plan        Plan              `json:"plan"`
	State       string            `json:"state"`
	Prompts     Prompts           `json:"prompts"`
	Steps       []Record          `json:"steps"`
	Report      *verify.Report    `json:"report,omitempty"`
}
type Policy struct{ PlanID string }
type UndoOptions struct{ Models, Engines bool }
type NotReady struct{ Check string }

func (e *NotReady) Error() string { return "setup is not Ready: " + e.Check }
func (e *Executor) emit(kind string, data any) {
	if e.Event != nil {
		e.Event(kind, data)
	}
}
func (e *Executor) expand(s string) string {
	s = strings.ReplaceAll(s, "~/.trialm", e.Root)
	s = strings.ReplaceAll(s, "~/", e.Home+string(filepath.Separator))
	s = strings.ReplaceAll(s, "{PATH}", os.Getenv("PATH"))
	s = strings.ReplaceAll(s, "{LD_LIBRARY_PATH}", os.Getenv("LD_LIBRARY_PATH"))
	return s
}
func (e *Executor) journalPath(slug string) (string, error) {
	if slug == "" || slug != filepath.Base(slug) || strings.ContainsAny(slug, "/\\") || slug == "." || slug == ".." {
		return "", errors.New("invalid setup slug")
	}
	return filepath.Join(e.Root, "setups", slug, "journal.json"), nil
}
func atomicWrite(path string, b []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".setup-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}
func save(path string, j *Journal) error {
	b, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, b, 0600)
}
func (e *Executor) Status(slug string) (Journal, error) {
	var j Journal
	path, err := e.journalPath(slug)
	if err != nil {
		return j, err
	}
	b, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(b, &j)
		if err == nil && len(j.Plan.Steps) != len(j.Steps) {
			err = errors.New("journal step count differs from plan")
		}
	}
	return j, err
}
func (e *Executor) Apply(ctx context.Context, p Plan, policy Policy) (Journal, error) {
	id := p.ID
	p.Seal()
	if p.ID == "" || policy.PlanID != p.ID || id != p.ID {
		return Journal{}, errors.New("setup plan changed; review trialm setup --dry-run and approve its new --yes=<plan_id>")
	}
	if err := e.validate(p); err != nil {
		return Journal{}, err
	}
	path, err := e.journalPath(p.Slug)
	if err != nil {
		return Journal{}, err
	}
	unlock, err := lock(path)
	if err != nil {
		return Journal{}, err
	}
	defer unlock()
	j, err := e.Status(p.Slug)
	if errors.Is(err, os.ErrNotExist) {
		j = Journal{Plan: p, State: "applying", Prompts: p.Prompts, Steps: make([]Record, len(p.Steps))}
	} else if err != nil {
		return j, err
	} else if j.Plan.ID != p.ID {
		if err = reconcile(&j, p); err != nil {
			return j, err
		}
	}
	if j.State == "undone" {
		for i := range j.Steps {
			if j.Steps[i].State == "undone" {
				j.Steps[i] = Record{}
			}
		}
		j.Prompts.Approvals++
	}
	// A resumed plan retains the journal's exact install/fetch steps.
	if len(j.Steps) != len(p.Steps) {
		return j, errors.New("journal step count differs from plan")
	}
	j.Plan = p
	j.PendingPlan = nil
	j.State = "applying"
	if err = save(path, &j); err != nil {
		return j, err
	}
	for i, s := range p.Steps {
		r := &j.Steps[i]
		if r.State == "done" && s.Kind != Start && s.Kind != Check {
			if s.Kind != Fetch || matchesDigest(e.expand(s.Request.Dest), s.Request.SHA256) {
				continue
			}
		}
		u := Update{Index: i + 1, Total: len(p.Steps), Kind: s.Kind, Title: s.Title, State: "running"}
		if r.State == "" {
			r.Inverse, err = e.inverse(s, p.Slug)
			if err != nil {
				return j, err
			}
		}
		r.State = "running"
		if err = save(path, &j); err != nil {
			return j, err
		}
		e.emit("setup_step", u)
		err = e.run(ctx, s, &j, i, path, u)
		if err != nil {
			r.State = "failed"
			r.Error = err.Error()
			j.State = "failed"
			var approval *ApprovalRequired
			if errors.As(err, &approval) {
				j.State = "needs_approval"
				j.PendingPlan = &approval.Plan
				e.emit("setup_plan", map[string]any{"plan_id": approval.Plan.ID, "plan": approval.Plan, "pending": true, "reason": approval.Reason})
			}
			if ctx.Err() != nil {
				r.State = "paused"
				j.State = "paused"
			}
			saveErr := save(path, &j)
			u.State = r.State
			e.emit("setup_step", u)
			if j.State == "paused" {
				e.emit("setup_paused", j)
			}
			return j, errors.Join(err, saveErr)
		}
		r.State = "done"
		r.Error = ""
		u.State = "done"
		if err = save(path, &j); err != nil {
			return j, err
		}
		e.emit("setup_step", u)
	}
	j.State = "ready"
	if err = save(path, &j); err != nil {
		return j, err
	}
	e.emit("setup_ready", j.Report)
	return j, nil
}
func (e *Executor) inverse(s Step, slug string) (Inverse, error) {
	inv := Inverse{NativeRepo: s.NativeRepo, Path: e.expand(s.Path), Category: s.Kind, Argv: s.Inverse, Env: s.Env, Model: s.Package == "ollama-model" || s.Package == "lms-import"}
	if s.Kind == Fetch {
		inv.Path = e.expand(s.Request.Dest)
	}
	if s.Kind == Start && len(s.Inverse) == 0 {
		inv.PIDFile = filepath.Join(e.Root, "run", slug+"-"+s.ID+".pid")
	}
	if s.Kind == Stop {
		b, err := os.ReadFile(inv.Path)
		if err != nil {
			return inv, err
		}
		var record proc.Record
		if err = json.Unmarshal(b, &record); err != nil {
			return inv, err
		}
		inv.PIDFile = inv.Path
		inv.Path = ""
		inv.Stopped = &record
	}
	if inv.Path != "" {
		info, err := os.Lstat(inv.Path)
		if err == nil {
			inv.Existed = true
			inv.Mode = info.Mode().Perm()
			if s.Kind == Write {
				inv.Data, err = os.ReadFile(inv.Path)
			}
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return inv, err
		}
	}
	return inv, nil
}
func (e *Executor) run(ctx context.Context, s Step, j *Journal, i int, path string, u Update) error {
	expand := func(argv []string) []string {
		out := append([]string(nil), argv...)
		for k := range out {
			out[k] = e.expand(out[k])
		}
		return out
	}
	r := &j.Steps[i]
	logPath := filepath.Join(e.Root, "logs", j.Plan.Slug, s.ID+".log")
	switch s.Kind {
	case Fetch, Install, RunInstaller:
		if s.Request != nil {
			request := *s.Request
			request.Dest = e.expand(request.Dest)
			request.Token = os.Getenv("HF_TOKEN")
			repo, file := hfLocation(request.URL)
			request.Reuse = download.Candidates(e.Home, repo, file, request.SHA256)
			fetchCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			var changed *ApprovalRequired
			onProgress := func(p download.Progress) {
				e.mu.Lock()
				defer e.mu.Unlock()
				r.Progress = p
				if request.Size > 0 && p.Total > request.Size+request.Size/10 && changed == nil {
					encoded, _ := json.Marshal(j.Plan)
					var candidate Plan
					_ = json.Unmarshal(encoded, &candidate)
					candidate.Steps[i].Request.Size = p.Total
					candidate.Seal()
					if MaterialChange(j.Plan, candidate) {
						changed = &ApprovalRequired{Plan: candidate, Reason: "download grew by more than 10%"}
						cancel()
					}
				}
				_ = save(path, j)
				u.Progress = &p
				e.emit("setup_progress", u)
			}
			client := e.Downloader
			if client == nil {
				client = download.Client{Progress: onProgress}
			}
			if aware, ok := client.(interface{ SetProgress(func(download.Progress)) }); ok {
				aware.SetProgress(onProgress)
			}
			result, err := fetchRetry(fetchCtx, client, request)
			e.mu.Lock()
			material := changed
			e.mu.Unlock()
			if material != nil {
				return material
			}
			if err != nil {
				return err
			}
			r.Bytes += result.Bytes
			if s.Kind == Install {
				if err := unpack(ctx, request.Dest, e.expand(s.Path), s.ArchiveRoot); err != nil {
					return err
				}
				return os.Remove(request.Dest)
			}
			if s.Kind == RunInstaller {
				b, err := os.ReadFile(request.Dest)
				if err != nil {
					return err
				}
				if string(b) != s.Content {
					return errors.New("installer source differs from approved source")
				}
				e.emit("setup_log", Update{Lines: []string{s.Content}})
			}
		}
		if len(s.Argv) > 0 {
			return e.command(ctx, expand(j.effectiveArgs(s.Argv)), expand(j.effectiveArgs(s.Env)), logPath, u)
		}
		return nil
	case Write:
		content, err := e.content(j.effective(s.Content), s.ID != "recipe")
		if err != nil {
			return err
		}
		return atomicWrite(e.expand(s.Path), content, 0600)
	case Start:
		if err := e.pickPort(ctx, j, s, r.Inverse.PIDFile); err != nil {
			return err
		}
		if err := save(path, j); err != nil {
			return err
		}
		s.Argv = j.effectiveArgs(s.Argv)
		if len(s.Argv) > 0 && s.Argv[0] == "trialm" {
			executable, err := os.Executable()
			if err != nil {
				return err
			}
			s.Argv[0] = executable
		}
		s.Env = j.effectiveArgs(s.Env)
		s.Health = j.effective(s.Health)
		if len(s.Inverse) > 0 {
			if err := e.command(ctx, expand(s.Argv), expand(s.Env), logPath, u); err != nil {
				return err
			}
			return proc.WaitHealth(ctx, s.Health)
		}
		_, err := proc.Start(ctx, proc.Options{
			Argv:    expand(s.Argv),
			Env:     expand(s.Env),
			PIDFile: r.Inverse.PIDFile,
			Log:     logPath,
			Health:  s.Health,
			Lines:   func(lines []string) { u.Lines = lines; e.emit("setup_log", u) },
		})
		return err
	case Stop:
		return proc.Stop(e.expand(s.Path))
	case Admin:
		if e.Admin == nil {
			return fmt.Errorf("OS password prompt required for %q", s.Argv)
		}
		return e.Admin(ctx, s.Argv)
	case EngineDelegate:
		return errors.New("engine delegation arrives in P3")
	case Check:
		if len(s.Argv) > 0 {
			return e.command(ctx, s.Argv, nil, logPath, u)
		}
		if s.Content != "" {
			return errors.New(s.Content)
		}
		o := j.Plan.ReadyOptions
		o.Endpoint = j.Plan.CheckEndpoint
		if o.Endpoint == "" {
			o.Endpoint = j.Plan.Endpoint
		}
		o.APIs = j.Plan.APIs
		o.Endpoint = j.effective(o.Endpoint)
		o.Model = e.expand(j.Plan.LocalModelName)
		o.ContextTokens = j.Plan.ContextTokens
		o.Predicted = j.Plan.Predicted
		o.BitsPerWeight = j.Plan.BitsPerWeight
		if j.Plan.Harness != "" {
			h, _ := harness.Get(j.Plan.Harness)
			o.Harness = &h
		}
		o.Progress = func(line string) { e.emit("setup_log", Update{Lines: []string{line}}) }
		ready := e.Ready
		if ready == nil {
			ready = verify.Ready
		}
		report, err := ready(ctx, o)
		j.Report = &report
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return &NotReady{Check: err.Error()}
		}
		if !report.Ready {
			if report.Failing == "" {
				for _, check := range report.Checks {
					if !check.OK {
						report.Failing = check.Name
						break
					}
				}
			}
			if report.Failing == "" {
				report.Failing = "verification"
			}
			return &NotReady{Check: report.Failing}
		}
		return nil
	}
	return fmt.Errorf("unknown setup step %q", s.Kind)
}
func (e *Executor) command(ctx context.Context, argv, env []string, logPath string, u Update) error {
	if err := os.MkdirAll(filepath.Dir(logPath), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr = f, f
	cmd.Env = append(os.Environ(), env...)
	proc.DetachAttrs(cmd)
	cmd.Cancel = func() error { return proc.Terminate(cmd.Process) }
	if err = cmd.Start(); err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err = <-exited:
			u.Lines = proc.Tail(logPath, 5)
			e.emit("setup_log", u)
			return err
		case <-ticker.C:
			u.Lines = proc.Tail(logPath, 5)
			e.emit("setup_log", u)
		}
	}
}

func (e *Executor) expandArgs(argv []string) []string {
	out := append([]string(nil), argv...)
	for i := range out {
		out[i] = e.expand(out[i])
	}
	return out
}

// OSAdmin is wired only by the interactive CLI. Tests leave Executor.Admin nil.
func OSAdmin(ctx context.Context, argv []string) error {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	return cmd.Run()
}

func matchesDigest(path, digest string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	hash := sha256.New()
	if _, err = io.Copy(hash, f); err != nil {
		return false
	}
	return fmt.Sprintf("%x", hash.Sum(nil)) == digest
}

func (e *Executor) Resume(ctx context.Context, slug, approvalID string) (Journal, error) {
	j, err := e.Status(slug)
	if err != nil {
		return j, err
	}
	if j.State == "undone" {
		return j, errors.New("setup was undone; choose a recipe to set it up again")
	}
	p := j.Plan
	if j.PendingPlan != nil {
		p = *j.PendingPlan
		if approvalID != p.ID {
			e.emit("setup_plan", map[string]any{"plan_id": p.ID, "plan": p})
			return j, &ApprovalRequired{Plan: p, Reason: "approve the material setup change"}
		}
	}
	if approvalID != "" && approvalID != p.ID {
		return j, errors.New("setup plan changed; approve the saved plan_id")
	}
	e.emit("setup_status", j)
	return e.Apply(ctx, p, Policy{PlanID: p.ID})
}
func (j Journal) Summary() string {
	done := 0
	var progress download.Progress
	for _, r := range j.Steps {
		if r.State == "done" {
			done++
		} else if r.Progress.Total > 0 {
			progress = r.Progress
		}
	}
	message := fmt.Sprintf("%d of %d done", done, len(j.Steps))
	if progress.Total > 0 {
		message += fmt.Sprintf(", download %d%%", progress.Done*100/progress.Total)
	}
	return message
}

func (e *Executor) Running(ctx context.Context, j Journal) map[string]bool {
	running := map[string]bool{}
	for i, step := range j.Plan.Steps {
		if step.Kind != Start {
			continue
		}
		if j.Steps[i].Inverse.PIDFile != "" {
			_, running[step.ID] = proc.Status(ctx, j.Steps[i].Inverse.PIDFile)
		} else {
			running[step.ID] = proc.Health(ctx, j.effective(step.Health))
		}
	}
	return running
}
