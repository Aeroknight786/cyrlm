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
	"github.com/aeroknight786/trialm/internal/setup"
	"github.com/aeroknight786/trialm/internal/store"
	"github.com/aeroknight786/trialm/internal/ui/window"
	"github.com/charmbracelet/colorprofile"
)

type Screen string

const (
	First     Screen = "key"
	Paste     Screen = "paste"
	Needs     Screen = "needs"
	Recipes   Screen = "recipes"
	Details   Screen = "details"
	Trial     Screen = "trial"
	Summary   Screen = "summary"
	Home      Screen = "launcher"
	Help      Screen = "help"
	Gallery   Screen = "gallery"
	ClaimCard Screen = "claim"
	Search    Screen = "search"
	Why       Screen = "why"
	Setup     Screen = "setup"
	Chat      Screen = "chat"
	Ready     Screen = "ready"
	Launching Screen = "launch"
)

// Config uses application callbacks so no Charm types escape into the CLI/core.
type Config struct {
	LocalPlan       *setup.Plan
	Plan            func(context.Context, core.Row, store.Preferences) (setup.Plan, error)
	Apply           func(context.Context, setup.Plan, core.Sink) (setup.Journal, error)
	Models          func(context.Context, core.Detection, store.Preferences) ([]core.ModelChoice, error)
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
	Tools           []string
}
type modelsMsg struct {
	rows []core.ModelChoice
	err  error
}
type sparkleMsg int
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
type childMsg struct {
	kind Screen
	err  error
}
type pulseMsg time.Time
type windowDoneMsg struct{ err error }

