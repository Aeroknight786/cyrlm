package ui

import (
	"github.com/aeroknight786/trialm/internal/figure"
	"github.com/aeroknight786/trialm/internal/store"
)

func TrialCards(rows []store.TrialEntry) []map[string]any {
	out := []map[string]any{}
	for _, r := range rows {
		out = append(out, map[string]any{"id": r.ID, "at": r.At, "model": r.Model, "quant": r.Quant, "runtime": r.Runtime, "rating": figure.Exact(float64(r.Rating), "rating", figure.Measured, "user:rating"), "summary": r.Report.EventData(r.Band)})
	}
	return out
}
