package tui

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/aeroknight786/trialm/internal/core"
	"github.com/aeroknight786/trialm/internal/setup"
	"github.com/aeroknight786/trialm/internal/verify"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/aeroknight786/trialm/internal/openrouter"
	"github.com/aeroknight786/trialm/internal/store"
	"github.com/aeroknight786/trialm/internal/trial"
)

var update = flag.Bool("update", false, "update TUI frame goldens")

func fixture(t testing.TB) *Root {
	t.Helper()
	s := store.Store{Dir: t.TempDir()}
	p, _ := s.Preferences()
	p.Tool = "codex"
	p.Context = 65536
	p.QualityFloor = "3bit-ok"
	opts := core.Options{Device: "Apple M5", RAM: 24, Runtime: "all", Tuned: true, Context: p.Context, Use: "agent", QualityFloor: p.QualityFloor}
	cfg := SessionConfig(context.Background(), s, p, opts, 3, "test")
	cfg.UpdateNotice = nil
	cfg.Balance = nil
	cfg.Tools = []string{"Codex", "Claude Code"}
	m := New(cfg)
	d, e := cfg.Detect(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	m.detection = d
	l, e := cfg.Find(context.Background(), p)
	if e != nil {
		t.Fatal(e)
	}
	m.listing = l
	m.last = store.TrialEntry{ID: "fixture", At: time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC), Model: "qwen3.6:35b-a3b", Quant: "IQ3_XXS", Runtime: "llamacpp", Band: 60, Report: trial.Report{Requests: 38, LocalSeconds: 840, CloudSeconds: 190, ReadingSeconds: 370, WritingSeconds: 294, BetweenTurnsSeconds: 176, FirstTurn: 10, TypicalTurn: 9.8, SlowestTurn: 41, SlowestCause: "reading", Fidelity: "fp8 (declared), no-logging; upper bound on quality", SpendKnown: true, SpendUSD: .21}}
	m.history = []store.TrialEntry{m.last}
	m.notice = "v0.6.1 available · trialm update"
	setupFrame(m)
	return m
}
func TestViewGoldens(t *testing.T) {
	old := lipgloss.Writer.Profile
	lipgloss.Writer.Profile = colorprofile.TrueColor
	defer func() { lipgloss.Writer.Profile = old }()
	screens := []Screen{First, Paste, Calibration, Calibrating, Needs, Recipes, Details, Trial, Summary, Home, Gallery, ClaimCard, Search, Why, Setup, Chat, Ready, Launching, "setup-progress", "setup-paused", "home-running"}
	for _, screen := range screens {
		for _, w := range []int{60, 80, 120} {
			for _, plain := range []bool{false, true} {
				name := fmt.Sprintf("%s-%d-color%t", screen, w, !plain)
				t.Run(name, func(t *testing.T) {
					m := fixture(t)
					m.Update(tea.WindowSizeMsg{Width: w, Height: 24})
					m.stack = []Screen{screen}
					switch screen {
					case Setup, Ready, Launching, "setup-progress", "setup-paused", "home-running":
						setupFrame(m)
					}
					if screen == Setup {
						m.setup.update = setup.Update{}
						m.setup.states = nil
						m.setup.logs = nil
						m.setup.journal = setup.Journal{}
					}
					if screen == "setup-progress" {
						m.stack = []Screen{Setup}
						m.busy = true
					}
					if screen == "setup-paused" {
						m.stack = []Screen{Setup}
						m.setup.journal.State = "paused"
						m.message = "Setup paused. Enter resumes."
					}
					if screen == "home-running" {
						m.stack = []Screen{Home}
						m.ready[0].Running = map[string]bool{"server": true}
					}
					m.theme = newPalette(plain, false)
					m.started = time.Now().Add(-30 * time.Second)
					m.password = huh.NewInput().Title("OpenRouter key").Value(&m.secret).EchoMode(huh.EchoModePassword)
					m.password.Focus()
					m.search.SetValue("qwen")
					m.chat = newChat(context.Background(), nil)
					m.chat.resize(w, 24)
					got := m.View().Content
					if screen == Recipes && !strings.Contains(got, "Qwen3.6") {
						t.Fatal("recipe rows are not visible")
					}
					if plain && strings.Contains(got, "\x1b") {
						t.Fatal("NO_COLOR has an escape")
					}
					for _, line := range strings.Split(got, "\n") {
						if ansi.StringWidth(line) > w {
							t.Fatalf("overflow %d: %q", ansi.StringWidth(line), line)
						}
					}
					path := filepath.Join("testdata", name+".golden")
					if *update {
						if e := os.MkdirAll(filepath.Dir(path), 0755); e != nil {
							t.Fatal(e)
						}
						if e := os.WriteFile(path, []byte(got), 0644); e != nil {
							t.Fatal(e)
						}
					}
					want, e := os.ReadFile(path)
					if e != nil {
						t.Fatal(e)
					}
					if got != strings.ReplaceAll(string(want), "\r\n", "\n") { // a CRLF checkout is not a change
						t.Fatalf("frame changed: %s; use -update\ngot:\n%s\nwant:\n%s", path, got, want)
					}
				})
			}
		}
	}
}
func key(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	default:
		return tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
	}
}
func TestNavigationAndRating(t *testing.T) {
	m := fixture(t)
	m.stack = []Screen{Recipes}
	m.Update(key("d"))
	if m.screen() != Details {
		t.Fatal(m.screen())
	}
	m.Update(key("esc"))
	if m.screen() != Recipes {
		t.Fatal(m.screen())
	}
	m.stack = []Screen{Needs}
	m.field = 3
	m.Update(key("l"))
	if m.cfg.Preferences.Priority != "quality" {
		t.Fatal(m.cfg.Preferences.Priority)
	}
	m.stack = []Screen{Summary}
	m.cfg.Preferences.Priority = "quality"
	_, cmd := m.Update(key("1"))
	if cmd == nil || m.screen() != Recipes || m.cfg.Preferences.Priority != "balanced" {
		t.Fatal(m.screen(), m.cfg.Preferences.Priority)
	}
	rows, e := m.cfg.Store.History()
	if e != nil || len(rows) != 1 || rows[0].Rating != 1 {
		t.Fatal(rows, e)
	}
	m.Update(tea.PasteMsg{Content: "llama-server -m untrusted.gguf --evil"})
	if m.screen() != ClaimCard || !strings.Contains(m.View().Content, "claim parsing arrives in v0.6") {
		t.Fatal(m.View().Content)
	}
	m.theme = newPalette(true, true)
	m.stack = []Screen{First}
	if strings.ContainsAny(m.View().Content, "✓❯·≈━") {
		t.Fatal(m.View().Content)
	}
}
func TestClaimsHaveSourcesAndNeverRank(t *testing.T) {
	for _, c := range Claims() {
		if c.SourceURL == "" || c.SourceName == "" || c.Hardware == "" || c.Recipe == "" || c.Date == "" || c.DateBasis == "" || !strings.Contains("VCS", c.Grade) || len(c.Figures) == 0 {
			t.Fatal(c)
		}
		for _, f := range c.Figures {
			if string(f.Kind) != "claimed" || f.Src == "" {
				t.Fatal(f)
			}
		}
	}
	m := fixture(t)
	for _, r := range m.listing.Rows {
		if r.Result.Kind == "claimed" {
			t.Fatal("claim ranked")
		}
	}
}

