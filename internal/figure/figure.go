// Package figure carries provenance and uncertainty with each public number.
package figure

import (
	"math"
	"strconv"
)

type Kind string

const (
	Claimed    Kind = "claimed"
	Simulated  Kind = "simulated"
	Calibrated Kind = "calibrated"
	Measured   Kind = "measured"
)

type Figure struct {
	V        float64 `json:"v"`
	Lo       float64 `json:"lo"`
	Hi       float64 `json:"hi"`
	Unit     string  `json:"unit"`
	Kind     Kind    `json:"kind"`
	Src      string  `json:"src"`
	InSample bool    `json:"in_sample,omitempty"`
}

func Exact(v float64, unit string, kind Kind, src string) Figure {
	return Figure{V: v, Lo: v, Hi: v, Unit: unit, Kind: kind, Src: src}
}
func Band(v float64, pct int, unit string, kind Kind, src string) Figure {
	d := math.Abs(v) * float64(pct) / 100
	return Figure{V: v, Lo: v - d, Hi: v + d, Unit: unit, Kind: kind, Src: src}
}
func (f Figure) Number() string {
	pct := 0
	if f.V != 0 {
		pct = int(math.Ceil(100*math.Max(math.Abs(f.Hi-f.V), math.Abs(f.V-f.Lo))/math.Abs(f.V) - 1e-9))
	}
	s := BandNumber(f.V, pct)
	if f.Kind == Claimed {
		s += " claim"
	}
	if f.InSample {
		s += " fit"
	}
	return s
}
func (f Figure) String() string {
	s := f.Number()
	if f.Unit != "" {
		s += " " + f.Unit
	}
	return s
}
func BandNumber(v float64, pct int) string {
	digits := 2
	prefix := ""
	if pct <= 10 {
		digits = 3
	} else if pct > 25 {
		prefix = "~"
	}
	decimals := 0
	if v != 0 {
		decimals = max(0, digits-1-int(math.Floor(math.Log10(math.Abs(v)))))
	}
	rounded := v
	if v != 0 {
		scale := math.Pow(10, float64(digits-1-int(math.Floor(math.Log10(math.Abs(v))))))
		rounded = math.Round(v*scale) / scale
	}
	return prefix + strconv.FormatFloat(rounded, 'f', decimals, 64)
}
