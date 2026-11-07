package hw

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
)

// Power is the Mac's power state. Low Power Mode caps the GPU: Qwen3.5 9B on
// an M5 wrote 6-8 tok/s with it on and 21 with it off, so predictions,
// calibration and Ready measurements need to know.
type Power struct {
	LowPower  bool `json:"low_power,omitempty"`
	OnBattery bool `json:"on_battery,omitempty"`
}

// MacPower reads pmset (fast, no privileges). Zero on other systems.
func MacPower(ctx context.Context) Power {
	if runtime.GOOS != "darwin" {
		return Power{}
	}
	g, _ := exec.CommandContext(ctx, "/usr/bin/pmset", "-g").Output()
	b, _ := exec.CommandContext(ctx, "/usr/bin/pmset", "-g", "batt").Output()
	return parsePower(string(g), string(b))
}

func parsePower(settings, batt string) Power {
	p := Power{OnBattery: strings.Contains(batt, "'Battery Power'")}
	for _, line := range strings.Split(settings, "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[0] == "lowpowermode" {
			p.LowPower = f[1] == "1"
		}
	}
	return p
}