// Driver preserves the real root through every tea message, with read-only
// snapshots serialized on tea's event loop so the race detector checks it too.
type snapshot chan Screen
type driver struct{ root *Root }

func (d *driver) Init() tea.Cmd  { return d.root.Init() }
func (d *driver) View() tea.View { return d.root.View() }
func (d *driver) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if v, ok := msg.(probePlan); ok {
		v.ready <- d.root.setup.plan.ID != "" && !d.root.busy
		return d, nil
	}
	if v, ok := msg.(probeRows); ok {
		v.ready <- len(d.root.listing.Rows) > 0 && !d.root.busy
		return d, nil
	}
	if ch, ok := msg.(snapshot); ok {
		ch <- d.root.screen()
		return d, nil
	}
	_, cmd := d.root.Update(msg)
	return d, cmd
}
func waitScreen(t *testing.T, p *tea.Program, want Screen) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ch := make(snapshot, 1)
		p.Send(ch)
		select {
		case got := <-ch:
			if got == want {
				return
			}
		case <-time.After(time.Second):
			t.Fatal("event loop stopped")
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("never reached %s", want)
}
func TestJourneyAKeyCount(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("TRIALM_OFFLINE", "1")
	s := store.Store{Dir: t.TempDir()}
	p, _ := s.Preferences()
	p.Tool = "codex"
	p.Context = 65536
	p.QualityFloor = "3bit-ok"
	clicks := atomic.Int32{}
	requests := atomic.Int32{}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/keys":
			fmt.Fprint(w, `{"key":"fake-secret"}`)
		case "/credits":
			fmt.Fprint(w, `{"data":{"total_credits":10,"total_usage":0}}`)
		default:
			t.Errorf("unexpected %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer api.Close()
	trialServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		fmt.Fprint(w, `{"choices":[{"message":{"content":"hello"}}]}`)
	}))
	defer trialServer.Close()
	cfg := SessionConfig(context.Background(), s, p, core.Options{Device: "Apple M5", RAM: 24, Runtime: "all", Tuned: true, Use: "agent", Context: 65536}, 3, "test")
	cfg.UpdateNotice = nil
	cfg.Balance = nil
	cfg.Tools = []string{"Codex", "Claude Code"}
	cfg.Headless = false
	cfg.Authorize = func(ctx context.Context, pasted string) error {
		return Authorize(ctx, s, openrouter.Client{BaseURL: api.URL}, api.URL+"/auth", func(raw string) error {
			clicks.Add(1)
			u, _ := url.Parse(raw)
			cb, _ := url.Parse(u.Query().Get("callback_url"))
			q := cb.Query()
			q.Set("code", "browser-code")
			cb.RawQuery = q.Encode()
			go func() {
				r, e := http.Get(cb.String())
				if e == nil {
					r.Body.Close()
				}
			}()
			return nil
		}, pasted)
	}
	cfg.Process = func(ctx context.Context, exe string, args []string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, exe, "-test.run=TestTrialHelper")
		cmd.Env = append(os.Environ(), "TRIALM_TUI_HELPER=1", "TRIALM_TUI_ENDPOINT="+trialServer.URL, "TRIALM_HOME="+s.Dir)
		return cmd
	}
	_ = s.Write("prefs.json", map[string]any{"calibration_offer": "accepted"})
	approvals := atomic.Int32{}
	launches := atomic.Int32{}
	cfg.Plan = func(ctx context.Context, row core.Row, p store.Preferences) (setup.Plan, error) {
		return setup.Plan{ID: "approved-plan", Slug: "local", Harness: "codex"}, nil
	}
	cfg.Apply = func(ctx context.Context, p setup.Plan, sink core.Sink) (setup.Journal, error) {
		if p.ID != "approved-plan" {
			return setup.Journal{}, errors.New("wrong approval")
		}
		approvals.Add(1)
		report := &verify.Report{Ready: true}
		sink(core.Event{Type: "setup_ready", Data: report})
		return setup.Journal{Plan: p, State: "ready", Report: report}, nil
	}
	trialProcess := cfg.Process
	cfg.Process = func(ctx context.Context, exe string, args []string) *exec.Cmd {
		cmd := trialProcess(ctx, exe, args)
		if args[0] == "launch" {
			launches.Add(1)
			cmd.Env = append(cmd.Env, "TRIALM_TUI_LAUNCH=1")
		}
		return cmd
	}
	m := New(cfg)
	program := tea.NewProgram(&driver{m}, tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithColorProfile(colorprofile.Ascii))
	done := make(chan error, 1)
	go func() { _, e := program.Run(); done <- e }()
	defer program.Kill()
	keys := 0
	waitScreen(t, program, First)
	program.Send(key("enter"))
	keys++
	waitScreen(t, program, Needs)
	program.Send(key("enter"))
	keys++
	waitScreen(t, program, Recipes)
	// Ranking completes asynchronously. Query the loop until the first row exists.
	deadline := time.Now().Add(5 * time.Second)
	for {
		ready := make(chan bool, 1)
		program.Send(probeRows{ready})
		if <-ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no recipes")
		}
		time.Sleep(5 * time.Millisecond)
	}
	program.Send(key("enter"))
	keys++
	waitScreen(t, program, Summary)
	program.Send(key("s"))
	keys++
	waitScreen(t, program, Setup)
	// The plan callback completes in the model loop before approval.
	deadline = time.Now().Add(5 * time.Second)
	for {
		ready := make(chan bool, 1)
		program.Send(probePlan{ready})
		if <-ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("plan not shown")
		}
		time.Sleep(time.Millisecond)
	}
	program.Send(key("enter"))
	keys++
	waitScreen(t, program, Ready)
	program.Send(key("enter"))
	keys++
	waitScreen(t, program, Home)
	program.Send(key("q"))
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if keys+1 > 7 || approvals.Load() != 1 || launches.Load() != 1 || clicks.Load() != 1 || requests.Load() != 1 {
		t.Fatalf("keys=%d browser clicks=%d requests=%d", keys, clicks.Load(), requests.Load())
	}
	t.Logf("%d keys including install paste, 1 approval, 1 browser click, 0 copy-paste. Calibration already completed.", keys+1)
}

