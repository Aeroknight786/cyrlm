// Package ui renders core events without terminal control sequences.
package ui

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/aeroknight786/trialm/internal/core"
	"github.com/aeroknight786/trialm/internal/figure"
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
		if s, ok := e.Data.(string); ok {
			_, r.Err = fmt.Fprintln(r.Out, s)
		}
		return
	}
	next := e.Next
	if next == nil {
		next = []core.Next{}
	}
	event := map[string]any{"v": 1, "type": e.Type, "data": Numbers(e.Data)}
	if e.Type == "result" {
		event["next"] = next
	}
	r.Err = json.NewEncoder(r.Out).Encode(event)
}

// Numbers adapts legacy payloads at the public boundary. The version of the
// envelope is the sole raw number; every payload number carries provenance.
func Numbers(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return map[string]string{"error": "cannot encode event"}
	}
	var raw any
	if json.Unmarshal(b, &raw) != nil {
		return nil
	}
	return numbers(raw, "", figure.Simulated)
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
