// Package specs holds published device specifications (peak memory
// bandwidth and dense FP16 matrix throughput) and maps detected device names
// to them. Values come from vendor spec sheets; they were assembled and used
// to fit the predictor's efficiency factors against 565 public llama.cpp
// benchmark results (see research/).
package specs

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Segment identifies a hardware class with its own accuracy profile.
type Segment string

// Hardware segments.
const (
	AppleBase   Segment = "apple_base"
	ApplePro    Segment = "apple_pro"
	AppleMax    Segment = "apple_max"
	AppleUltra  Segment = "apple_ultra"
	NVDesktop   Segment = "nv_desktop"
	NVLaptop    Segment = "nv_laptop"
	NVUnified   Segment = "nv_unified"
	AMDDiscrete Segment = "amd_dgpu"
	AMDLaptop   Segment = "amd_laptop"
	StrixHalo   Segment = "amd_strix_halo"
	AMDiGPU     Segment = "amd_igpu"
	IntelArc    Segment = "intel_arc"
	InteliGPU   Segment = "intel_igpu"
	CPUApple    Segment = "cpu_apple"
	CPUx86      Segment = "cpu_x86"
	CPUOther    Segment = "cpu_other"
)

// Device is one accelerator (or CPU) specification.
type Device struct {
	// Name is the canonical display name, e.g. "RTX 4090", "Apple M4 Pro 20c".
	Name    string
	Segment Segment
	// BandwidthGBs is peak memory bandwidth in GB/s (1e9 bytes).
	BandwidthGBs float64
	// FP16TFLOPS is dense FP16 matrix throughput (tensor cores / XMX / WMMA
	// where present; Apple M1–M4 FP32 vector rate; CPUs FP32 FMA peak).
	FP16TFLOPS float64
	// Confident is false when the memory configuration had to be assumed
	// (iGPUs, laptops with unknown TGP, new chips with little data).
	Confident bool
	// Note explains assumptions.
	Note string
}

type entry struct {
	keys []string // lowercase substrings that must all be present
	dev  Device
}

var table []entry

func add(name string, seg Segment, bw, tf float64, confident bool, note string, keys ...string) {
	if len(keys) == 0 {
		keys = []string{strings.ToLower(name)}
	}
	table = append(table, entry{keys: keys, dev: Device{Name: name, Segment: seg, BandwidthGBs: bw, FP16TFLOPS: tf, Confident: confident, Note: note}})
}

