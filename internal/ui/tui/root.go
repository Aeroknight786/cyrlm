package tui

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"github.com/aeroknight786/trialm/internal/core"
	"github.com/aeroknight786/trialm/internal/figure"
	"github.com/aeroknight786/trialm/internal/openrouter"
	"github.com/aeroknight786/trialm/internal/store"
	"github.com/aeroknight786/trialm/internal/ui/window"
	"github.com/charmbracelet/colorprofile"
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
	Process         func(context.Context, string, []string) *exec.Cmd
	WindowArgs      []string
	Headless        bool
	Balance         func(context.Context) (figure.Figure, error)
	Tools           []string
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
type headlessReadyMsg struct {
	login *headlessLogin
	err   error
}
type noticeMsg string
type balanceMsg struct {
	figure figure.Figure
	err    error
}
type childMsg struct {
	kind Screen
	err  error
}
type pulseMsg time.Time
type windowDoneMsg struct{ err error }

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
	labels          []string
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
	chatStop        func() error
	Error           error
	meter           window.Meter
	headless        *headlessLogin
	authURL         string
	balance         figure.Figure
	autoQuality     bool
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
	m := &Root{autoQuality: cfg.Preferences.QualityFloor == "", cfg: cfg, stack: []Screen{start}, width: 80, height: 24, theme: newPalette(cfg.NoColor, cfg.ASCII), spin: spinner.New(spinner.WithSpinner(spinner.Dot)), bar: progress.New(progress.WithWidth(36), progress.WithDefaultBlend(), progress.WithoutPercentage())}
	if cfg.ASCII {
		m.spin = spinner.New(spinner.WithSpinner(spinner.Line))
	}
	m.search = textinput.New()
	m.search.Prompt = "Search models: "
	m.search.CharLimit = 500
	m.history, _ = cfg.Store.History()
	_ = cfg.Store.Read("credits.json", &m.balance)
	m.makeFields()
	if cfg.Start == Recipes && isClaimText(cfg.Preferences.Model) {
		m.replace(ClaimCard)
		m.message = "claim parsing arrives in v0.6"
	}
	return m
}
func Run(cfg Config) error {
	cfg.NoColor = cfg.NoColor || os.Getenv("NO_COLOR") != ""
	cfg.ASCII = cfg.ASCII || asciiTerminal(nativeOS, os.Getenv)
	m := New(cfg)
	opts := append(programOptions(), tea.WithContext(m.cfg.Context))
	_, err := tea.NewProgram(m, opts...).Run()
	return terminalResult(err, m.Error)
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
	if m.cfg.WindowArgs != nil {
		return tea.Batch(m.spin.Tick, m.beginWindow(m.cfg.WindowArgs))
	}
	if m.cfg.DirectArgs != nil {
		if m.cfg.Store.Key() != "" || !needsCloudKey(m.cfg.DirectArgs) {
			return tea.Batch(m.spin.Tick, m.execute(m.cfg.DirectArgs))
		}
		m.replace(First)
	}
	if m.cfg.Detect != nil {
		cmds = append(cmds, func() tea.Msg { d, e := m.cfg.Detect(m.cfg.Context); return detectedMsg{d, e} })
	}
	if m.cfg.UpdateNotice != nil {
		cmds = append(cmds, func() tea.Msg { return noticeMsg(m.cfg.UpdateNotice(m.cfg.Context)) })
	}
	if m.cfg.Balance != nil && m.cfg.Store.Key() != "" {
		cmds = append(cmds, func() tea.Msg { v, e := m.cfg.Balance(m.cfg.Context); return balanceMsg{v, e} })
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
		return m.beginWindow(args)
	}
	if m.cfg.Preferences.Use == "chat" {
		args = append(args, "--use", "chat")
	}
	return m.execute(args)
}
func (m *Root) execute(args []string) tea.Cmd {
	if m.cfg.Preferences.Use == "chat" {
		return m.startChat(args)
	}
	argv := append([]string{"try", "--plain", "--here"}, args...)
	cmd := exec.CommandContext(m.cfg.Context, m.cfg.Executable, argv...)
	if m.cfg.Process != nil {
		cmd = m.cfg.Process(m.cfg.Context, m.cfg.Executable, argv)
	}
	if cmd.Env == nil {
		cmd.Env = childEnv(os.Environ())
	} else {
		cmd.Env = childEnv(cmd.Env)
	}
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
	case tea.ColorProfileMsg:
		if !m.theme.plain && v.Profile >= colorprofile.ANSI256 {
			m.theme = m.theme.rich()
		}
	case tea.WindowSizeMsg:
		m.width = max(20, v.Width)
		m.height = max(8, v.Height)
		m.bar.SetWidth(max(8, min(48, m.width-6)))
		m.chat.resize(m.width, m.height)
		for _, f := range m.fields {
			f.WithWidth(max(12, m.width-24))
		}
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(v)
		return m, cmd
	case detectedMsg:
		m.detection = v.detection
		if m.autoQuality {
			m.cfg.Preferences.QualityFloor = "3bit-ok"
			if v.detection.Target.GPUMemBytes > 24*(1<<30) {
				m.cfg.Preferences.QualityFloor = "4bit+"
			}
			m.makeFields()
		}
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
	case headlessReadyMsg:
		m.busy = false
		if v.err != nil {
			m.message = "Cannot start sign-in. Press p to paste a key."
			return m, nil
		}
		m.headless = v.login
		m.authURL = v.login.URL
		m.push(Paste)
		m.password = huh.NewInput().Title("Authorization code").Value(&m.secret).EchoMode(huh.EchoModePassword)
		m.password.WithWidth(max(24, m.width-4))
		m.password.WithKeyMap(huh.NewDefaultKeyMap())
		m.password.WithTheme(m.formTheme())
		return m, m.password.Focus()
	case authorizedMsg:
		m.busy = false
		m.secret = ""
		m.headless = nil
		m.authURL = ""
		_ = m.cfg.Store.Read("credits.json", &m.balance)
		if v.err != nil {
			if m.screen() == Paste {
				m.back()
				m.password = nil
			}
			m.message = "Could not connect OpenRouter. Press p to paste a key or try again."
			return m, nil
		}
		if m.cfg.DirectArgs != nil {
			m.replace(Trial)
			return m, m.execute(m.cfg.DirectArgs)
		}
		return m, m.afterKey()
	case balanceMsg:
		if v.err == nil {
			m.balance = v.figure
			_ = m.cfg.Store.Write("credits.json", m.balance)
		}
	case noticeMsg:
		m.notice = string(v)
	case windowDoneMsg:
		if v.err != nil {
			m.Error = v.err
			return m, tea.Quit
		}
		return m, func() tea.Msg { return childMsg{kind: Trial} }
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
		if m.screen() == Trial {
			m.meter = window.ReadMeter(m.cfg.Store)
		}
		if m.screen() == Calibrating || m.screen() == Trial {
			return m, tea.Tick(time.Second, func(t time.Time) tea.Msg { return pulseMsg(t) })
		}
	case tea.PasteMsg:
		if m.screen() == Paste && m.busy {
			return m, nil
		}
		if m.screen() == Paste && m.password != nil {
			_, cmd := m.password.Update(v)
			return m, cmd
		}
		if m.screen() == Chat && !m.chat.busy {
			var cmd tea.Cmd
			m.chat.input, cmd = m.chat.input.Update(v)
			return m, cmd
		}
		if m.screen() == Search && !isClaimText(v.Content) {
			var cmd tea.Cmd
			m.search, cmd = m.search.Update(v)
			return m, cmd
		}
		if m.screen() == Recipes || m.screen() == Home || m.screen() == Search {
			m.push(ClaimCard)
			m.message = "claim parsing arrives in v0.6"
			return m, nil
		}
	case chatStartedMsg:
		m.busy = false
		if v.err != nil {
			m.back()
			m.message = v.err.Error()
			return m, nil
		}
		m.chat = newChat(m.cfg.Context, v.client)
		m.chatStop = v.stop
		m.replace(Chat)
		m.chat.resize(m.width, m.height)
		return m, nil
	case chatResponseMsg:
		return m, m.chat.receive(v)
	case tea.KeyPressMsg:
		key := v.String()
		if key == "ctrl+c" {
			if m.headless != nil {
				m.headless.close()
				m.headless = nil
			}
			if m.calibration != nil {
				m.calibration.cancel()
			}
			if m.chatStop != nil {
				stop := m.chatStop
				m.chatStop = nil
				m.Error = context.Canceled
				return m, tea.Sequence(func() tea.Msg { _ = stop(); return nil }, tea.Interrupt)
			}
			m.Error = context.Canceled
			return m, tea.Interrupt
		}
		if m.screen() == Chat {
			if key == "esc" {
				return m, m.finishChat()
			}
			return m, m.chat.update(v)
		}
		if m.screen() == Paste && m.busy {
			return m, nil
		}
		if m.screen() == Paste {
			if key == "esc" {
				if m.headless != nil {
					m.headless.close()
					m.headless = nil
				}
				m.authURL = ""
				m.secret = ""
				m.back()
				return m, nil
			}
			if key == "enter" {
				if m.headless != nil {
					login := m.headless
					secret := m.secret
					m.busy = true
					return m, func() tea.Msg { return authorizedMsg{login.finish(m.cfg.Context, secret)} }
				}
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
				if isClaimText(m.search.Value()) {
					m.replace(ClaimCard)
					m.message = "claim parsing arrives in v0.6"
					return m, nil
				}
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
				m.password.WithTheme(m.formTheme())
				m.password.WithWidth(max(24, m.width-4))
				m.password.WithKeyMap(huh.NewDefaultKeyMap())
				return m, m.password.Focus()
			case "s":
				m.cfg.DirectArgs = nil
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
				m.fields[m.field].Blur()
				m.field = (m.field + len(m.fields) - 1) % len(m.fields)
			case "down", "j", "tab":
				m.fields[m.field].Blur()
				m.field = (m.field + 1) % len(m.fields)
			case "left", "h", "right", "l":
				_, cmd := m.fields[m.field].Update(changeKey(v))
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
	if m.cfg.Headless && m.screen() == First && secret == "" {
		m.busy = true
		return func() tea.Msg {
			h, e := beginHeadless(m.cfg.Store, openrouter.Client{}, "")
			return headlessReadyMsg{h, e}
		}
	}
	if m.cfg.Authorize == nil {
		return nil
	}
	m.busy = true
	return func() tea.Msg { return authorizedMsg{m.cfg.Authorize(m.cfg.Context, secret)} }
}
func (m *Root) makeFields() {
	m.fields = nil
	m.labels = nil
	p := &m.cfg.Preferences
	add := func(title string, value *string, options ...string) {
		opts := make([]huh.Option[string], len(options))
		for i, o := range options {
			opts[i] = huh.NewOption(o, o)
		}
		f := huh.NewSelect[string]().Options(opts...).Value(value).Inline(true)
		f.WithKeyMap(huh.NewDefaultKeyMap())
		f.WithWidth(32)
		m.labels = append(m.labels, title)
		f.WithTheme(m.formTheme())
		f.Focus()
		m.fields = append(m.fields, f)
	}
	add("Tool", &p.Tool, "codex", "claude-code", "opencode", "aider", "qwen-code", "cline", "continue", "zed", "generic")
	add("Use", &p.Use, "agent", "chat")
	models := []string{"any", "qwen3.6", "gpt-oss"}
	if p.Model != "" && p.Model != "any" && p.Model != "qwen3.6" && p.Model != "gpt-oss" {
		models = append(models, p.Model)
	}
	add("Model", &p.Model, models...)
	add("Priority", &p.Priority, "speed", "balanced", "quality", "long")
	ctxOptions := []huh.Option[int]{huh.NewOption("48k", 49152), huh.NewOption("64k", 65536), huh.NewOption("128k", 131072), huh.NewOption("256k", 262144)}
	if p.Context != 0 && p.Context != 49152 && p.Context != 65536 && p.Context != 131072 && p.Context != 262144 {
		ctxOptions = append(ctxOptions, huh.NewOption(strconv.Itoa(p.Context)+" tokens", p.Context))
	}
	ctx := huh.NewSelect[int]().Options(ctxOptions...).Value(&p.Context).Inline(true)
	ctx.WithKeyMap(huh.NewDefaultKeyMap())
	ctx.WithWidth(32)
	m.labels = append(m.labels, "Context (native)")
	ctx.WithTheme(m.formTheme())
	ctx.Focus()
	m.fields = append(m.fields, ctx)
	add("Quality", &p.QualityFloor, "3bit-ok", "4bit+", "8bit")
	add("Engines", &p.Engines, "stable+new", "stable", "installed")
	add("Speed boosts", &p.Boosts, "auto", "off", "everything")
	add("Trial", &p.Privacy, "repo", "playground", "predict-only")
	budgetOptions := []huh.Option[float64]{huh.NewOption("$1", 1.), huh.NewOption("$2", 2.), huh.NewOption("$5", 5.), huh.NewOption("$10", 10.)}
	if p.BudgetUSD != 1 && p.BudgetUSD != 2 && p.BudgetUSD != 5 && p.BudgetUSD != 10 {
		budgetOptions = append(budgetOptions, huh.NewOption(figure.Exact(p.BudgetUSD, "USD", figure.Measured, "prefs:budget").String(), p.BudgetUSD))
	}
	budget := huh.NewSelect[float64]().Options(budgetOptions...).Value(&p.BudgetUSD).Inline(true)
	budget.WithKeyMap(huh.NewDefaultKeyMap())
	budget.WithWidth(32)
	budget.WithTheme(m.formTheme())
	budget.Focus()
	m.fields = append(m.fields, budget)
	m.labels = append(m.labels, "Trial cap")
	retention := huh.NewSelect[bool]().Options(huh.NewOption("no-logging", false), huh.NewOption("ZDR only", true)).Value(&p.ZDR).Inline(true)
	retention.WithKeyMap(huh.NewDefaultKeyMap())
	retention.WithWidth(32)
	retention.WithTheme(m.formTheme())
	retention.Focus()
	m.fields = append(m.fields, retention)
	m.labels = append(m.labels, "Retention")

}
func (m *Root) footer() string {
	if m.busy || (m.screen() == Chat && m.chat.busy) {
		return "ctrl-c stop"
	}
	if m.screen() == Recipes && len(m.listing.Rows) == 0 {
		return "r needs  / search  q quit"
	}
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
	if m.busy && m.screen() != Calibrating && m.screen() != Trial {
		body += "\n" + m.spin.View() + " Working…"
	}
	if m.message != "" {
		body += "\n" + m.theme.caveat.Render(m.message)
	}
	body += "\n\n" + m.helpView()
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
		if m.screen() == Recipes || m.screen() == Needs || m.screen() == Chat {
			lines[i] = clip(l, m.width)
		} else {
			lines[i] = wrap(l, m.width)
		}
	}
	return strings.Join(lines, "\n")
}

var _ tea.Model = (*Root)(nil)

func changeKey(v tea.KeyPressMsg) tea.KeyPressMsg {
	// huh supports arrows; translate the shared h/l bindings at the root.
	if v.String() == "h" {
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	}
	if v.String() == "l" {
		return tea.KeyPressMsg{Code: tea.KeyRight}
	}
	return v
}

func (m *Root) beginWindow(args []string) tea.Cmd {
	m.busy = true
	return tea.Batch(func() tea.Msg { return windowDoneMsg{m.cfg.Window(m.cfg.Context, args)} }, tea.Tick(time.Second, func(t time.Time) tea.Msg { return pulseMsg(t) }))
}

func needsCloudKey(args []string) bool {
	for i, a := range args {
		if a == "--upstream" && i+1 < len(args) {
			return args[i+1] == "auto" || args[i+1] == "openrouter"
		}
		if strings.HasPrefix(a, "--upstream=") {
			return a == "--upstream=auto" || a == "--upstream=openrouter"
		}
	}
	return true
}

func terminalResult(err, modelError error) error {
	if errors.Is(err, tea.ErrInterrupted) {
		return context.Canceled
	}
	if err != nil {
		return err
	}
	return modelError
}

func isClaimText(s string) bool { return strings.Contains(s, " -") || strings.Contains(s, "://") }
