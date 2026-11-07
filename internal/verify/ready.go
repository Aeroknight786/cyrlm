package verify

import (
	"context"
	"errors"

	"github.com/aeroknight786/trialm/internal/harness"
)

// P2 contract: the setup executor's verify step and `trialm launch --check`
// depend only on Ready and these types. The verify agent replaces the stub.

// Predicted is what triaLM predicted for this recipe on this machine.
type Predicted struct {
	DecodeTPS0      float64 // writing speed near depth 0
	DecodeTPSTarget float64 // writing speed at ReadyOptions.ContextTokens
	PrefillTPS      float64 // prompt reading speed at the recipe's ubatch
}

// ReadyOptions configures the Ready checks for one running engine.
type ReadyOptions struct {
	Endpoint      string // engine base URL, e.g. http://127.0.0.1:8080
	Model         string // model name the engine serves
	APIKey        string
	ContextTokens int     // the recipe's target depth
	BitsPerWeight float64 // below 3, 20 tool calls must parse
	Predicted     Predicted
	Harness       *harness.Harness // nil skips the harness probe
	APIs          []string         // API kinds the route uses (as HarnessCheckRun)
	Progress      func(string)     // one short line per sub-step; optional
}

// Check is one named Ready check.
type Check struct {
	Name   string `json:"name"` // warmup | decode | prefill | follow_up | spec_verify | harness | tool_calls | spill
	OK     bool   `json:"ok"`
	Detail string `json:"detail"` // plain words, e.g. "writing 21 tok/s at 32k (predicted 24)"
}

// Report is the outcome of Ready.
type Report struct {
	Ready  bool    `json:"ready"`
	Checks []Check `json:"checks"`
	// Failing names the first failing check ("" when Ready).
	Failing string `json:"failing,omitempty"`
	// Measured speeds, for calibration and the "we were N% optimistic" line.
	DecodeTPS      map[int]float64 `json:"decode_tps"` // depth -> tok/s
	PrefillTPS     float64         `json:"prefill_tps"`
	FollowUpTTFT   float64         `json:"follow_up_ttft_s"`
	SpecVerifySecs []float64       `json:"spec_verify_s,omitempty"` // batch 1,2,3,4
	Misses         []string        `json:"misses,omitempty"`        // e.g. "we were 34% optimistic on prompt reading"
}

// Ready measures a running engine and decides whether the setup is Ready.
func Ready(ctx context.Context, o ReadyOptions) (Report, error) {
	return Report{}, errors.New("verify: Ready not implemented")
}
