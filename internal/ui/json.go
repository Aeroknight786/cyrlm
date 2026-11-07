// Package ui renders core events without terminal control sequences.
package ui

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/aeroknight786/trialm/internal/core"
	"github.com/aeroknight786/trialm/internal/figure"
	"github.com/aeroknight786/trialm/internal/setup"
)

type Renderer struct {
	Out   io.Writer
	Human io.Writer
	JSON  bool
	Err   error
}

func (r *Renderer) Handle(e core.Event) {
	if r.Err != nil {
		return
	}
	if !r.JSON {
		switch e.Type {
		case "setup_plan":
			if data, ok := e.Data.(map[string]any); ok && data["pending"] == true {
				if p, ok := data["plan"].(setup.Plan); ok {
					PrintPlan(r.Out, p)
				}
			}
		case "setup_status":
			if j, ok := e.Data.(setup.Journal); ok {
				_, r.Err = fmt.Fprintln(r.Out, j.Summary())
			}
		case "setup_step":
			if u, ok := e.Data.(setup.Update); ok {
				_, r.Err = fmt.Fprintf(r.Out, "%d/%d %s · %s · %s\n", u.Index, u.Total, u.Kind, u.Title, u.State)
			}
		case "setup_progress":
			if u, ok := e.Data.(setup.Update); ok && u.Progress != nil {
				p := u.Progress
				_, r.Err = fmt.Fprintf(r.Out, "Download %s / %s · %s/s · ETA %s\n", gb(float64(p.Done)), gb(float64(p.Total)), gb(p.BytesPerSec), dur(p.ETA))
			}
		case "setup_ready":
			_, r.Err = fmt.Fprintln(r.Out, "✓ Ready")
		case "setup_paused":
			_, r.Err = fmt.Fprintln(r.Out, "Setup paused. Run trialm setup to resume.")
		case "setup_undone":
			_, r.Err = fmt.Fprintln(r.Out, "Setup undone.")
		}

		if s, ok := e.Data.(string); ok {
			_, r.Err = fmt.Fprintln(r.Out, s)
		}
		return
	}
	next := e.Next
	if next == nil {
		next = []core.Next{}
	}
	payload := Numbers(e.Data)
	if e.Type == "setup_ready" {
		payload = numberData(e.Data, figure.Measured)
	}
	if e.Type == "setup_progress" {
		if u, ok := e.Data.(setup.Update); ok && u.Progress != nil {
			p := u.Progress
			payload = map[string]any{"index": figure.Exact(float64(u.Index), "count", figure.Measured, "setup:journal"), "total": figure.Exact(float64(u.Total), "count", figure.Measured, "setup:journal"), "bytes": figure.Exact(float64(p.Done), "bytes", figure.Measured, "download:progress"), "total_bytes": figure.Exact(float64(p.Total), "bytes", figure.Measured, "download:progress"), "rate": figure.Exact(p.BytesPerSec, "bytes/s", figure.Measured, "download:progress"), "eta": figure.Exact(p.ETA.Seconds(), "s", figure.Measured, "download:progress")}
		}
	}
	event := map[string]any{"v": 1, "type": e.Type, "data": payload}
	if e.Type == "result" {
		if data, ok := e.Data.(map[string]any); ok {
			if ready, ok := data["ready"].(bool); ok {
				event["ready"] = ready
			}
		}
	}
	if e.Type == "result" || strings.HasPrefix(e.Type, "setup_") {
		event["next"] = next
	}
	r.Err = json.NewEncoder(r.Out).Encode(event)
}

// Numbers adapts legacy payloads at the public boundary. The version of the
// envelope is the sole raw number; every payload number carries provenance.
func Numbers(v any) any { return numberData(v, figure.Simulated) }
func numberData(v any, kind figure.Kind) any {
	b, err := json.Marshal(v)
	if err != nil {
		return map[string]string{"error": "cannot encode event"}
	}
	var raw any
	if json.Unmarshal(b, &raw) != nil {
		return nil
	}
	return numbers(raw, "", kind)
}
func numbers(v any, key string, kind figure.Kind) any {
	switch x := v.(type) {
	case map[string]any:
		if _, ok := x["v"]; ok {
			if _, ok = x["src"]; ok {
				return x
			}
		}
		if k, ok := x["kind"].(string); ok {
			switch figure.Kind(k) {
			case figure.Claimed, figure.Simulated, figure.Calibrated, figure.Measured:
				kind = figure.Kind(k)
			}
		}
		decode, _ := x["decode_error_range_pct"].(float64)
		prefill, _ := x["prefill_error_range_pct"].(float64)
		if predicted, ok := x["predicted"].(map[string]any); ok {
			for metric, item := range predicted {
				if value, ok := item.(float64); ok {
					pct := int(decode)
					if strings.Contains(strings.ToLower(metric), "prefill") {
						pct = int(prefill)
					}
					predicted[metric] = figure.Band(value, pct, "tok/s", figure.Simulated, "predict:"+metric)
				}
			}
		}
		for k, item := range x {
			pct := 0
			if strings.HasPrefix(k, "decode_tps") {
				pct = int(decode)
			}
			if strings.HasPrefix(k, "prefill_tps") {
				pct = int(prefill)
			}
			if strings.HasSuffix(k, "_s") && k != "cloud_session_s" {
				pct = int(max(decode, prefill))
			}
			converted := numbers(item, k, kind)
			if f, ok := converted.(figure.Figure); ok && pct > 0 {
				converted = figure.Band(f.V, pct, f.Unit, f.Kind, "predict:"+k)
			}
			x[k] = converted
		}
		return x
	case []any:
		for i, item := range x {
			x[i] = numbers(item, key, kind)
		}
		return x
	case float64:
		key = strings.ToLower(key)
		unit := "count"
		switch {
		case strings.Contains(key, "tps"):
			unit = "tok/s"
		case strings.HasSuffix(key, "_s") || strings.HasSuffix(key, "seconds"):
			unit = "s"
		case strings.Contains(key, "usd") || strings.Contains(key, "credit"):
			unit = "USD"
		case strings.HasSuffix(key, "_gb"):
			unit = "GB"
		case strings.Contains(key, "pct"):
			unit = "%"
		case strings.Contains(key, "tokens"):
			unit = "tokens"
		case strings.Contains(key, "bytes"):
			unit = "bytes"
		}
		return figure.Exact(x, unit, kind, "core:"+key)
	default:
		return v
	}
}