type probePlan struct{ ready chan bool }
type probeRows struct{ ready chan bool }

func TestTrialHelper(t *testing.T) {
	if os.Getenv("TRIALM_TUI_HELPER") != "1" {
		return
	}
	if os.Getenv("TRIALM_TUI_LAUNCH") == "1" {
		return
	}
	r, e := http.Post(os.Getenv("TRIALM_TUI_ENDPOINT"), "application/json", strings.NewReader(`{"messages":[{"role":"user","content":"hello"}]}`))
	if e != nil {
		t.Fatal(e)
	}
	r.Body.Close()
	e = store.Default().SaveTrial(store.TrialEntry{ID: "child", At: time.Now(), Model: "fake", Report: trial.Report{Requests: 1}})
	if e != nil {
		t.Fatal(e)
	}
}

func TestMain(m *testing.M) {
	if os.Getenv("TRIALM_CALIBRATION_HELPER") == "1" {
		fmt.Println(`{"v":1,"type":"step","step":2,"total":4,"message":"bench fixture","eta_s":45}`)
		fmt.Println(`{"v":1,"type":"result","complete":true,"message":"done"}`)
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func TestCalibrationScreenAndPersistence(t *testing.T) {
	m := fixture(t)
	t.Setenv("TRIALM_HOME", m.cfg.Store.Dir)
	t.Setenv("TRIALM_CALIBRATION_HELPER", "1")
	m.stack = []Screen{Calibration}
	m.Update(key("s"))
	if m.screen() != Needs {
		t.Fatal(m.screen())
	}
	var prefs map[string]any
	if e := m.cfg.Store.Read("prefs.json", &prefs); e != nil || prefs["calibration_offer"] != "skipped" {
		t.Fatal(prefs, e)
	}
	m.stack = []Screen{First}
	m.afterKey()
	if m.screen() != Needs {
		t.Fatal("offer repeated")
	}
	m.stack = []Screen{Calibration}
	m.Update(key("enter"))
	if m.screen() != Calibrating {
		t.Fatal(m.screen())
	}
	job := m.calibration
	for {
		v := job.next()().(calibrationEventMsg)
		m.Update(v)
		if v.done {
			if v.err != nil {
				t.Fatal(v.err)
			}
			break
		}
		if v.event.Step == 2 && (!strings.Contains(m.View().Content, "ETA") || job.fraction != .25) {
			t.Fatal(job, m.View().Content)
		}
	}
	if m.screen() != Needs {
		t.Fatal(m.screen())
	}
	_ = m.cfg.Store.Read("prefs.json", &prefs)
	if prefs["calibration_offer"] != "accepted" {
		t.Fatal(prefs)
	}
	p := m.cfg.Preferences
	p.Priority = "speed"
	if e := m.cfg.Store.SavePreferences(p); e != nil {
		t.Fatal(e)
	}
	_ = m.cfg.Store.Read("prefs.json", &prefs)
	if prefs["calibration_offer"] != "accepted" {
		t.Fatal(prefs)
	}
}
func TestFooterBindings(t *testing.T) {
	m := fixture(t)
	allowed := map[Screen][]string{First: {"enter browser", "p paste", "s skip", "q quit"}, Calibration: {"enter calibrate", "s skip", "q quit"}, Recipes: {"enter try", "s set up", "d details", "w window", "r needs", "/ search"}, Details: {"enter try", "space boosts", "s set up", "? why", "esc back", "q quit"}, Summary: {"1 too slow", "2 fine", "3 great", "s set up", "t another", "q quit"}}
	for screen, want := range allowed {
		m.stack = []Screen{screen}
		if got := strings.Split(m.footer(), "  "); len(got) > 6 || strings.Join(got, "|") != strings.Join(want, "|") {
			t.Fatal(screen, got)
		}
	}
}
func TestNoColorOverridesForce(t *testing.T) {
	t.Setenv("NO_COLOR", "yes")
	t.Setenv("FORCE_COLOR", "1")
	if len(programOptions()) != 1 {
		t.Fatal("NO_COLOR not applied")
	}
	m := fixture(t)
	m.theme = newPalette(true, false)
	m.stack = []Screen{Needs}
	m.Update(tea.ColorProfileMsg{Profile: colorprofile.TrueColor})
	if strings.Contains(m.View().Content, "\x1b") {
		t.Fatal("colour leaked")
	}
}

func BenchmarkCachedView(b *testing.B) {
	m := fixture(b)
	m.stack = []Screen{Recipes}
	m.width = 120
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.View()
	}
}
func TestASCIIConsole(t *testing.T) {
	if !asciiTerminal("windows", func(string) string { return "" }) || asciiTerminal("windows", func(k string) string {
		if k == "WT_SESSION" {
			return "yes"
		}
		return ""
	}) || !asciiTerminal("linux", func(k string) string {
		if k == "TERM" {
			return "dumb"
		}
		return ""
	}) {
		t.Fatal("console glyph selection")
	}
}

func TestQualityDefaultFollowsGPU(t *testing.T) {
	for _, gb := range []float64{16, 48} {
		p, _ := (store.Store{Dir: t.TempDir()}).Preferences()
		p.Tool = "codex"
		m := New(Config{Store: store.Store{Dir: t.TempDir()}, Preferences: p})
		d := core.Detection{}
		d.Target.GPUMemBytes = gb * (1 << 30)
		m.Update(detectedMsg{detection: d})
		want := "3bit-ok"
		if gb > 24 {
			want = "4bit+"
		}
		if m.cfg.Preferences.QualityFloor != want {
			t.Fatal(gb, m.cfg.Preferences)
		}
	}
}
func TestHeadlessCodeAgainstFake(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/keys" {
			fmt.Fprint(w, `{"key":"fake-secret"}`)
		} else {
			fmt.Fprint(w, `{"data":{"total_credits":10,"total_usage":1}}`)
		}
	}))
	defer srv.Close()
	s := store.Store{Dir: t.TempDir()}
	h, e := beginHeadless(s, openrouter.Client{BaseURL: srv.URL}, srv.URL+"/auth")
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(h.URL, h.verifier) || !strings.Contains(h.URL, "S256") {
		t.Fatal("invalid public authorization URL")
	}
	if e = h.finish(context.Background(), "http://127.0.0.1/callback?code=one-use-code"); e != nil {
		t.Fatal(e)
	}
	t.Setenv("OPENROUTER_API_KEY", "")
	if s.Key() != "fake-secret" {
		t.Fatal("key not saved")
	}
}