// Root owns every screen and sub-model for the entire interactive session.
type Root struct {
	cfg                                              Config
	setup                                            setupView
	models                                           []core.ModelChoice
	modelIndex                                       int
	detecting, detected, offer, expanded, autoLaunch bool
	sparkle                                          int
	rawError                                         string
	pendingTrial                                     bool
	stack                                            []Screen
	width, height                                    int
	theme                                            palette
	detection                                        core.Detection
	listing                                          core.Listing
	selected, field                                  int
	fields                                           []huh.Field
	labels                                           []string
	secret                                           string
	password                                         *huh.Input
	search                                           textinput.Model
	spin                                             spinner.Model
	bar                                              progress.Model
	busy                                             bool
	message, notice                                  string
	history                                          []store.TrialEntry
	last                                             store.TrialEntry
	started                                          time.Time
	calibration                                      *calibrationJob
	chat                                             chatModel
	chatStop                                         func() error
	Error                                            error
	meter                                            window.Meter
	headless                                         *headlessLogin
	authURL                                          string
	autoQuality                                      bool
	pendingClaim                                     bool
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
		start = Home
	}
	m := &Root{autoQuality: cfg.Preferences.QualityFloor == "", cfg: cfg, stack: []Screen{start}, width: 80, height: 24, theme: newPalette(cfg.NoColor, cfg.ASCII), spin: spinner.New(spinner.WithSpinner(spinner.Dot)), bar: progress.New(progress.WithWidth(36), progress.WithDefaultBlend(), progress.WithoutPercentage())}
	var saved map[string]json.RawMessage
	_ = cfg.Store.Read("prefs.json", &saved)
	_, offered := saved["calibration_offer"]
	m.offer = !offered
	if m.offer {
		m.modelIndex = -1
	}
	m.detecting = start == Home
	if cfg.ASCII {
		m.spin = spinner.New(spinner.WithSpinner(spinner.Line))
	}
	m.search = textinput.New()
	m.search.Prompt = "Search models: "
	m.search.CharLimit = 500
	m.history, _ = cfg.Store.History()
	m.makeFields()
	if cfg.LocalPlan != nil {
		m.setup = setupView{plan: *cfg.LocalPlan, states: map[int]string{}}
	}
	if cfg.Start == Recipes && isClaimText(cfg.Preferences.Model) {
		m.replace(ClaimCard)
		m.pendingClaim = true
	}
	return m
}
func Run(cfg Config) error {
	cfg.NoColor = cfg.NoColor || os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb"
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
	cmds := []tea.Cmd{m.tick()}
	if m.cfg.WindowArgs != nil {
		return tea.Batch(m.tick(), m.beginWindow(m.cfg.WindowArgs))
	}
	if m.cfg.DirectArgs != nil {
		if m.cfg.Store.Key() != "" || !core.NeedsCloudKey(m.cfg.DirectArgs) {
			return tea.Batch(m.tick(), m.execute(m.cfg.DirectArgs))
		}
		m.replace(First)
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
	if m.screen() == Paste {
		m.back()
	}
	m.back()
	if m.pendingTrial {
		m.pendingTrial = false
		return m.try(false)
	}
	return nil
}
func (m *Root) loadModels() tea.Cmd {
	if m.cfg.Models == nil {
		m.detecting = false
		return nil
	}
	m.detecting = true
	d, p := m.detection, m.cfg.Preferences
	return func() tea.Msg { rows, e := m.cfg.Models(m.cfg.Context, d, p); return modelsMsg{rows, e} }
}
func (m *Root) tick() tea.Cmd {
	if m.theme.plain || m.theme.ascii {
		return nil
	}
	return m.spin.Tick
}
func (m *Root) fail(sentence string, err error) {
	m.message = sentence
	if err != nil {
		m.rawError = err.Error()
	}
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
	if m.screen() == Home {
		if m.modelIndex < 0 || m.modelIndex >= len(m.models) {
			return core.Row{}, false
		}
		return m.models[m.modelIndex].Row, true
	}
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
		m.pendingTrial = true
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
	cmd := m.child(argv)
	return tea.ExecProcess(cmd, func(e error) tea.Msg { return childMsg{kind: Trial, err: e} })
}
func (m *Root) child(argv []string) *exec.Cmd {
	cmd := exec.CommandContext(m.cfg.Context, m.cfg.Executable, argv...)
	if m.cfg.Process != nil {
		cmd = m.cfg.Process(m.cfg.Context, m.cfg.Executable, argv)
	}
	if cmd.Env == nil {
		cmd.Env = core.ChildEnv(os.Environ())
	} else {
		cmd.Env = core.ChildEnv(cmd.Env)
	}
	return cmd
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
	case progress.FrameMsg:
		var cmd tea.Cmd
		bar, cmd := m.bar.Update(v)
		m.bar = bar
		return m, cmd
	case sparkleMsg:
		m.sparkle = int(v)
		if m.theme.plain || m.theme.ascii {
			m.sparkle = 3
		}
		if m.sparkle < 3 {
			return m, tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg { return sparkleMsg(int(v) + 1) })
		}
		if m.autoLaunch && m.screen() == Ready {
			return m, m.launchSetup(m.setup.journal)
		}
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(v)
		return m, cmd
	case setupEvent:
		return m, m.setupEvent(v)
	case modelsMsg:
		m.detecting = false
		if v.err != nil {
			m.fail("Could not load your models. Press f to find a recipe or d for details.", v.err)
		} else {
			m.models = v.rows
			m.modelIndex = min(m.modelIndex, len(m.models))
			if !m.offer {
				m.modelIndex = max(0, m.modelIndex)
			}
		}
	case detectedMsg:
		m.detection = v.detection
		m.detected = true
		if m.autoQuality {
			m.cfg.Preferences.QualityFloor = "3bit-ok"
			if v.detection.Target.GPUMemBytes > 24*(1<<30) {
				m.cfg.Preferences.QualityFloor = "4bit+"
			}
			m.makeFields()
		}
		if v.err != nil {
			m.detecting = false
			m.fail("Could not read this machine. Press f to find a recipe or d for details.", v.err)
			return m, nil
		}
		if m.screen() == Home {
			return m, m.loadModels()
		}
		if m.screen() == Recipes {
			return m, m.find()
		}
	case recipesMsg:
		m.busy = false
		if v.err != nil {
			m.fail("That did not finish. Press esc to go back or d for details.", v.err)
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
		if (v.kind == Launching || v.kind == Setup) && v.err == nil {
			m.stack = []Screen{Home}
			m.autoLaunch = false
			return m, m.loadModels()
		}
		if v.err != nil {
			m.back()
			m.fail("That did not finish. Press esc to go back or d for details.", v.err)
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
		if m.calibration == nil {
			return m, nil // stopped; the job is being drained
		}
		if v.done {
			m.busy = false
			m.calibration = nil
			if v.err != nil {
				m.fail("Calibration stopped. Your models are ready to choose. Press d for details.", v.err)
			}
			return m, m.loadModels()
		}
		m.message = core.CalibrationStatus(v.event.Probe, v.message)
		m.calibration.record(v.event)
		return m, tea.Batch(m.calibration.next(), m.bar.SetPercent(m.calibration.fraction))
	case pulseMsg:
		if m.screen() == Trial {
			m.meter = window.ReadMeter(m.cfg.Store)
		}
		if m.calibration != nil || m.screen() == Trial {
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
			m.pendingClaim = true
			return m, nil
		}
	case chatStartedMsg:
		m.busy = false
		if v.err != nil {
			m.back()
			m.fail("That did not finish. Press esc to go back or d for details.", v.err)
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
			if m.setup.cancel != nil {
				m.setup.cancel()
				m.message = "Pausing setup…"
				return m, nil
			}
			if m.headless != nil {
				m.headless.close()
				m.headless = nil
			}
			if m.calibration != nil {
				job := m.calibration
				m.calibration = nil
				m.Error = context.Canceled
				return m, tea.Sequence(func() tea.Msg { job.stop(); return nil }, tea.Interrupt)
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
					m.pendingClaim = true
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
		if key == "d" && (m.screen() == Home || m.screen() == Setup || m.screen() == Ready || m.rawError != "") {
			m.expanded = !m.expanded
			return m, nil
		}
		if key == "?" && m.screen() == Home {
			m.push(Help)
			return m, nil
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
			case "s", "enter":
				if m.offer {
					choice := "skipped"
					if key == "enter" {
						choice = "accepted"
					}
					if e := m.choice(choice); e != nil {
						m.fail("Could not save that choice. Press s or enter to retry.", e)
						return m, nil
					}
					m.offer = false
					m.modelIndex = max(0, m.modelIndex)
					if key == "enter" {
						m.busy = true
						m.started = time.Now()
						m.calibration = newCalibration(m.cfg.Context, m.cfg.Executable)
						return m, tea.Batch(m.calibration.next(), tea.Tick(time.Second, func(t time.Time) tea.Msg { return pulseMsg(t) }))
					}
					return m, nil
				}
				if key == "s" || m.detecting {
					return m, nil
				}
				if m.modelIndex >= len(m.models) {
					m.push(Recipes)
					return m, m.find()
				}
				choice := m.models[m.modelIndex]
				if choice.Journal != nil {
					return m, m.launchSetup(*choice.Journal)
				}
				m.autoLaunch = true
				return m, m.planSetup()
			case "up", "k":
				low := 0
				if m.offer {
					low = -1
				}
				m.modelIndex = max(low, m.modelIndex-1)
			case "down", "j":
				m.modelIndex = min(len(m.models), m.modelIndex+1)
			case "tab":
				for i, tool := range m.cfg.Tools {
					if tool == m.cfg.Preferences.Tool {
						m.cfg.Preferences.Tool = m.cfg.Tools[(i+1)%len(m.cfg.Tools)]
						return m, nil
					}
				}
				if len(m.cfg.Tools) > 0 {
					m.cfg.Preferences.Tool = m.cfg.Tools[0]
				}
			case "t":
				return m, m.try(false)
			case "f":
				m.push(Recipes)
				return m, m.find()
			case "/":
				return m, m.openSearch()
			case "c":
				m.push(Gallery)
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
				return m, m.planSetup()
			case "n", "r":
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
				return m, m.planSetup()
			}
		case Setup:
			if (key == "enter" || key == "y" || key == "Y") && m.setup.plan.ID != "" && m.cfg.Apply != nil {
				return m, m.applySetup()
			}
			if key == "n" {
				m.back()
			}
		case Ready:
			if key == "u" {
				m.busy = true
				return m, tea.ExecProcess(m.child([]string{"setup", m.setup.journal.Plan.Slug, "--undo", "--plain"}), func(e error) tea.Msg { return childMsg{kind: Setup, err: e} })
			}
			if key == "enter" {
				return m, m.launchSetup(m.setup.journal)
			}
		case Gallery:
			if key == "down" || key == "j" {
				m.selected = min(m.selected+1, len(Claims())-1)
			}
			if key == "up" || key == "k" {
				m.selected = max(0, m.selected-1)
			}
			if key == "enter" || key == "?" {
				m.pendingClaim = false
				m.push(ClaimCard)
			}
		case ClaimCard:
			if key == "?" && !m.pendingClaim {
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
		m.labels = append(m.labels, title)
		f.WithTheme(m.formTheme())
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
	m.labels = append(m.labels, "Context (native)")
	ctx.WithTheme(m.formTheme())
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
	budget.WithTheme(m.formTheme())
	m.fields = append(m.fields, budget)
	m.labels = append(m.labels, "Trial cap")
	retention := huh.NewSelect[bool]().Options(huh.NewOption("no-logging", false), huh.NewOption("ZDR only", true)).Value(&p.ZDR).Inline(true)
	retention.WithKeyMap(huh.NewDefaultKeyMap())
	retention.WithTheme(m.formTheme())
	m.fields = append(m.fields, retention)
	m.labels = append(m.labels, "Retention")

}
func (m *Root) footer() string {
	if m.screen() == Setup {
		if m.busy {
			return "ctrl-c pauses  d details"
		}
		return "enter go  esc back  d details"
	}
	if m.screen() == Ready {
		return "enter open " + toolName(m.cfg.Preferences.Tool) + " now  d details  q later"
	}
	if m.busy || (m.screen() == Chat && m.chat.busy) {
		return "ctrl-c stop"
	}
	if m.screen() == Recipes && len(m.listing.Rows) == 0 {
		return "n needs  / search  esc back  q quit"
	}
	switch m.screen() {
	case First:
		return "enter browser  p paste  esc back"
	case Paste:
		return "enter connect  esc back"
	case Needs:
		return "↑↓ row  ←→ change  enter go  esc back  q quit"
	case Recipes:
		return "enter try  s set up  d details  w window  n needs  / search"
	case Details:
		return "enter try  space boosts  s set up  ? why  esc back  q quit"
	case Home:
		if m.offer && m.modelIndex < 0 {
			return "enter start  s skip  ? help"
		}
		return "enter run in " + toolName(m.cfg.Preferences.Tool) + "  t cloud trial  / search  ? help"
	case Summary:
		return "1 too slow  2 fine  3 great  s set up  t another  q quit"
	case Gallery:
		return "↑↓ move  enter claim  esc back  q quit"
	case ClaimCard:
		if m.pendingClaim {
			return "esc back  q quit"
		}
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
	if m.busy && m.calibration == nil && m.screen() != Home && m.screen() != Trial && m.screen() != Setup {
		body += "\n" + m.waiting() + " Working…"
	}
	if m.message != "" {
		body += "\n" + m.theme.caveat.Render(m.message)
	}
	if m.expanded && m.rawError != "" {
		body += "\n" + wrap(m.rawError, m.width-4)
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
