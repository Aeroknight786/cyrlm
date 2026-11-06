package hw

import (
	"context"
	"fmt"
	"runtime"
	"strings"
)

// Detect describes the current machine: OS, CPU, RAM and GPUs.
//
// It needs no admin rights, runs its probes concurrently (each external
// command is limited to 3 s, the whole call to under 5 s) and never fails
// hard: whatever could not be determined is left zero and explained in
// System.Notes. The returned System is always non-nil; the error is non-nil
// only when ctx was cancelled by the caller.
func Detect(ctx context.Context) (*System, error) {
	d := detector{r: osRunner{}, goos: runtime.GOOS, goarch: runtime.GOARCH, numCPU: runtime.NumCPU()}
	return d.detect(ctx)
}

// detector holds the injectable inputs of Detect.
type detector struct {
	r      runner
	goos   string
	goarch string
	numCPU int // fallback logical CPU count
}

func (d detector) detect(parent context.Context) (*System, error) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, detectBudget)
	defer cancel()

	s := &System{OS: d.goos, Arch: d.goarch, GPUs: []GPU{}}
	switch d.goos {
	case "darwin":
		d.detectDarwin(ctx, s)
	case "linux":
		d.detectLinux(ctx, s)
	case "windows":
		d.detectWindows(ctx, s)
	default:
		s.note("hardware detection is not implemented for %s; CPU threads come from the Go runtime and RAM/GPUs are unknown", d.goos)
	}
	d.finalize(s)
	return s, parent.Err()
}

// finalize fills fallbacks and normalizes GPUs.
func (d detector) finalize(s *System) {
	if s.CPUThreads == 0 && d.numCPU > 0 {
		s.CPUThreads = d.numCPU
	}
	if s.CPUCores == 0 && s.CPUThreads > 0 {
		s.CPUCores = s.CPUThreads
		s.note("physical CPU core count unknown; assuming one core per logical CPU (%d)", s.CPUThreads)
	}
	if s.CPUName == "" {
		s.note("CPU model name could not be detected")
	}
	if s.RAMBytes == 0 {
		s.note("total RAM could not be detected")
	}
	if s.FreeRAMBytes > s.RAMBytes && s.RAMBytes > 0 {
		s.FreeRAMBytes = s.RAMBytes
	}
	if s.GPUs == nil {
		s.GPUs = []GPU{}
	}
	for i := range s.GPUs {
		g := &s.GPUs[i]
		if g.Backends == nil {
			g.Backends = backendsFor(g.Vendor, d.goos)
		}
		if isLaptopName(g.Name) {
			g.Laptop = true
		}
	}
	sortGPUs(s.GPUs)
	if len(s.GPUs) == 0 && (d.goos == "linux" || d.goos == "windows" || d.goos == "darwin") {
		s.note("no GPU detected; predictions will assume CPU-only inference")
	}
}

func (s *System) note(format string, args ...any) {
	s.Notes = append(s.Notes, fmt.Sprintf(format, args...))
}

// firstLine returns the first non-empty trimmed line of b (for error notes).
func firstLine(b []byte) string {
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			if len(l) > 160 {
				l = l[:160] + "..."
			}
			return l
		}
	}
	return ""
}

// errDetail describes a failed probe from its output and error.
func errDetail(out []byte, err error) string {
	if l := firstLine(out); l != "" {
		return l
	}
	if err != nil {
		return err.Error()
	}
	return "no output"
}
