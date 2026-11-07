package calib

import (
	"fmt"
	"math"
	"strings"
)

// Step is one engine invocation. Estimates describe the owner's M5, build
// 11960, including synthetic generation; they are not a timeout or a promise.
type Step struct {
	Probe            Probe   `json:"probe"`
	Group            int     `json:"group"`
	Depths           string  `json:"depths"`
	EstimatedSeconds float64 `json:"estimated_seconds"`
}

func Plan(quick bool, spec ...bool) []Step {
	var steps []Step
	for _, p := range Grid(quick) {
		depths := "0"
		referenceCount := p.Shape.Counts[1]
		// Deep preparation dominates; six Gemma layers preserve its full cycle,
		// and two dense layers suffice for the incremental attention cost.
		if p.Shape.Class == "dense" || p.Shape.Class == "gemma" {
			referenceCount = p.Shape.Counts[0]
		}
		if !p.Check && p.Quant == referenceQuant(p.Shape.Class) && p.Layers == referenceCount {
			depths = "0,8192,32768"
		}
		// Old full-depth invocations, from run-timestamps.txt. Check probes were
		// visibly contended, so estimate depth preparation from the training pair.
		legacy := map[string]float64{"hybrid": 18, "sliding": 42, "gemma": 36, "dense": 18, "moe": 41}[p.Shape.Class]
		zero := 1 + .35*float64(p.Layers)
		_, _, _, q := p.Layout()
		generation := .5 + float64(q.FileBytes)/2e9
		estimate := zero + generation
		if depths != "0" {
			estimate += 1.15 * (legacy - (1 + .4*float64(p.Layers)))
		}
		// Allow 15% for process/load variance and the extra state restore at r=3.
		steps = append(steps, Step{Probe: p, Depths: depths, EstimatedSeconds: math.Ceil(1.15 * estimate)})
		if len(spec) > 0 && spec[0] {
			steps = append(steps, Step{Probe: p, Group: 1, Depths: "0", EstimatedSeconds: math.Ceil(1.15 * (.5 + .04*float64(p.Layers)))})
		}
	}
	return steps
}
func EstimatedSeconds(steps []Step) float64 {
	total := 0.
	for _, s := range steps {
		total += s.EstimatedSeconds
	}
	return total
}
func (s Step) Args(path string, cpu bool) []string {
	prompt, gen := "512", "8"
	if s.Group == 1 {
		prompt, gen = "1,2,3,4", "0"
	}
	args := []string{"-m", path, "-p", prompt, "-n", gen, "-d", s.Depths, "-r", "3", "-o", "json", "-ngl", "99", "-fa", "on", "-lm", "mlock"}
	// llama-bench's default warm-up stays enabled.
	if cpu {
		args = append(args, "-ngl", "0", "-dev", "none", "-nopo", "1")
	}
	return args
}
func (s Step) Description() string {
	check := ""
	if s.Probe.Check {
		check = " (held out)"
	}
	return fmt.Sprintf("%s%s · %s", s.Probe.ID(), check, strings.Join(s.Args("probe.gguf", false)[2:], " "))
}

// Duration is a plan's estimated run time for people, e.g. "about 5–8 min";
// the estimate comes from one machine, so it is a range, not a figure.
func Duration(steps []Step) string {
	m := math.Round(EstimatedSeconds(steps) / 60)
	return fmt.Sprintf("about %.0f–%.0f min", math.Max(1, m-1), m+2)
}
