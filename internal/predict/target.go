package predict

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/aeroknight786/cyrlm/internal/hw"
	"github.com/aeroknight786/cyrlm/internal/specs"
)

const gib = 1 << 30

// Target is the machine a prediction is for.
type Target struct {
	// Name is a human-readable description of the machine.
	Name string
	OS   string
	Arch string
	// Device is the accelerator used (or the CPU for CPU-only).
	Device specs.Device
	// HWBackends are the accelerator's backend capabilities.
	HWBackends []string
	// GPUMemBytes is memory the accelerator can use for the model: VRAM, or
	// the unified-memory GPU budget (macOS wired limit, APU carve-out/GTT).
	GPUMemBytes float64
	// VRAMTotalBytes is the accelerator's total memory as runtimes report it
	// (used for runtime defaults such as Ollama's context tiers).
	VRAMTotalBytes float64
	// RAMBytes is system RAM.
	RAMBytes float64
	Unified  bool
	CPUOnly  bool
	Laptop   bool
	// DiskGBs is assumed sequential read speed for model loading.
	DiskGBs float64
	// Notes explain assumptions about the target.
	Notes []string
	// Synthetic is true for --device what-if targets.
	Synthetic bool
}

// TargetFromSystem builds a Target from detected hardware. measuredCPUGBs is
// an optional CPU memory-bandwidth probe result (0 = not measured).
func TargetFromSystem(sys *hw.System, measuredCPUGBs float64) Target {
	t := Target{OS: sys.OS, Arch: sys.Arch, RAMBytes: float64(sys.RAMBytes), DiskGBs: 2.5}
	if sys.OS == "darwin" {
		t.DiskGBs = 5
	}
	var gpu *hw.GPU
	for i := range sys.GPUs {
		g := &sys.GPUs[i]
		if g.Vendor == hw.VendorApple || (!g.Integrated && g.VRAMBytes >= 2*gib) {
			gpu = g
			break
		}
	}
	if gpu == nil {
		// Prefer a usable iGPU/APU over CPU-only when it has memory to use.
		for i := range sys.GPUs {
			g := &sys.GPUs[i]
			if g.Integrated && (g.VRAMBytes >= 4*gib || sys.RAMBytes >= 16*gib) {
				if _, ok := specs.LookupGPU(g.Name, g.Laptop); ok {
					gpu = g
					break
				}
			}
		}
	}
	ram := float64(sys.RAMBytes)
	switch {
	case gpu != nil && gpu.Vendor == hw.VendorApple:
		chip := sys.AppleChip
		if chip == "" {
			chip = gpu.Name
		}
		d, ok := specs.LookupApple(chip, gpu.Cores)
		if !ok {
			d = specs.Device{Name: chip, Segment: specs.ApplePro, BandwidthGBs: 150, FP16TFLOPS: 5, Note: "unknown Apple chip; spec guessed"}
		}
		t.Device = d
		t.HWBackends = []string{"metal"}
		t.Unified = true
		t.GPUMemBytes = AppleGPUBudget(ram, float64(sys.GPUWiredLimitBytes))
		t.VRAMTotalBytes = t.GPUMemBytes
		if sys.GPUWiredLimitBytes == 0 {
			t.Notes = append(t.Notes, fmt.Sprintf("macOS lets the GPU use about %.0f of %.0f GB by default (raise with sudo sysctl iogpu.wired_limit_mb)", t.GPUMemBytes/gib, ram/gib))
		}
		if gpu.Cores > 0 {
			t.Name = fmt.Sprintf("%s (%d-core GPU), %.0f GB unified memory", strings.TrimSuffix(d.Name, fmt.Sprintf(" %dc", gpu.Cores)), gpu.Cores, ram/gib)
		} else {
			chipName := d.Name
			if i := strings.LastIndex(chipName, " "); i > 0 {
				chipName = chipName[:i] // drop the assumed "<n>c" bin
			}
			t.Name = fmt.Sprintf("%s (GPU core count unknown), %.0f GB unified memory", chipName, ram/gib)
		}
	case gpu != nil:
		d, ok := specs.LookupGPU(gpu.Name, gpu.Laptop)
		if !ok {
			d = guessGPU(gpu)
			t.Notes = append(t.Notes, "unknown GPU model: memory bandwidth guessed from its memory size; run a calibration probe for a real number")
		}
		t.Device = d
		t.HWBackends = gpu.Backends
		if len(t.HWBackends) == 0 {
			t.HWBackends = defaultBackends(gpu.Vendor)
		}
		t.Laptop = gpu.Laptop || d.Segment == specs.NVLaptop
		if gpu.Integrated || gpu.Unified {
			t.Unified = true
			mem := float64(gpu.VRAMBytes)
			if mem < 2*gib {
				mem = ram / 2
				t.Notes = append(t.Notes, "integrated GPU: assuming it can use half of system RAM")
			}
			t.GPUMemBytes = mem - 1*gib
		} else {
			mem := float64(gpu.VRAMBytes)
			t.VRAMTotalBytes = mem
			if gpu.FreeVRAMBytes > 0 && float64(gpu.FreeVRAMBytes) < mem-0.4*gib {
				t.GPUMemBytes = float64(gpu.FreeVRAMBytes) - 0.3*gib
				t.Notes = append(t.Notes, fmt.Sprintf("using currently free VRAM (%.1f of %.1f GB)", float64(gpu.FreeVRAMBytes)/gib, mem/gib))
			} else {
				t.GPUMemBytes = mem - 0.4*gib
			}
		}
		mem := "VRAM"
		if t.Unified {
			mem = "shared memory"
		}
		t.Name = fmt.Sprintf("%s (%.0f GB %s), %.0f GB RAM", gpu.Name, float64(gpu.VRAMBytes)/gib, mem, ram/gib)
	default:
		t.CPUOnly = true
		if sys.AppleChip != "" {
			t.Device = specs.AppleCPU(sys.AppleChip, sys.CPUPerfCores)
		} else {
			t.Device = specs.X86CPU(sys.CPUName, sys.CPUCores, sys.CPUFlags, measuredCPUGBs)
		}
		t.HWBackends = []string{"cpu"}
		t.GPUMemBytes = ram*0.75 - 2*gib
		t.Name = fmt.Sprintf("%s (CPU only), %.0f GB RAM", sys.CPUName, ram/gib)
		if measuredCPUGBs <= 0 {
			t.Notes = append(t.Notes, "CPU-only: memory bandwidth assumed; run `cyrlm detect --probe` to measure it")
		}
	}
	if t.Device.Note != "" {
		t.Notes = append(t.Notes, t.Device.Note)
	}
	return t
}

