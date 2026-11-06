// Command charm-demo is a compile-checked skeleton for triaLM's CLI shape:
//
//	requirements (huh form) -> recipe comparison (lipgloss table) -> setup progress (bubbletea, inline)
//
// Three output modes, chosen once at startup:
//
//	TUI   : stdin+stdout are terminals and neither --json nor --yes was given
//	plain : no TTY, --yes, TERM=dumb or CI set: no prompts, no animation; ANSI stripped by lipgloss
//	JSON  : --json: one JSON document on stdout, nothing else on stdout
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image/color"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"
)

// ---------- domain (stand-ins for triaLM's real types) ----------

type Requirements struct {
	Harness  string  `json:"harness"`
	MinTokS  float64 `json:"min_decode_tok_s"`
	Context  int     `json:"context_tokens"`
	Priority string  `json:"priority"` // speed | quality
}

type Recipe struct {
	ID          string   `json:"id"`
	Engine      string   `json:"engine"`
	Model       string   `json:"model"`
	Techniques  []string `json:"techniques"`
	DecodeTokS  float64  `json:"decode_tok_s"`
	PrefillTokS float64  `json:"prefill_tok_s"`
	MemGB       float64  `json:"mem_gb"`
	MaxContext  int      `json:"max_context"`
	Quality     int      `json:"quality"` // relative score, higher is better
}

func sampleRecipes() []Recipe {
	return []Recipe{
		{"qwen3-30b-a3b/llama.cpp/q4km+moe-offload", "llama.cpp", "Qwen3-30B-A3B Q4_K_M", []string{"expert-offload", "kv-q8"}, 38, 410, 17.2, 131072, 7},
		{"qwen3-30b-a3b/mlx/4bit", "mlx-lm", "Qwen3-30B-A3B 4bit", []string{"wired-limit"}, 52, 620, 17.9, 65536, 7},
		{"gpt-oss-20b/ollama/mxfp4", "ollama", "gpt-oss-20b MXFP4", []string{}, 61, 900, 13.1, 131072, 6},
		{"qwen3-32b/llama.cpp/q4km+spec", "llama.cpp", "Qwen3-32B Q4_K_M", []string{"speculative", "kv-q8"}, 14, 180, 21.5, 32768, 8},
	}
}

func rank(req Requirements, rs []Recipe) []Recipe {
	out := rs[:0:0]
	for _, r := range rs {
		if r.MaxContext >= req.Context {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if (a.DecodeTokS >= req.MinTokS) != (b.DecodeTokS >= req.MinTokS) {
			return a.DecodeTokS >= req.MinTokS // meets requirement first
		}
		if req.Priority == "quality" && a.Quality != b.Quality {
			return a.Quality > b.Quality
		}
		return a.DecodeTokS > b.DecodeTokS
	})
	return out
}

// ---------- theme: one accent, neutrals, three status colours, adaptive ----------

type theme struct{ accent, muted, ok, warn, bad color.Color }

func newTheme(isDark bool) theme {
	ld := lipgloss.LightDark(isDark) // pick(light, dark)
	return theme{
		accent: ld(lipgloss.Color("#0B7A75"), lipgloss.Color("#4FD1C5")),
		muted:  ld(lipgloss.Color("#6E7781"), lipgloss.Color("#8B949E")),
		ok:     ld(lipgloss.Color("#1A7F37"), lipgloss.Color("#3FB950")),
		warn:   ld(lipgloss.Color("#9A6700"), lipgloss.Color("#D29922")),
		bad:    ld(lipgloss.Color("#CF222E"), lipgloss.Color("#F85149")),
	}
}

// formTheme derives huh styles from ThemeBase so forms match the tables.
// huh calls it with its own background detection (tea.BackgroundColorMsg).
var formTheme = huh.ThemeFunc(func(isDark bool) *huh.Styles {
	t, s := newTheme(isDark), huh.ThemeBase(isDark)
	s.Focused.Base = s.Focused.Base.BorderForeground(t.accent)
	s.Focused.Title = s.Focused.Title.Foreground(t.accent).Bold(true)
	s.Focused.Description = s.Focused.Description.Foreground(t.muted)
	s.Focused.SelectSelector = s.Focused.SelectSelector.Foreground(t.accent)
	s.Focused.SelectedOption = s.Focused.SelectedOption.Foreground(t.accent)
	s.Focused.ErrorIndicator = s.Focused.ErrorIndicator.Foreground(t.bad)
	s.Focused.ErrorMessage = s.Focused.ErrorMessage.Foreground(t.bad)
	s.Focused.FocusedButton = s.Focused.FocusedButton.Background(t.accent)
	return s
})

