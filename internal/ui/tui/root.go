package tui

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"github.com/aeroknight786/trialm/internal/core"
	"github.com/aeroknight786/trialm/internal/store"
)

type Screen string

const (
	First       Screen = "first"
	Paste       Screen = "paste"
	Calibration Screen = "calibration"
	Calibrating Screen = "calibrating"
	Needs       Screen = "needs"
	Recipes     Screen = "recipes"
	Details     Screen = "details"
	Trial       Screen = "trial"
	Summary     Screen = "summary"
	Home        Screen = "home"
	Gallery     Screen = "gallery"
	ClaimCard   Screen = "claim"
	Search      Screen = "search"
	Why         Screen = "why"
	Setup       Screen = "setup"
	Chat        Screen = "chat"
)

// Config uses application callbacks so no Charm types escape into the CLI/core.
type Config struct {
	Context         context.Context
	Store           store.Store
	Preferences     store.Preferences
	Detect          func(context.Context) (core.Detection, error)
	Find            func(context.Context, store.Preferences) (core.Listing, error)
	Authorize       func(context.Context, string) error
	TrialArgs       func(core.Row, store.Preferences) []string
	Window          func(context.Context, []string) error
	UpdateNotice    func(context.Context) string
	Executable      string
	Start           Screen
	NoColor, ASCII  bool
	DirectArgs      []string
	AutomaticWindow bool
}
type detectedMsg struct {
	detection core.Detection
	err       error
}
type recipesMsg struct {
	listing core.Listing
	err     error
}
type authorizedMsg struct{ err error }
type noticeMsg string
type childMsg struct {
	kind Screen
	err  error
}
type pulseMsg time.Time

// Root owns every screen and sub-model for the entire interactive session.
type Root struct {
	cfg             Config
	stack           []Screen
	width, height   int
	theme           palette
	detection       core.Detection
	listing         core.Listing
	selected, field int
	fields          []huh.Field
	secret          string
	password        *huh.Input
	search          textinput.Model
	spin            spinner.Model
	bar             progress.Model
	busy            bool
	message, notice string
	history         []store.TrialEntry
	last            store.TrialEntry
	started         time.Time
	calibration     *calibrationJob
	chat            chatModel
	Error           error
}

func New(cfg Config) *Root {
	if cfg.Context == nil {
		cfg.Context = context.Background()
	}
	if cfg.Executable == "" {
		cfg.Executable, _ = os.Executable()
	}
	start := cfg.Start
	if start == "" {
		start = First
	}
	m := &Root{cfg: cfg, stack: []Screen{start}, width: 80, height: 24, theme: newPalette(cfg.NoColor, cfg.ASCII), spin: spinner.New(spinner.WithSpinner(spinner.Dot)), bar: progress.New(progress.WithWidth(36), progress.WithDefaultBlend(), progress.WithoutPercentage())}
	m.search = textinput.New()
	m.search.Prompt = "Search models: "
	m.search.CharLimit = 500
	m.history, _ = cfg.Store.History()
	m.makeFields()
	return m
}
func Run(cfg Config) error {
	cfg.NoColor = cfg.NoColor || os.Getenv("NO_COLOR") != ""
	cfg.ASCII = cfg.ASCII || os.Getenv("TERM") == "dumb"
	m := New(cfg)
	_, err := tea.NewProgram(m, programOptions()...).Run()
	if err != nil {
		return err
	}
	return m.Error
}
func (m *Root) screen() Screen   { return m.stack[len(m.stack)-1] }
func (m *Root) push(s Screen)    { m.stack = append(m.stack, s); m.message = "" }
func (m *Root) replace(s Screen) { m.stack[len(m.stack)-1] = s; m.message = "" }
func (m *Root) back() {
	if len(m.stack) > 1 {
		m.stack = m.stack[:len(m.stack)-1]
	}
	m.message = ""
}
func (m *Root) Init() tea.Cmd {
	cmds := []tea.Cmd{m.spin.Tick}
	if m.cfg.DirectArgs != nil {
		return tea.Batch(m.spin.Tick, m.execute(m.cfg.DirectArgs))
	}
	if m.cfg.Detect != nil {
		cmds = append(cmds, func() tea.Msg { d, e := m.cfg.Detect(m.cfg.Context); return detectedMsg{d, e} })
	}
	if m.cfg.UpdateNotice != nil {
		cmds = append(cmds, func() tea.Msg { return noticeMsg(m.cfg.UpdateNotice(m.cfg.Context)) })
	}
	return tea.Batch(cmds...)
}
func (m *Root) find() tea.Cmd {
	if m.cfg.Find == nil {
		return nil
	}
	m.busy = true
	p := m.cfg.Preferences
	return func() tea.Msg { l, e := m.cfg.Find(m.cfg.Context, p); return recipesMsg{l, e} }
}
func (m *Root) afterKey() tea.Cmd {
	var p map[string]json.RawMessage
	_ = m.cfg.Store.Read("prefs.json", &p)
	if _, ok := p["calibration_offer"]; !ok {
		m.replace(Calibration)
	} else {
		m.replace(Needs)
	}
	return nil
}
func (m *Root) choice(choice string) error {
	p := map[string]json.RawMessage{}
	if e := m.cfg.Store.Read("prefs.json", &p); e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	p["calibration_offer"], _ = json.Marshal(choice)
	return m.cfg.Store.Write("prefs.json", p)
}
func (m *Root) row() (core.Row, bool) {
	if len(m.listing.Rows) == 0 {
		return core.Row{}, false
	}
	m.selected = max(0, min(m.selected, len(m.listing.Rows)-1))
	return m.listing.Rows[m.selected], true
}
func (m *Root) try(window bool) tea.Cmd {
	window = window || m.cfg.AutomaticWindow
	row, ok := m.row()
	if !ok {
		return nil
	}
	if m.cfg.Store.Key() == "" {
		m.push(First)
		m.message = "Connect OpenRouter to start a trial."
		return nil
	}
	if m.cfg.Preferences.Privacy == "predict-only" {
		m.message = "Predict only. Change Trial in needs to start a cloud trial."
		return nil
	}
	args := m.cfg.TrialArgs(row, m.cfg.Preferences)
	m.push(Trial)
	m.started = time.Now()
	if window {
		m.busy = true
		return func() tea.Msg { return childMsg{Trial, m.cfg.Window(m.cfg.Context, args)} }
	}
	if m.cfg.Preferences.Use == "chat" {
		args = append(args, "--use", "chat")
	}
	return m.execute(args)
}
func (m *Root) execute(args []string) tea.Cmd {
	cmd := exec.CommandContext(m.cfg.Context, m.cfg.Executable, append([]string{"try", "--plain", "--here"}, args...)...)
	cmd.Env = childEnv(os.Environ())
	return tea.ExecProcess(cmd, func(e error) tea.Msg { return childMsg{kind: Trial, err: e} })
}

func childEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, e := range env {
		if !strings.HasPrefix(e, "CLAUDECODE=") && !strings.HasPrefix(e, "CLAUDE_CODE_ENTRYPOINT=") {
			out = append(out, e)
		}
	}
	return out
}
func (m *Root) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = max(20, v.Width)
		m.height = max(8, v.Height)
		m.bar.SetWidth(max(8, min(48, m.width-6)))
		m.chat.resize(m.width, m.height)
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(v)
		return m, cmd
	case detectedMsg:
		m.detection = v.detection
		if v.err != nil {
			m.message = v.err.Error()
			return m, nil
		}
		if m.screen() == First && m.cfg.Store.Key() != "" {
			var prefs map[string]json.RawMessage
			_ = m.cfg.Store.Read("prefs.json", &prefs)
			if _, ok := prefs["tool"]; ok {
				m.replace(Home)
				return m, m.find()
			}
			return m, m.afterKey()
		}
		if m.screen() == Recipes || m.screen() == Home {
			return m, m.find()
		}
	case recipesMsg:
		m.busy = false
		if v.err != nil {
			m.message = v.err.Error()
			return m, nil
		}
		m.listing = v.listing
		m.selected = 0
	case authorizedMsg:
		m.busy = false
		m.secret = ""
		if v.err != nil {
			m.message = "Could not connect OpenRouter. Press p to paste a key or try again."
			return m, nil
		}
		return m, m.afterKey()
	case noticeMsg:
		m.notice = string(v)
	case childMsg:
		m.busy = false
		if v.err != nil {
			m.message = v.err.Error()
			m.back()
			m.message = v.err.Error()
			return m, nil
		}
		if v.kind == Trial {
			m.history, _ = m.cfg.Store.History()
			if len(m.history) > 0 {
				m.last = m.history[len(m.history)-1]
				m.replace(Summary)
			} else {
				m.back()
				m.message = "Trial finished. No requests were recorded."
			}
		}
	case calibrationEventMsg:
		if v.done {
			m.busy = false
			m.calibration = nil
			m.replace(Needs)
			if v.err != nil {
				m.message = "Calibration did not finish: " + v.err.Error()
			}
			return m, nil
		}
		m.message = v.message
		m.calibration.record(v.event)
		return m, m.calibration.next()
	case pulseMsg:
		if m.screen() == Calibrating {
			return m, tea.Tick(time.Second, func(t time.Time) tea.Msg { return pulseMsg(t) })
		}
	case tea.PasteMsg:
		if m.screen() == Recipes || m.screen() == Home || m.screen() == Search {
			m.push(ClaimCard)
			m.message = "claim parsing arrives in v0.6"
			return m, nil
		}
	case chatResponseMsg:
		return m, m.chat.receive(v)
	case tea.KeyPressMsg:
		key := v.String()
		if key == "ctrl+c" {
			if m.calibration != nil {
				m.calibration.cancel()
			}
			m.Error = context.Canceled
			return m, tea.Interrupt
		}
		if m.screen() == Chat {
			if key == "esc" {
				return m, tea.Quit
			}
			return m, m.chat.update(v)
		}
		if m.screen() == Paste {
			if key == "esc" {
				m.secret = ""
				m.back()
				return m, nil
			}
			if key == "enter" {
				return m, m.authorize(m.secret)
			}
			_, cmd := m.password.Update(msg)
			return m, cmd
		}
		if m.screen() == Search {
			if key == "esc" {
				m.back()
				return m, nil
			}
			if key == "enter" {
				m.cfg.Preferences.Model = m.search.Value()
				m.replace(Recipes)
				return m, m.find()
			}
			var c tea.Cmd
			m.search, c = m.search.Update(msg)
			return m, c
		}
		if key == "q" {
			return m, tea.Quit
		}
		if key == "esc" {
			if !m.busy {
				m.back()
			}
			return m, nil
		}
		if m.busy {
			return m, nil
		}
		switch m.screen() {
		case First:
			switch key {
			case "enter":
				return m, m.authorize("")
			case "p":
				m.push(Paste)
				m.password = huh.NewInput().Title("OpenRouter key").Value(&m.secret).EchoMode(huh.EchoModePassword)
				m.password.WithTheme(huh.ThemeFunc(func(bool) *huh.Styles { return huh.ThemeBase(true) }))
				return m, m.password.Focus()
			case "s":
				return m, m.afterKey()
			}
		case Calibration:
			if key == "s" {
				if e := m.choice("skipped"); e != nil {
					m.message = e.Error()
					return m, nil
				}
				m.replace(Needs)
			}
			if key == "enter" {
				if e := m.choice("accepted"); e != nil {
					m.message = e.Error()
					return m, nil
				}
				m.replace(Calibrating)
				m.busy = true
				m.started = time.Now()
				m.calibration = newCalibration(m.cfg.Context, m.cfg.Executable)
				return m, tea.Batch(m.calibration.next(), tea.Tick(time.Second, func(t time.Time) tea.Msg { return pulseMsg(t) }))
			}
		case Needs:
			switch key {
			case "enter":
				if e := m.cfg.Store.SavePreferences(m.cfg.Preferences); e != nil {
					m.message = e.Error()
					return m, nil
				}
				m.push(Recipes)
				return m, m.find()
			case "up", "k":
				m.field = (m.field + len(m.fields) - 1) % len(m.fields)
			case "down", "j", "tab":
				m.field = (m.field + 1) % len(m.fields)
			case "left", "h", "right", "l":
				_, cmd := m.fields[m.field].Update(v)
				return m, cmd
			}
		case Home:
			switch key {
			case "f", "enter":
				m.push(Needs)
			case "c":
				m.push(Gallery)
			case "t":
				if len(m.history) > 0 {
					m.last = m.history[len(m.history)-1]
					m.push(Summary)
				}
			case "/":
				return m, m.openSearch()
			}
		case Recipes, Details:
			switch key {
			case "down", "j":
				m.selected = min(m.selected+1, len(m.listing.Rows)-1)
			case "up", "k":
				m.selected = max(0, m.selected-1)
			case "enter":
				return m, m.try(false)
			case "w":
				return m, m.try(true)
			case "d":
				if m.screen() == Recipes {
					m.push(Details)
				}
			case "s":
				m.push(Setup)
			case "r":
				m.push(Needs)
			case "/":
				return m, m.openSearch()
			case "?":
				m.push(Why)
			case " ", "space":
				if m.screen() == Details {
					if m.cfg.Preferences.Boosts == "off" {
						m.cfg.Preferences.Boosts = "auto"
					} else {
						m.cfg.Preferences.Boosts = "off"
					}
					return m, m.find()
				}
			case "c":
				m.push(Gallery)
			}
		case Summary:
			switch key {
			case "1", "2", "3":
				m.last.Rating = int(key[0] - '0')
				if e := m.cfg.Store.SaveTrial(m.last); e != nil {
					m.message = e.Error()
					return m, nil
				}
				if key == "1" {
					m.cfg.Preferences.Priority = store.TowardSpeed(m.cfg.Preferences.Priority)
					if e := m.cfg.Store.SavePreferences(m.cfg.Preferences); e != nil {
						m.message = e.Error()
						return m, nil
					}
					m.replace(Recipes)
					return m, m.find()
				}
				m.message = "Rating saved."
			case "t":
				m.replace(Recipes)
				return m, m.find()
			case "s":
				m.push(Setup)
			}
		case Gallery:
			if key == "down" || key == "j" {
				m.selected = min(m.selected+1, len(Claims())-1)
			}
			if key == "up" || key == "k" {
				m.selected = max(0, m.selected-1)
			}
			if key == "enter" || key == "?" {
				m.push(ClaimCard)
			}
		case ClaimCard:
			if key == "?" {
				m.push(Why)
			}
		}
	}
	return m, nil
}
func (m *Root) openSearch() tea.Cmd { m.push(Search); return m.search.Focus() }
func (m *Root) authorize(secret string) tea.Cmd {
	if m.cfg.Authorize == nil {
		return nil
	}
	m.busy = true
	return func() tea.Msg { return authorizedMsg{m.cfg.Authorize(m.cfg.Context, secret)} }
}
func (m *Root) makeFields() {
	p := &m.cfg.Preferences
	add := func(title string, value *string, options ...string) {
		opts := make([]huh.Option[string], len(options))
		for i, o := range options {
			opts[i] = huh.NewOption(o, o)
		}
		f := huh.NewSelect[string]().Title(title).Options(opts...).Value(value).Inline(true)
		f.WithTheme(huh.ThemeFunc(func(bool) *huh.Styles { return huh.ThemeBase(true) }))
		f.Focus()
		m.fields = append(m.fields, f)
	}
	add("Tool", &p.Tool, "codex", "claude-code", "opencode", "aider", "qwen-code", "cline", "continue", "generic")
	add("Use", &p.Use, "agent", "chat")
	add("Model", &p.Model, "any", "qwen3.6", "gpt-oss")
	add("Priority", &p.Priority, "speed", "balanced", "quality", "long")
	ctx := huh.NewSelect[int]().Title("Context (native)").Options(huh.NewOption("48k", 49152), huh.NewOption("64k", 65536), huh.NewOption("128k", 131072), huh.NewOption("256k", 262144)).Value(&p.Context).Inline(true)
	ctx.WithTheme(huh.ThemeFunc(func(bool) *huh.Styles { return huh.ThemeBase(true) }))
	ctx.Focus()
	m.fields = append(m.fields, ctx)
	add("Quality", &p.QualityFloor, "3bit-ok", "4bit+")
	add("Engines", &p.Engines, "stable+new", "stable", "installed")
	add("Speed boosts", &p.Boosts, "auto", "off", "everything")
	add("Trial", &p.Privacy, "repo", "playground", "predict-only")
}
func (m *Root) footer() string {
	switch m.screen() {
	case First:
		return "enter browser  p paste  s skip  q quit"
	case Paste:
		return "enter connect  esc back"
	case Calibration:
		return "enter calibrate  s skip  q quit"
	case Calibrating:
		return "ctrl-c stop"
	case Needs:
		return "↑↓ row  ←→ change  enter go  esc back  q quit"
	case Recipes:
		return "enter try  s set up  d details  w window  r needs  / search"
	case Details:
		return "enter try  space boosts  s set up  ? why  esc back  q quit"
	case Home:
		return "enter find  f needs  t history  c claims  / search  q quit"
	case Summary:
		return "1 too slow  2 fine  3 great  s set up  t another  q quit"
	case Gallery:
		return "↑↓ move  enter claim  esc back  q quit"
	case ClaimCard:
		return "? sources  esc back  q quit"
	case Search:
		return "enter search  esc back"
	case Trial:
		return "ctrl-c stop"
	case Chat:
		return "enter send  esc finish  ctrl-c stop"
	default:
		return "esc back  q quit"
	}
}
func (m *Root) View() tea.View {
	body := m.body()
	if m.busy && m.screen() != Calibrating {
		body += "\n" + m.spin.View() + " Working…"
	}
	if m.message != "" {
		body += "\n" + m.theme.caveat.Render(m.message)
	}
	body += "\n\n" + m.theme.dim.Render(m.footer())
	if m.theme.plain {
		body = strip(body)
	}
	return tea.NewView(m.fit(m.theme.glyph(body)))
}
func (m *Root) fit(s string) string {
	// All cells are composed before clipping; terminal controls and pasted secrets
	// never enter this view. ANSI-aware clipping keeps the renderer at the margin.
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = clip(l, m.width)
	}
	return strings.Join(lines, "\n")
}

var _ tea.Model = (*Root)(nil)
