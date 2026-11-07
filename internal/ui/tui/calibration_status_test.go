package tui

import (
	"github.com/aeroknight786/trialm/internal/core"
	"testing"
)

func TestCalibrationStatusIsPlainWords(t *testing.T) {
	for msg, want := range map[string]string{
		"Generating or loading synthetic GGUF":                    "Writing a synthetic model · qwen3-moe IQ3_XXS · 24 layers",
		"Benchmarking -p 512 -n 8 -d 0 -r 3 -o json -ngl 99":      "Timing reading and writing on this GPU · qwen3-moe IQ3_XXS · 24 layers",
		"Saved depth 32768, prompt 512, decode 0 (mlock)":         "Measured at 32k context · qwen3-moe IQ3_XXS · 24 layers",
		"Saved depth 0, prompt 1, decode 0 (mlock)":               "Measured at short context · qwen3-moe IQ3_XXS · 24 layers",
		"Optional machine calibration; estimated 190 seconds ...": "Optional machine calibration; estimated 190 seconds ...",
	} {
		if got := core.CalibrationStatus("v2-qwen3-moe-IQ3_XXS-24", msg); got != want {
			t.Errorf("%q: got %q want %q", msg, got, want)
		}
	}
}