func TestInterruptMapsToExit130(t *testing.T) {
	m := fixture(t)
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil || !errors.Is(m.Error, context.Canceled) {
		t.Fatal(m.Error)
	}
	if !errors.Is(terminalResult(tea.ErrInterrupted, m.Error), context.Canceled) {
		t.Fatal("interrupt would not map to exit 130")
	}
}

func TestFirstScreenUsesSavedBudget(t *testing.T) {
	m := fixture(t)
	m.stack = []Screen{First}
	m.cfg.Preferences.BudgetUSD = 5
	if !strings.Contains(m.View().Content, "5.00 USD") {
		t.Fatal(m.View().Content)
	}
}

func TestPastesReachMaskedInputAndChat(t *testing.T) {
	m := fixture(t)
	m.stack = []Screen{First}
	m.Update(key("p"))
	m.Update(tea.PasteMsg{Content: "fake-secret-key"})
	if m.secret != "fake-secret-key" || strings.Contains(m.View().Content, "fake-secret-key") {
		t.Fatal("masked paste did not reach the input safely")
	}
	m.chat = newChat(context.Background(), nil)
	m.stack = []Screen{Chat}
	m.Update(tea.PasteMsg{Content: "hello pasted chat"})
	if m.chat.input.Value() != "hello pasted chat" {
		t.Fatal(m.chat.input.Value())
	}
	m.stack = []Screen{Search}
	m.search.Focus()
	m.Update(tea.PasteMsg{Content: "qwen"})
	if m.search.Value() != "qwen" {
		t.Fatal(m.search.Value())
	}
	m.cfg.Start = Recipes
	m.cfg.Preferences.Model = "llama-server -m file.gguf -ngl 999"
	m = New(m.cfg)
	if m.screen() != ClaimCard || strings.Count(m.View().Content, "claim parsing arrives in v0.6") != 1 {
		t.Fatal(m.View().Content)
	}
}

