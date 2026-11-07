package tui

import (
	"strings"
	"testing"

	"github.com/aeroknight786/trialm/internal/hw"
)

func TestLauncherWarnsInLowPowerMode(t *testing.T) {
	m := fixture(t)
	m.stack = []Screen{Home}
	m.width = 120
	if strings.Contains(m.launcher(), "Low Power Mode") {
		t.Fatal("warning shown at full power")
	}
	m.detection.System = &hw.System{Power: hw.Power{LowPower: true}}
	if got := m.launcher(); !strings.Contains(got, "Low Power Mode is on") || !strings.Contains(got, "System Settings → Battery") {
		t.Fatalf("no Low Power warning:\n%s", got)
	}
}