func init() {
	// NVIDIA desktop (FP16 tensor dense, FP16 accumulate).
	for _, d := range []struct {
		n      string
		bw, tf float64
	}{
		{"RTX 2060 Super", 448, 57.4}, {"RTX 2060", 336, 51.6}, {"RTX 2070 Super", 448, 72.5}, {"RTX 2070", 448, 59.7},
		{"RTX 2080 Super", 496, 89.2}, {"RTX 2080 Ti", 616, 107.6}, {"RTX 2080", 448, 80.5},
		{"RTX 3060 Ti", 448, 64.8}, {"RTX 3060", 360, 51.2}, {"RTX 3070 Ti", 608, 87.0}, {"RTX 3070", 448, 81.3},
		{"RTX 3080 Ti", 912, 136.4}, {"RTX 3080", 760, 119.1}, {"RTX 3090 Ti", 1008, 160.0}, {"RTX 3090", 936, 142.3},
		{"RTX 4060 Ti", 288, 88.3}, {"RTX 4060", 272, 60.6}, {"RTX 4070 Ti Super", 672, 176.4}, {"RTX 4070 Ti", 504, 160.4},
		{"RTX 4070 Super", 504, 141.9}, {"RTX 4070", 504, 116.8}, {"RTX 4080 Super", 736, 208.9}, {"RTX 4080", 717, 194.9},
		{"RTX 4090", 1008, 330.3},
		{"RTX 5060 Ti", 448, 94.8}, {"RTX 5060", 448, 76.0}, {"RTX 5070 Ti", 896, 175.8}, {"RTX 5070", 672, 123.5},
		{"RTX 5080", 960, 225.1}, {"RTX 5090", 1792, 419.0},
		{"RTX A6000", 768, 154.8}, {"RTX 6000 Ada", 960, 364.2}, {"RTX PRO 6000", 1792, 503.8},
		{"Tesla T4", 320, 65.0},
	} {
		add(d.n, NVDesktop, d.bw, d.tf, true, "")
	}
	// NVIDIA laptop (reference memory config; TGP/clocks vary by laptop).
	for _, d := range []struct {
		n      string
		bw, tf float64
	}{
		{"RTX 5090 Laptop", 896, 228.0}, {"RTX 5080 Laptop", 896, 174.0}, {"RTX 5070 Ti Laptop", 672, 118.0},
		{"RTX 5070 Laptop", 384, 92.0}, {"RTX 5060 Laptop", 384, 76.0},
		{"RTX 4090 Laptop", 576, 172.0}, {"RTX 4080 Laptop", 432, 124.0}, {"RTX 4070 Laptop", 256, 64.0},
		{"RTX 4060 Laptop", 256, 58.3}, {"RTX 4050 Laptop", 192, 48.5},
		{"RTX 3080 Ti Laptop", 512, 95.0}, {"RTX 3080 Laptop", 448, 80.0}, {"RTX 3070 Ti Laptop", 448, 72.0},
		{"RTX 3070 Laptop", 448, 64.0}, {"RTX 3060 Laptop", 336, 52.0}, {"RTX 3050 Laptop", 192, 33.0},
	} {
		base := strings.ToLower(strings.TrimSuffix(d.n, " Laptop"))
		add(d.n, NVLaptop, d.bw, d.tf, false, "laptop GPU: power limit (TGP) and memory clock vary by model; run a calibration probe", base, "laptop")
	}
	// NVIDIA unified.
	add("DGX Spark (GB10)", NVUnified, 273, 62.5, false, "", "gb10")
	add("Jetson AGX Thor", NVUnified, 273, 62.5, false, "", "thor")

	// AMD discrete.
	for _, d := range []struct {
		n      string
		bw, tf float64
	}{
		{"RX 6600 XT", 256, 21.2}, {"RX 6600", 224, 17.9}, {"RX 6700 XT", 384, 26.4}, {"RX 6750 XT", 432, 26.6},
		{"RX 6800 XT", 512, 41.5}, {"RX 6800", 512, 32.3}, {"RX 6900 XT", 512, 46.1}, {"RX 6950 XT", 576, 47.3},
		{"RX 7600 XT", 288, 45.1}, {"RX 7600", 288, 43.5}, {"RX 7700 XT", 432, 70.3}, {"RX 7800 XT", 624, 74.6},
		{"RX 7900 GRE", 576, 91.9}, {"RX 7900 XTX", 960, 122.8}, {"RX 7900 XT", 800, 103.2},
		{"RX 9060 XT", 320, 102.6}, {"RX 9070 XT", 640, 194.7}, {"RX 9070", 640, 144.5},
		{"Radeon AI PRO R9700", 640, 191.3}, {"Radeon PRO W7900", 864, 122.6},
	} {
		add(d.n, AMDDiscrete, d.bw, d.tf, true, "")
	}
	add("RX 6800M", AMDLaptop, 384, 23.6, false, "laptop GPU", "6800m")
	add("RX 7600M XT", AMDLaptop, 288, 32.0, false, "laptop GPU", "7600m")

	// AMD APUs (unified memory; bandwidth depends on the installed RAM).
	add("Ryzen AI Max+ 395 / Radeon 8060S", StrixHalo, 256, 59.4, true, "LPDDR5X-8000 256-bit", "8060s")
	add("Ryzen AI Max 390 / Radeon 8050S", StrixHalo, 256, 44.5, true, "LPDDR5X-8000 256-bit", "8050s")
	add("Ryzen AI Max (Strix Halo)", StrixHalo, 256, 59.4, false, "assumed Radeon 8060S", "ryzen ai max")
	add("Radeon 890M", AMDiGPU, 120, 23.8, false, "LPDDR5X-7500 assumed", "890m")
	add("Radeon 880M", AMDiGPU, 120, 17.8, false, "LPDDR5X-7500 assumed", "880m")
	add("Radeon 780M", AMDiGPU, 89.6, 16.6, false, "DDR5-5600 dual-channel assumed", "780m")
	add("Radeon 760M", AMDiGPU, 89.6, 11.1, false, "DDR5-5600 dual-channel assumed", "760m")
	add("Radeon 680M", AMDiGPU, 76.8, 6.8, false, "DDR5-4800 dual-channel assumed", "680m")

	// Intel.
	for _, d := range []struct {
		n      string
		bw, tf float64
	}{
		{"Arc A580", 512, 98.0}, {"Arc A750", 512, 117.0}, {"Arc A770", 560, 137.6}, {"Arc B570", 380, 101.5},
		{"Arc B580", 456, 116.5}, {"Arc Pro B60", 456, 98.5},
	} {
		add(d.n, IntelArc, d.bw, d.tf, true, "")
	}
	add("Arc A770M", IntelArc, 512, 105.0, false, "laptop GPU", "a770m")
	add("Arc 140V (Lunar Lake)", InteliGPU, 136.5, 33.5, false, "LPDDR5X-8533 assumed", "140v")
	add("Arc 130V (Lunar Lake)", InteliGPU, 136.5, 28.0, false, "LPDDR5X-8533 assumed", "130v")
	add("Arc B390 (Panther Lake)", InteliGPU, 153.6, 60.0, false, "LPDDR5X-9600 assumed", "b390")
	add("Intel Arc Graphics (Meteor/Arrow Lake)", InteliGPU, 119.5, 9.0, false, "LPDDR5X-7467 assumed", "intel arc graphics")
	add("Intel Iris Xe", InteliGPU, 68.3, 4.2, false, "LPDDR4X-4266 assumed", "iris")
	add("Intel UHD Graphics", InteliGPU, 51.2, 1.0, false, "DDR4-3200 assumed", "uhd")

	// Longest keys first so "rtx 4070 ti super" wins over "rtx 4070".
	sort.SliceStable(table, func(i, j int) bool {
		return keyLen(table[i].keys) > keyLen(table[j].keys)
	})
}

