package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/aeroknight786/trialm/internal/engine"
	"github.com/aeroknight786/trialm/internal/harness"
	"github.com/aeroknight786/trialm/internal/server"
	"github.com/aeroknight786/trialm/internal/setup"
	"github.com/aeroknight786/trialm/internal/verify"
)

var ErrNoReady = errors.New("no Ready setup exists; run trialm setup")

type LaunchOptions struct {
	Slug, Tool                string
	Print, Stop, Check, Serve bool
	Log                       io.Writer
	Admin                     func(context.Context, []string) error
}
type Launch struct {
	Journal  setup.Journal  `json:"setup"`
	Wiring   harness.Setup  `json:"wiring"`
	Endpoint string         `json:"endpoint"`
	Bridge   *server.Server `json:"-"`
}

// ReadySetups returns the most recently saved Ready journal first.
func (s Session) ReadySetups(ctx context.Context) ([]setup.Journal, error) {
	e, err := s.SetupExecutor()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(e.Root, "setups"))
	if errors.Is(err, os.ErrNotExist) {
		return []setup.Journal{}, nil
	}
	if err != nil {
		return nil, err
	}
	type saved struct {
		j  setup.Journal
		at int64
	}
	var rows []saved
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		j, readErr := e.Status(entry.Name())
		if readErr != nil || j.State != "ready" || j.Report == nil || !j.Report.Ready {
			continue
		}
		info, readErr := os.Stat(filepath.Join(e.Root, "setups", entry.Name(), "journal.json"))
		if readErr != nil {
			return nil, readErr
		}
		j.Running = e.Running(ctx, j)
		rows = append(rows, saved{j, info.ModTime().UnixNano()})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].at > rows[j].at })
	out := make([]setup.Journal, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.j)
	}
	return out, nil
}

// Launch prepares a local route. The caller owns the bridge lifetime and the TTY.
func (s Session) Launch(ctx context.Context, o LaunchOptions) (l Launch, err error) {
	e, err := s.SetupExecutor()
	if err != nil {
		return l, err
	}
	e.Admin = o.Admin
	rows, err := s.ReadySetups(ctx)
	if err != nil {
		return l, err
	}
	for _, j := range rows {
		if o.Slug == "" || o.Slug == j.Plan.Slug {
			l.Journal = j
			break
		}
	}
	if l.Journal.State == "" {
		return l, ErrNoReady
	}
	p := l.Journal.Plan
	tool := o.Tool
	if tool == "" {
		tool = p.Harness
	}
	if tool == "" {
		h, _ := harness.DetectInstalled()
		tool = h.ID
	}
	h, err := harness.Get(tool)
	if err != nil {
		return l, err
	}
	if !o.Print {
		l.Journal, err = e.LaunchEngine(ctx, p.Slug, o.Stop)
		if err != nil || o.Stop {
			return l, err
		}
	}
	base := l.Journal.Effective(p.Endpoint)
	profile, profileErr := engine.Get(p.Runtime)
	if profileErr != nil {
		return l, fmt.Errorf("unknown saved engine %s", p.Runtime)
	}
	apis := profile.APIs
	bridge := h.NeedsBridge(apis) || o.Serve
	protocol := h.ProtocolFor(apis)
	if bridge {
		port := 11535
		if !o.Print {
			l.Bridge, port, err = server.Local(base, p.LocalModelName, p.RuntimeName, p.ContextTokens, port, o.Log)
			if err != nil {
				return l, err
			}
			go func(bridge *server.Server) { _ = bridge.Serve() }(l.Bridge)
		}
		base = "http://127.0.0.1:" + strconv.Itoa(port)
		apis = []string{"responses", "anthropic", "openai-chat", "ollama"}
		protocol = h.ProtocolFor(apis)
	}
	l.Endpoint = base
	u, err := url.Parse(base)
	if err != nil {
		return l, err
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		return l, err
	}
	l.Wiring = h.SetupFor(harness.Options{Port: port, Model: p.LocalModelName, ContextTokens: p.ContextTokens, Tuned: p.Tuned, Protocol: protocol, Bridge: true, Home: e.Root})
	l.Wiring.Unset = []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT"}
	if !o.Print {
		err = l.Wiring.WriteFiles()
	}
	if err == nil && o.Check {
		report, checkErr := verify.Ready(ctx, verify.ReadyOptions{Endpoint: base, Model: p.LocalModelName, ContextTokens: p.ContextTokens, Predicted: p.Predicted, BitsPerWeight: p.BitsPerWeight, Harness: &h, APIs: apis})
		l.Journal.Report = &report
		err = checkErr
		if err == nil && !report.Ready {
			err = &setup.NotReady{Check: report.Failing}
		}
	}
	if err != nil && l.Bridge != nil {
		_ = l.Bridge.Shutdown(context.Background())
	}
	s.Emit("launch", map[string]any{"engine": p.Runtime, "endpoint": base, "command": l.Wiring.Command, "env": l.Wiring.Env})
	return l, err
}

func (l Launch) PIDFiles() []string {
	var files []string
	for _, r := range l.Journal.Steps {
		if r.Inverse.PIDFile != "" {
			files = append(files, r.Inverse.PIDFile)
		}
	}
	return files
}

func (l Launch) Command() string { return strings.TrimSpace(l.Wiring.Render()) }
