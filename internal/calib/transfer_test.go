package calib

import (
	"strings"
	"testing"
)

func TestTransferFallbackEvidence(t *testing.T) {
	for _, mode := range []string{"decode", "prefill"} {
		for _, component := range []string{"base", "kernel", "both"} {
			for _, rejected := range []bool{false, true} {
				c := Calibration{}
				add := func(class, quant string, depth int, mult float64, band int) {
					c.Fits = append(c.Fits, Fit{Class: class, Quant: quant, Depth: depth, Mode: mode, Fixed: 1, PerLayer: 1, BaseFixed: mult, BaseLayer: mult, BandPct: band})
				}
				for _, d := range []int{0, 8192, 32768} {
					add("hybrid", "IQ3_XXS", d, 0.5, 20)
				}
				for _, item := range []struct{ name, quant string }{{"base", "IQ3_XXS"}, {"kernel", "Q4_K_M"}} {
					if component != item.name && component != "both" {
						add("dense", item.quant, 0, 2, 90)
					} else if rejected {
						c.Fits = append(c.Fits, Fit{Class: "dense", Quant: item.quant, Mode: mode, BandPct: -1, Rejected: "held-out error exceeds 35%"})
					}
				}
				for _, d := range []int{0, 8192, 32768} {
					f, ok := c.FactorEvidence("hybrid", "Q4_K_M", d, 32, mode)
					if !ok || !f.Fallback || f.Multiplier != .5 || f.BandPct < 92 || !strings.Contains(f.Basis, "alone") || !strings.Contains(f.Basis, "kernel ratio allowance") {
						t.Fatalf("%s %s rejected=%v depth=%d: %+v %v", mode, component, rejected, d, f, ok)
					}
					if rejected && !strings.Contains(f.Basis, "rejected") {
						t.Fatal(f.Basis)
					}
					if !rejected && !strings.Contains(f.Basis, "missing") {
						t.Fatal(f.Basis)
					}
				}
				// A failed check for the actual requested pair still blocks composition.
				c.Fits = append(c.Fits, Fit{Class: "hybrid", Quant: "Q4_K_M", Mode: mode, Rejected: "failed"})
				if _, _, ok := c.Factor("hybrid", "Q4_K_M", 0, 32, mode); ok {
					t.Fatal("bypassed target rejection")
				}
			}
		}
	}
}

func TestTransferNeedsArchitectureAndKnownKernel(t *testing.T) {
	c := Calibration{Fits: []Fit{{Class: "hybrid", Quant: "IQ3_XXS", Mode: "decode", Fixed: 1, PerLayer: 1, BaseFixed: 2, BaseLayer: 2, BandPct: 10}}}
	for _, tc := range []struct {
		class, q string
		depth    int
	}{{"hybrid", "UNSUPPORTED", 0}, {"hybrid", "Q4_K_M", 32768}, {"gemma", "Q8_0", 0}, {"mla", "Q4_K_M", 0}, {"dense", "Q8_0", 0}} {
		if _, _, ok := c.Factor(tc.class, tc.q, tc.depth, 32, "decode"); ok {
			t.Fatal(tc)
		}
	}
}

func TestInvalidTransferBandIsUnavailable(t *testing.T) {
	c := Calibration{Fits: []Fit{
		{Class: "hybrid", Quant: "IQ3_XXS", Mode: "decode", Fixed: 1, PerLayer: 1, BaseFixed: 1, BaseLayer: 1, BandPct: 10},
		{Class: "dense", Quant: "IQ3_XXS", Mode: "decode", Fixed: 1, PerLayer: 1, BaseFixed: 1, BaseLayer: 1, BandPct: 10},
		{Class: "dense", Quant: "Q8_0", Mode: "decode", Fixed: 1, PerLayer: 1, BaseFixed: 1, BaseLayer: 1, BandPct: 10, Checks: []FitPoint{{Layers: 4}}},
	}}
	f, ok := c.FactorEvidence("hybrid", "Q8_0", 0, 32, "decode")
	if !ok || !f.Fallback || f.BandPct < 60 {
		t.Fatal("invalid band was composed", f, ok)
	}
}
