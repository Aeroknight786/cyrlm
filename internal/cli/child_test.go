package cli

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/aeroknight786/trialm/internal/harness"
)

const goldenChildEnv = "GOLDEN_CHILD_ARGS"

func TestMain(m *testing.M) {
	if raw := os.Getenv(goldenChildEnv); raw != "" {
		var args []string
		if err := json.Unmarshal([]byte(raw), &args); err != nil {
			panic(err)
		}
		harness.KnownBinaries = nil
		os.Exit(Main(context.Background(), append([]string{"trialm"}, args...), "test"))
	}
	os.Exit(m.Run())
}
