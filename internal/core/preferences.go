package core

import (
	"context"
	"fmt"
	"github.com/aeroknight786/trialm/internal/harness"
	"github.com/aeroknight786/trialm/internal/selfupdate"
	"github.com/aeroknight786/trialm/internal/store"
	"os"
	"strconv"
	"strings"
	"time"
)

func (session Session) FindPreferences(ctx context.Context, s store.Store, p store.Preferences, o Options, top int) (Listing, error) {
	opts := o
	opts.Context = p.Context
	opts.Use = p.Use
	opts.Priority = p.Priority
	opts.Engines = p.Engines
	opts.Boosts = p.Boosts
	opts.QualityFloor = p.QualityFloor
	opts.MaxDownloadGB = p.MaxDownloadGB
	opts.Tuned = p.Boosts != "off"
	hh, e := harness.Get(p.Tool)
	if e != nil {
		return Listing{}, e
	}
	d, e := session.Detect(ctx, opts)
	if e != nil {
		return Listing{}, e
	}
	query := p.Model
	if query == "any" {
		query = ""
	}
	l, e := session.Find(ctx, opts, d.Target, hh.Scenario, hh, query, top)
	if e == nil {
		rows := []store.Recipe{}
		for _, r := range l.Rows {
			rows = append(rows, store.Recipe{Model: r.Result.Model.OllamaTag, Quant: r.Result.Quant.Name, Runtime: r.Result.Runtime.ID, Harness: hh.ID})
		}
		e = s.Write("last.json", rows)
	}
	return l, e
}
func TrialArgs(row Row, p store.Preferences, o Options) []string {
	r := row.Result
	a := []string{r.Model.OllamaTag, "--runtime", r.Runtime.ID, "--quant", r.Quant.Name, "--for", p.Tool, "--ctx", strconv.Itoa(p.Context), "--use", p.Use, "--budget", strconv.FormatFloat(p.BudgetUSD, 'f', -1, 64), "--privacy", p.Privacy, "--boosts", p.Boosts}
	if p.ZDR {
		a = append(a, "--zdr")
	}
	if o.Device != "" {
		a = append(a, "--device", o.Device, "--ram", fmt.Sprint(o.RAM), "--vram", fmt.Sprint(o.VRAM))
	}
	return a
}

func ChildEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, e := range env {
		if !strings.HasPrefix(e, "CLAUDECODE=") && !strings.HasPrefix(e, "CLAUDE_CODE_ENTRYPOINT=") {
			out = append(out, e)
		}
	}
	return out
}
func NeedsCloudKey(args []string) bool {
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

// CalibrationStatus turns a calibrate --json event into words: the raw
// messages carry llama-bench flags and probe ids meant for the log.
func CalibrationStatus(probe, msg string) string {
	what := probe
	if f := strings.Split(probe, "-"); len(f) >= 4 {
		what = strings.Join(f[1:len(f)-2], "-") + " " + f[len(f)-2] + " · " + f[len(f)-1] + " layers"
	}
	var depth int
	switch {
	case strings.HasPrefix(msg, "Generating or loading"):
		return "Writing a synthetic model · " + what
	case strings.HasPrefix(msg, "Benchmarking"):
		return "Timing reading and writing on this GPU · " + what
	case strings.HasPrefix(msg, "Using finished rows"):
		return "Reusing finished measurements · " + what
	case strings.HasPrefix(msg, "Saved depth"):
		if _, err := fmt.Sscanf(msg, "Saved depth %d", &depth); err == nil {
			if depth == 0 {
				return "Measured at short context · " + what
			}
			return fmt.Sprintf("Measured at %dk context · %s", depth/1024, what)
		}
	}
	return msg
}

func UpdateNotice(ctx context.Context, s store.Store, version string) string {
	n := selfupdate.Cached(s)
	if os.Getenv("TRIALM_OFFLINE") != "1" && selfupdate.Claim(s, time.Now()) {
		check, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		_ = (selfupdate.Client{}).Refresh(check, s)
		n = selfupdate.Cached(s)
	}
	if selfupdate.Newer(n.Version, version) {
		return n.Version + " available · trialm update"
	}
	return ""
}