// TierMemBytes is the memory size runtimes use to pick defaults.
func (t Target) TierMemBytes() float64 {
	if t.VRAMTotalBytes > 0 {
		return t.VRAMTotalBytes
	}
	return t.GPUMemBytes
}

// AppleGPUBudget returns the GPU-usable share of unified memory: an explicit
// iogpu.wired_limit_mb, else macOS's default working-set limit (about 2/3 of
// RAM up to 36 GB, 3/4 above; community-measured, not Apple-documented).
func AppleGPUBudget(ramBytes, wiredLimitBytes float64) float64 {
	if wiredLimitBytes > 0 {
		return wiredLimitBytes
	}
	if ramBytes <= 36*gib {
		return ramBytes * 2 / 3
	}
	return ramBytes * 3 / 4
}

func defaultBackends(vendor string) []string {
	switch vendor {
	case hw.VendorNVIDIA:
		return []string{"cuda", "vulkan"}
	case hw.VendorAMD:
		return []string{"rocm", "vulkan"}
	case hw.VendorIntel:
		return []string{"sycl", "vulkan"}
	case hw.VendorApple:
		return []string{"metal"}
	}
	return []string{"vulkan"}
}

func guessGPU(g *hw.GPU) specs.Device {
	v := float64(g.VRAMBytes) / gib
	bw := 272.0
	switch {
	case v > 24:
		bw = 1000
	case v > 16:
		bw = 936
	case v > 12:
		bw = 448
	case v > 8:
		bw = 360
	}
	seg := specs.NVDesktop
	switch g.Vendor {
	case hw.VendorAMD:
		seg = specs.AMDDiscrete
	case hw.VendorIntel:
		seg = specs.IntelArc
	}
	return specs.Device{Name: g.Name, Segment: seg, BandwidthGBs: bw, FP16TFLOPS: bw / 6, Confident: false, Note: "spec guessed"}
}

