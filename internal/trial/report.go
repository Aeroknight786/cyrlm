package trial

import (
	"fmt"
	"sort"
	"strings"

	"github.com/aeroknight786/trialm/internal/figure"
	"github.com/aeroknight786/trialm/internal/sim"
)

type Report struct {
	Requests, CloudBound, Reloads, ContextPeak, Overflows                                        int
	LocalSeconds, CloudSeconds, ReadingSeconds, WritingSeconds, LoadSeconds, BetweenTurnsSeconds float64
	FirstTurn, TypicalTurn, SlowestTurn                                                          float64
	SlowestCause, Fidelity                                                                       string
	CachedTokens, PromptTokens                                                                   int
	SpendUSD                                                                                     float64
}

func Summarize(records []sim.Record, spend float64, fidelity string) Report {
	r := Report{Requests: len(records), SpendUSD: spend, Fidelity: fidelity}
	var turns []float64
	for i, s := range records {
		local := max(0, s.TotalSec-s.CloudLagSec)
		r.LocalSeconds += local + s.QueueSec
		r.CloudSeconds += s.CloudSec
		r.LoadSeconds += s.LoadSec
		r.ReadingSeconds += s.PrefillSec
		r.WritingSeconds += max(0, local-s.LoadSec-s.PrefillSec)
		if s.CloudLagSec > 0 {
			r.CloudBound++
		}
		if s.Cold {
			r.Reloads++
		}
		r.ContextPeak = max(r.ContextPeak, s.PromptTokens+s.OutputTokens)
		if s.ContextError != "" || s.Truncated != "" {
			r.Overflows++
		}
		r.PromptTokens += s.PromptTokens
		r.CachedTokens += s.CachedTokens
		if i == 0 {
			r.FirstTurn = s.TotalSec
		} else {
			turns = append(turns, s.TotalSec)
			r.BetweenTurnsSeconds += max(0, s.At.Sub(records[i-1].At).Seconds()-records[i-1].TotalSec)
		}
		if s.TotalSec > r.SlowestTurn {
			r.SlowestTurn = s.TotalSec
			r.SlowestCause = "writing"
			if s.CloudLagSec > 0 {
				r.SlowestCause = "cloud-bound"
			} else if s.LoadSec > s.PrefillSec {
				r.SlowestCause = "model load"
			} else if s.PrefillSec > local/2 {
				r.SlowestCause = "reading"
			}
		}
	}
	sort.Float64s(turns)
	if len(turns) > 0 {
		r.TypicalTurn = turns[len(turns)/2]
	}
	return r
}
func (r Report) Plain(band int) string {
	var b strings.Builder
	number := func(v float64) string {
		return figure.Band(v, band, "s", figure.Simulated, "trial:local-schedule").String()
	}
	measured := func(v float64, unit string) string {
		return figure.Exact(v, unit, figure.Measured, "trial:observed").String()
	}
	fmt.Fprintf(&b, "Trial summary · %s\n", r.Fidelity)
	fmt.Fprintf(&b, "  %d requests · local %s · cloud %s · %d cloud-bound moments\n", r.Requests, number(r.LocalSeconds), measured(r.CloudSeconds, "s"), r.CloudBound)
	fmt.Fprintf(&b, "  time split: reading %s · writing %s · load %s · between turns (tools/user) %s\n", number(r.ReadingSeconds), number(r.WritingSeconds), number(r.LoadSeconds), measured(r.BetweenTurnsSeconds, "s"))
	fmt.Fprintf(&b, "  first turn %s · typical follow-up %s · slowest %s (%s)\n", number(r.FirstTurn), number(r.TypicalTurn), number(r.SlowestTurn), r.SlowestCause)
	reuse := 0
	if r.PromptTokens > 0 {
		reuse = 100 * r.CachedTokens / r.PromptTokens
	}
	fmt.Fprintf(&b, "  cache reuse %d%% · loads %d · context peak %d tokens · overflows %d\n", reuse, r.Reloads, r.ContextPeak, r.Overflows)
	fmt.Fprintf(&b, "  spend %s\n", measured(r.SpendUSD, "USD"))
	return b.String()
}
