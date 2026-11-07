// Package trial enforces private routing and a bounded cloud trial.
package trial

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/aeroknight786/trialm/internal/catalog"
	"github.com/aeroknight786/trialm/internal/openrouter"
	"github.com/aeroknight786/trialm/internal/predict"
)

type Policy struct {
	ZDR, Tools      bool
	Context         int
	Provider, Quant string
}

func Select(endpoints []openrouter.Endpoint, p Policy) (openrouter.Endpoint, error) {
	var eligible []openrouter.Endpoint
	for _, e := range endpoints {
		policy := e.DataCollection
		if policy == "" {
			policy = e.DataPolicy.DataCollection
		}
		if policy != "deny" || e.Tag == "" || e.ContextLength < p.Context || (p.ZDR && (!(e.ZDR || e.DataPolicy.ZDR) || (e.SafetyRetention != nil && *e.SafetyRetention))) {
			continue
		}
		if p.Provider != "" {
			match := false
			for _, tag := range strings.Split(p.Provider, ",") {
				match = match || tag == e.Tag
			}
			if !match {
				continue
			}
		}
		if p.Tools && (!has(e.SupportedParameters, "tools") || !has(e.SupportedParameters, "tool_choice")) {
			continue
		}
		if _, err := Prices(e.Pricing); err != nil {
			continue
		}
		eligible = append(eligible, e)
	}
	if len(eligible) == 0 {
		return openrouter.Endpoint{}, fmt.Errorf("no no-logging endpoint meets this trial's context, tools and privacy requirements; use --privacy predict-only or choose another model")
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		a, b := eligible[i], eligible[j]
		qa, qb := precisionRank(a.Quantization, p.Quant), precisionRank(b.Quantization, p.Quant)
		if qa != qb {
			return qa < qb
		}
		up := func(e openrouter.Endpoint) float64 {
			if e.Uptime == nil {
				return 0
			}
			return *e.Uptime
		}
		if up(a) != up(b) {
			return up(a) > up(b)
		}
		pa, _ := Prices(a.Pricing)
		pb, _ := Prices(b.Pricing)
		return pa.Prompt+pa.Completion < pb.Prompt+pb.Completion
	})
	return eligible[0], nil
}
func has(ss []string, s string) bool { return slices.Contains(ss, s) }
func precisionRank(precision, quant string) int {
	p, q := strings.ToLower(precision), strings.ToUpper(quant)
	if q == "MXFP4" && (p == "fp4" || p == "mxfp4") {
		return 0
	}
	if q == "F16" || q == "BF16" || strings.Contains(q, "Q8") {
		if p == "bf16" || p == "fp16" {
			return 0
		}
		if p == "fp8" {
			return 1
		}
	}
	if p == "fp8" {
		return 0
	}
	if p == "bf16" || p == "fp16" {
		return 1
	}
	if p == "fp4" || p == "int4" {
		return 2
	}
	return 3
}
func Provider(e openrouter.Endpoint, zdr bool) map[string]any {
	p := map[string]any{"only": []string{e.Tag}, "data_collection": "deny", "allow_fallbacks": false, "require_parameters": true}
	if zdr {
		p["zdr"] = true
	}
	return p
}

type Rates struct{ Prompt, Completion, CacheRead, Request float64 }

func Prices(p openrouter.Price) (Rates, error) {
	var r Rates
	for _, v := range []struct {
		s        string
		p        *float64
		required bool
	}{{p.Prompt, &r.Prompt, true}, {p.Completion, &r.Completion, true}, {p.CacheRead, &r.CacheRead, false}, {p.Request, &r.Request, false}} {
		if v.s == "" && !v.required {
			continue
		}
		n, err := strconv.ParseFloat(v.s, 64)
		if err != nil || n < 0 || math.IsNaN(n) || math.IsInf(n, 0) {
			return r, fmt.Errorf("endpoint prices are missing or invalid")
		}
		*v.p = n
	}
	if p.CacheRead == "" {
		r.CacheRead = r.Prompt
	}
	return r, nil
}
func Fidelity(m catalog.Model, q catalog.Quant, e openrouter.Endpoint) string {
	p := strings.ToLower(e.Quantization)
	label := "≈ sharper · declared " + p + " vs local " + q.Name
	switch {
	case m.TrialStandIn != "" && !m.Generated:
		label = "≈ family · hosted sibling · quality unknown"
	case m.TrialStandIn != "":
		label = "≈ base · stand-in · quality unknown"
	case m.UpstreamUnverified:
		label = "≈ family · hosted sibling · quality unknown"
	case p == "" || p == "unknown":
		label = "precision undisclosed"
	case (strings.EqualFold(q.Name, "MXFP4") && (p == "fp4" || p == "mxfp4")) || (strings.EqualFold(q.Name, "BF16") && p == "bf16") || (strings.EqualFold(q.Name, "F16") && p == "fp16"):
		label = "exact · matching weights and declared " + p
	}
	if strings.Contains(strings.ToUpper(q.Name), "Q3") || strings.Contains(strings.ToUpper(q.Name), "Q2") {
		label += " · the trial is an upper bound on quality"
	}
	return label
}

// Pessimistic slows the same evaluated timing to the lower speed edge. The
// endpoint's cloud speeds never influence local pacing.
func Pessimistic(t *predict.Timing, decodePct, prefillPct int) *predict.Timing {
	clone := *t
	clone.DecodeMult *= math.Max(.05, 1-float64(decodePct)/100)
	clone.PrefillMult *= math.Max(.05, 1-float64(prefillPct)/100)
	return &clone
}