func TestFormPreservesExplicitValues(t *testing.T) {
	m := fixture(t)
	cfg := m.cfg
	cfg.Preferences.Model = "qwen3:8b"
	cfg.Preferences.Tool = "zed"
	cfg.Preferences.Context = 98304
	cfg.Preferences.BudgetUSD = 3.5
	cfg.ASCII = true
	cfg.NoColor = true
	m = New(cfg)
	p := m.cfg.Preferences
	if p.Model != "qwen3:8b" || p.Tool != "zed" || p.Context != 98304 || p.BudgetUSD != 3.5 {
		t.Fatal(p)
	}
	m.busy = true
	m.stack = []Screen{First}
	for _, r := range m.View().Content {
		if r > 127 {
			t.Fatalf("ASCII spinner/frame contains %q", r)
		}
	}
	m.busy = false
	m.stack = []Screen{Recipes}
	m.listing.Rows = nil
	if strings.Contains(m.footer(), "enter") || strings.Contains(m.footer(), "window") {
		t.Fatal(m.footer())
	}
}

func TestFailedPasteReturnsToKeyChoices(t *testing.T) {
	m := fixture(t)
	m.cfg.Authorize = func(context.Context, string) error { return errors.New("private upstream failure") }
	m.stack = []Screen{First}
	m.Update(key("p"))
	m.Update(tea.PasteMsg{Content: "fake-secret"})
	_, cmd := m.Update(key("enter"))
	m.Update(cmd())
	if m.screen() != First || m.password != nil || strings.Contains(m.View().Content, "fake-secret") {
		t.Fatal(m.screen(), m.View().Content)
	}
}

func TestUnparsedClaimNeverShowsCuratedSources(t *testing.T) {
	m := fixture(t)
	m.cfg.Start = Recipes
	m.cfg.Preferences.Model = "llama-server -m pasted.gguf"
	m = New(m.cfg)
	m.Update(key("?"))
	m.Update(key("esc"))
	view := m.View().Content
	if m.screen() != ClaimCard || !strings.Contains(view, "claim parsing arrives in v0.6") || strings.Contains(view, "Strata") || strings.Contains(m.footer(), "sources") {
		t.Fatal(view)
	}
}