// ---------- modes ----------

type mode int

const (
	modeTUI mode = iota
	modePlain
	modeJSON
)

func detectMode(jsonOut, yes bool) mode {
	switch {
	case jsonOut:
		return modeJSON
	case yes, os.Getenv("CI") != "", os.Getenv("TERM") == "dumb",
		!term.IsTerminal(os.Stdin.Fd()), !term.IsTerminal(os.Stdout.Fd()):
		return modePlain
	}
	return modeTUI
}

// colorOptions applies NO_COLOR per no-color.org (any non-empty value). colorprofile
// v0.4.3 only honours strconv.ParseBool-true values, so NO_COLOR=yes would still colour.
func colorOptions() []tea.ProgramOption {
	if os.Getenv("NO_COLOR") == "" {
		return nil
	}
	lipgloss.Writer.Profile = colorprofile.Ascii // bold/faint survive, colours do not
	return []tea.ProgramOption{tea.WithColorProfile(colorprofile.Ascii)}
}

// ---------- screen 1: requirements (huh) ----------

func askRequirements(req *Requirements) error {
	minTPS := strconv.FormatFloat(req.MinTokS, 'f', -1, 64)
	err := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Title("Coding agent").
			Options(huh.NewOption("Claude Code", "claude-code"), huh.NewOption("Codex", "codex"), huh.NewOption("OpenCode", "opencode")).
			Value(&req.Harness),
		huh.NewInput().Title("Slowest decode speed you'll accept").Description("tokens per second").
			Value(&minTPS).Validate(func(s string) error {
			if v, err := strconv.ParseFloat(s, 64); err != nil || v <= 0 {
				return errors.New("enter a positive number")
			}
			return nil
		}),
		huh.NewSelect[int]().Title("Context you need").
			Options(huh.NewOption("32K", 32768), huh.NewOption("64K", 65536), huh.NewOption("128K", 131072)).
			Value(&req.Context),
		huh.NewSelect[string]().Title("Optimise for").
			Options(huh.NewOption("Speed", "speed"), huh.NewOption("Quality", "quality")).
			Value(&req.Priority),
	)).WithTheme(formTheme).WithProgramOptions(colorOptions()...).
		WithAccessible(os.Getenv("ACCESSIBLE") != "").Run()
	req.MinTokS, _ = strconv.ParseFloat(minTPS, 64)
	return err
}

// ---------- screen 2: recipes (lipgloss table) ----------

func recipeTable(req Requirements, rs []Recipe, t theme) string {
	cell := lipgloss.NewStyle().Padding(0, 1)
	head := cell.Foreground(t.muted).Bold(true)
	tb := table.New().
		Border(lipgloss.NormalBorder()).BorderStyle(lipgloss.NewStyle().Foreground(t.muted)).
		BorderTop(false).BorderBottom(false).BorderLeft(false).BorderRight(false).BorderColumn(false).
		Headers("", "RECIPE", "ENGINE", "DECODE", "PREFILL", "MEMORY", "TECHNIQUES").
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return head
			}
			s := cell
			switch col {
			case 0:
				s = s.Foreground(t.accent).Bold(true)
			case 3: // colour decode speed against the user's requirement
				switch d := rs[row].DecodeTokS; {
				case d >= req.MinTokS:
					s = s.Foreground(t.ok)
				case d >= 0.7*req.MinTokS:
					s = s.Foreground(t.warn)
				default:
					s = s.Foreground(t.bad)
				}
			case 6:
				s = s.Foreground(t.muted)
			}
			if col >= 3 && col <= 5 {
				s = s.Align(lipgloss.Right)
			}
			return s
		})
	for i, r := range rs {
		mark := " "
		if i == 0 {
			mark = "›"
		}
		tb.Row(mark, r.ID, r.Engine,
			fmt.Sprintf("%.0f tok/s", r.DecodeTokS), fmt.Sprintf("%.0f tok/s", r.PrefillTokS),
			fmt.Sprintf("%.1f GB", r.MemGB), strings.Join(r.Techniques, ", "))
	}
	return tb.Render()
}