func keyLen(k []string) int {
	n := 0
	for _, s := range k {
		n += len(s)
	}
	return n
}

var spaces = regexp.MustCompile(`\s+`)

func normalize(name string) string {
	s := strings.ToLower(name)
	for _, r := range []string{"nvidia", "geforce", "amd", "(tm)", "(r)", "®", "™", "graphics card"} {
		s = strings.ReplaceAll(s, r, " ")
	}
	s = strings.ReplaceAll(s, "-", " ")
	return strings.TrimSpace(spaces.ReplaceAllString(s, " "))
}

// LookupGPU maps a detected GPU name to a spec. laptop forces the laptop
// variant when the platform says so even if the name doesn't.
func LookupGPU(name string, laptop bool) (Device, bool) {
	s := normalize(name)
	if laptop && !strings.Contains(s, "laptop") {
		s += " laptop"
	}
	for _, e := range table {
		ok := true
		for _, k := range e.keys {
			if !strings.Contains(s, k) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		// Avoid matching a desktop entry for a laptop part.
		if e.dev.Segment == NVDesktop && strings.Contains(s, "laptop") {
			continue
		}
		return e.dev, true
	}
	return Device{}, false
}

// Apple chip table: bandwidth and GPU-core bins. TFLOPS = cores × 128 ALUs
// × 2 × clock (FP32 vector; M5 uses 4× for its GPU neural accelerators,
// an assumption with little benchmark data so far).
type appleBin struct {
	cores int
	bw    float64
}

type appleChip struct {
	seg   Segment
	clock float64
	mult  float64
	bins  []appleBin
}

var appleChips = map[string]appleChip{
	"m1":       {AppleBase, 1.278, 1, []appleBin{{7, 68.25}, {8, 68.25}}},
	"m1 pro":   {ApplePro, 1.296, 1, []appleBin{{14, 200}, {16, 200}}},
	"m1 max":   {AppleMax, 1.296, 1, []appleBin{{24, 400}, {32, 400}}},
	"m1 ultra": {AppleUltra, 1.296, 1, []appleBin{{48, 800}, {64, 800}}},
	"m2":       {AppleBase, 1.398, 1, []appleBin{{8, 100}, {10, 100}}},
	"m2 pro":   {ApplePro, 1.398, 1, []appleBin{{16, 200}, {19, 200}}},
	"m2 max":   {AppleMax, 1.398, 1, []appleBin{{30, 400}, {38, 400}}},
	"m2 ultra": {AppleUltra, 1.398, 1, []appleBin{{60, 800}, {76, 800}}},
	"m3":       {AppleBase, 1.38, 1, []appleBin{{8, 100}, {10, 100}}},
	"m3 pro":   {ApplePro, 1.38, 1, []appleBin{{14, 150}, {18, 150}}},
	"m3 max":   {AppleMax, 1.38, 1, []appleBin{{30, 300}, {40, 400}}},
	"m3 ultra": {AppleUltra, 1.38, 1, []appleBin{{60, 819}, {80, 819}}},
	"m4":       {AppleBase, 1.578, 1, []appleBin{{8, 120}, {10, 120}}},
	"m4 pro":   {ApplePro, 1.578, 1, []appleBin{{16, 273}, {20, 273}}},
	"m4 max":   {AppleMax, 1.578, 1, []appleBin{{32, 410}, {40, 546}}},
	"m5":       {AppleBase, 1.6, 4, []appleBin{{10, 153.6}}},
	"m5 pro":   {ApplePro, 1.6, 4, []appleBin{{16, 307}, {20, 307}}},
	"m5 max":   {AppleMax, 1.6, 4, []appleBin{{32, 460}, {40, 614}}},
	"m5 ultra": {AppleUltra, 1.6, 4, []appleBin{{64, 1228}, {80, 1228}}},
}

var appleRe = regexp.MustCompile(`m(\d+)(?:\s+(pro|max|ultra))?`)

// LookupApple maps an Apple chip name ("Apple M4 Pro") and GPU core count
// (0 = unknown) to a spec. With unknown cores the lower bin is used and the
// result is marked not confident.
func LookupApple(chip string, gpuCores int) (Device, bool) {
	m := appleRe.FindStringSubmatch(strings.ToLower(chip))
	if m == nil {
		return Device{}, false
	}
	key := "m" + m[1]
	if m[2] != "" {
		key += " " + m[2]
	}
	c, ok := appleChips[key]
	if !ok {
		return Device{}, false
	}
	bin := c.bins[0]
	confident := gpuCores > 0
	if gpuCores > 0 {
		best := c.bins[0]
		for _, b := range c.bins {
			if b.cores <= gpuCores {
				best = b
			}
		}
		bin = best
	}
	cores := bin.cores
	if gpuCores > 0 {
		cores = gpuCores
	}
	note := ""
	if c.mult != 1 {
		confident = false
		note = "M5-generation matmul throughput assumed (GPU neural accelerators); little benchmark data yet"
	}
	if gpuCores == 0 {
		note = strings.TrimSpace(note + " GPU core count unknown; assumed lowest bin")
	}
	title := strings.ToUpper(key[:1]) + key[1:]
	if i := strings.Index(title, " "); i > 0 {
		title = title[:i] + " " + strings.ToUpper(title[i+1:i+2]) + title[i+2:]
	}
	return Device{
		Name:         "Apple " + title + " " + strconv.Itoa(cores) + "c",
		Segment:      c.seg,
		BandwidthGBs: bin.bw,
		FP16TFLOPS:   float64(cores) * 128 * 2 * c.clock * c.mult / 1000,
		Confident:    confident,
		Note:         note,
	}, true
}

// AppleCPU returns the spec used for CPU-only inference on Apple Silicon.
// The CPU cluster reaches only a fraction of SoC bandwidth; the predictor's
// CPU efficiency factor accounts for that.
func AppleCPU(chip string, perfCores int) Device {
	d, ok := LookupApple(chip, 0)
	bw := 100.0
	if ok {
		bw = d.BandwidthGBs
	}
	if perfCores <= 0 {
		perfCores = 4
	}
	return Device{
		Name:         strings.TrimSpace(chip) + " CPU",
		Segment:      CPUApple,
		BandwidthGBs: bw,
		FP16TFLOPS:   float64(perfCores) * 3.5 * 32 / 1000,
		Confident:    false,
		Note:         "CPU-only on Apple Silicon is rarely competitive with Metal",
	}
}

// modernX86 reports whether a CPU name belongs to a family with AVX2
// (Intel Haswell and later, AMD Zen and later).
func modernX86(name string) bool {
	n := strings.ToLower(name)
	for _, k := range []string{"ryzen", "epyc", "threadripper", "core(tm) i", "core i", "core ultra", "xeon"} {
		if strings.Contains(n, k) {
			return true
		}
	}
	return false
}

// X86CPU returns an estimated spec for CPU-only inference. measuredGBs > 0
// (from the bandwidth probe) replaces the assumption.
func X86CPU(name string, cores int, flags []string, measuredGBs float64) Device {
	fpc := 16.0 // FP32 FLOPs/cycle/core without AVX2
	if len(flags) == 0 && modernX86(name) {
		// Flags unknown (e.g. Windows): every x86 CPU family below has
		// AVX2; AVX-512 is not assumed.
		flags = []string{"avx2"}
	}
	for _, f := range flags {
		switch f {
		case "avx512f":
			fpc = 64
		case "avx2":
			if fpc < 32 {
				fpc = 32
			}
		case "neon":
			if fpc < 16 {
				fpc = 16
			}
		}
	}
	if cores <= 0 {
		cores = 4
	}
	d := Device{
		Name:       strings.TrimSpace(name),
		Segment:    CPUx86,
		FP16TFLOPS: float64(cores) * 3.5 * fpc / 1000,
		Confident:  false,
	}
	if measuredGBs > 0 {
		d.BandwidthGBs = measuredGBs
		d.Note = "bandwidth measured by probe"
	} else {
		d.BandwidthGBs = 60
		d.Note = "memory bandwidth assumed (~dual-channel DDR4/DDR5); run with --probe to measure"
	}
	return d
}