var sizeRe = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(?:gb|g)?$`)

// SyntheticSystem builds a what-if machine from a device name such as
// "RTX 4090", "Apple M4 Pro 20c", "Radeon 8060S" or "cpu", with RAM and
// (for discrete GPUs) VRAM in GB as marketed (binary GiB, as the OS
// reports them). vramGB 0 uses the device's usual size.
func SyntheticSystem(device string, ramGB, vramGB float64) (*hw.System, error) {
	if ramGB <= 0 {
		ramGB = 32
	}
	sys := &hw.System{OS: "linux", Arch: "amd64", RAMBytes: uint64(ramGB * gib), CPUName: "what-if CPU", CPUCores: 8, CPUThreads: 16, CPUFlags: []string{"avx2"}}
	low := strings.ToLower(strings.TrimSpace(device))
	if low == "cpu" || low == "" {
		return sys, nil
	}
	if strings.Contains(low, "apple") || regexp.MustCompile(`^m\d`).MatchString(low) {
		cores := 0
		if m := regexp.MustCompile(`(\d+)\s*c(ore)?\b`).FindStringSubmatch(low); m != nil {
			cores, _ = strconv.Atoi(m[1])
		}
		chip := strings.TrimSpace(regexp.MustCompile(`\d+\s*c(ore)?\b`).ReplaceAllString(device, ""))
		if !strings.HasPrefix(strings.ToLower(chip), "apple") {
			chip = "Apple " + chip
		}
		d, ok := specs.LookupApple(chip, cores)
		if !ok {
			return nil, fmt.Errorf("unknown Apple chip %q", device)
		}
		if cores == 0 {
			// pick the top bin's cores from the name for display
			fmt.Sscanf(d.Name[strings.LastIndex(d.Name, " ")+1:], "%dc", &cores)
		}
		sys.OS, sys.Arch = "darwin", "arm64"
		sys.AppleChip = chip
		sys.GPUs = []hw.GPU{{Vendor: hw.VendorApple, Name: chip, VRAMBytes: sys.RAMBytes, Unified: true, Cores: cores, Backends: []string{"metal"}}}
		return sys, nil
	}
	d, ok := specs.LookupGPU(device, strings.Contains(low, "laptop"))
	if !ok {
		return nil, fmt.Errorf("unknown device %q (try e.g. \"RTX 4090\", \"Apple M4 Pro 20c\", \"RX 7900 XTX\", \"Radeon 8060S\", \"cpu\")", device)
	}
	g := hw.GPU{Name: device, Laptop: d.Segment == specs.NVLaptop || d.Segment == specs.AMDLaptop}
	switch {
	case strings.HasPrefix(string(d.Segment), "nv"):
		g.Vendor, g.Backends = hw.VendorNVIDIA, []string{"cuda", "vulkan"}
	case strings.HasPrefix(string(d.Segment), "amd"):
		g.Vendor, g.Backends = hw.VendorAMD, []string{"rocm", "vulkan"}
	default:
		g.Vendor, g.Backends = hw.VendorIntel, []string{"sycl", "vulkan"}
	}
	switch d.Segment {
	case specs.StrixHalo, specs.AMDiGPU, specs.InteliGPU, specs.NVUnified:
		g.Integrated, g.Unified = d.Segment != specs.NVUnified, true
		if vramGB <= 0 {
			vramGB = ramGB * 0.75
		}
	default:
		if vramGB <= 0 {
			vramGB = typicalVRAM(d.Name)
		}
	}
	g.VRAMBytes = uint64(vramGB * gib)
	sys.GPUs = []hw.GPU{g}
	return sys, nil
}

// typicalVRAM returns the common memory size of a GPU model in GB.
func typicalVRAM(name string) float64 {
	n := strings.ToLower(name)
	table := []struct {
		k  string
		gb float64
	}{
		{"5090 laptop", 24}, {"5090", 32}, {"4090 laptop", 16}, {"4090", 24}, {"3090", 24}, {"7900 xtx", 24}, {"6000 ada", 48}, {"a6000", 48}, {"pro 6000", 96},
		{"5080 laptop", 16}, {"5080", 16}, {"4080", 16}, {"5070 ti laptop", 12}, {"5070 ti", 16}, {"4070 ti super", 16}, {"7900 xt", 20}, {"7900 gre", 16},
		{"9070", 16}, {"r9700", 32}, {"w7900", 48}, {"7800 xt", 16}, {"6800", 16}, {"6900", 16}, {"6950", 16}, {"a770", 16}, {"b580", 12}, {"b570", 10}, {"b60", 24},
		{"5060 ti", 16}, {"4060 ti", 8}, {"3080 ti", 12}, {"3080", 10}, {"3060", 12}, {"4070", 12}, {"5070", 12}, {"7700 xt", 12}, {"6700 xt", 12},
		{"9060 xt", 16}, {"4060", 8}, {"5060", 8}, {"3070", 8}, {"7600", 8}, {"a750", 8}, {"a580", 8},
	}
	for _, e := range table {
		if strings.Contains(n, e.k) {
			return e.gb
		}
	}
	if m := sizeRe.FindStringSubmatch(n); m != nil {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil && v >= 4 && v <= 192 {
			return v
		}
	}
	return 8
}