// ---------- screen 3: setup progress (bubbletea, inline, no alt-screen) ----------

type stepDoneMsg struct{}

type setupModel struct {
	t     theme
	steps []string
	i     int
	spin  spinner.Model
	bar   progress.Model
}

func newSetup(t theme, steps []string) setupModel {
	return setupModel{
		t: t, steps: steps,
		spin: spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(lipgloss.NewStyle().Foreground(t.accent))),
		bar:  progress.New(progress.WithWidth(40), progress.WithColors(t.accent), progress.WithoutPercentage()),
	}
}

// doStep is where real work runs: always inside a tea.Cmd, never in Update.
func doStep() tea.Cmd {
	return tea.Tick(350*time.Millisecond, func(time.Time) tea.Msg { return stepDoneMsg{} })
}

func (m setupModel) Init() tea.Cmd { return tea.Batch(m.spin.Tick, doStep()) }

func (m setupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Interrupt
		}
	case tea.WindowSizeMsg:
		m.bar.SetWidth(min(40, msg.Width-4))
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case stepDoneMsg:
		// tea.Println writes above the live region, so finished steps stay in scrollback.
		line := lipgloss.NewStyle().Foreground(m.t.ok).Render("✓") + " " + m.steps[m.i]
		m.i++
		if m.i == len(m.steps) {
			return m, tea.Sequence(tea.Println(line), tea.Quit)
		}
		return m, tea.Batch(tea.Println(line), doStep())
	}
	return m, nil
}

func (m setupModel) View() tea.View {
	if m.i >= len(m.steps) {
		return tea.NewView("")
	}
	muted := lipgloss.NewStyle().Foreground(m.t.muted)
	return tea.NewView(fmt.Sprintf("%s %s\n%s %s\n", m.spin.View(), m.steps[m.i],
		m.bar.ViewAs(float64(m.i)/float64(len(m.steps))), muted.Render(fmt.Sprintf("%d/%d", m.i, len(m.steps)))))
}

// ---------- main ----------

func main() {
	req := Requirements{}
	jsonOut := flag.Bool("json", false, "print one JSON document on stdout; never prompt")
	yes := flag.Bool("yes", false, "never prompt; take requirements from flags")
	flag.StringVar(&req.Harness, "harness", "claude-code", "claude-code | codex | opencode")
	flag.Float64Var(&req.MinTokS, "min-tps", 20, "slowest acceptable decode speed (tokens/s)")
	flag.IntVar(&req.Context, "ctx", 65536, "context window you need (tokens)")
	flag.StringVar(&req.Priority, "priority", "speed", "speed | quality")
	flag.Parse()

	m := detectMode(*jsonOut, *yes)
	opts := colorOptions()
	isDark := true
	if m == modeTUI {
		// Standalone OSC 11 query: blocks until the terminal answers DA1, or 2s. While it
		// waits it reads stdin, so keys typed then are lost. Do it once, up front; inside
		// one long-lived tea.Program use tea.BackgroundColorMsg instead.
		isDark = lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
		if err := askRequirements(&req); err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				os.Exit(130)
			}
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	}
	recipes := rank(req, sampleRecipes())

	switch m {
	case modeJSON:
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(map[string]any{"requirements": req, "recipes": recipes})
		return
	case modePlain:
		// lipgloss.Println downsamples to the stdout profile: a pipe gets no ANSI at all.
		lipgloss.Println(recipeTable(req, recipes, newTheme(true)))
		if len(recipes) > 0 {
			fmt.Printf("\nnext: trialm try %s\n", recipes[0].ID)
		}
		return
	}

	t := newTheme(isDark)
	lipgloss.Println(recipeTable(req, recipes, t))
	if len(recipes) == 0 {
		return
	}
	proceed := true
	if err := huh.NewForm(huh.NewGroup(huh.NewConfirm().
		Title("Set up " + recipes[0].ID + "?").Affirmative("Set up").Negative("Not now").Value(&proceed),
	)).WithTheme(formTheme).WithProgramOptions(opts...).Run(); err != nil || !proceed {
		return
	}
	steps := []string{"Check " + recipes[0].Engine, "Download " + recipes[0].Model, "Write " + req.Harness + " config", "Verify speed"}
	if _, err := tea.NewProgram(newSetup(t, steps), opts...).Run(); err != nil && !errors.Is(err, tea.ErrInterrupted) {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
