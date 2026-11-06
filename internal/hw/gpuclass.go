package hw

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// backendsFor returns the compute backends a vendor's hardware can use on goos.
// It describes capability only; the predictor decides what is practical.
func backendsFor(vendor, goos string) []string {
	if goos == "darwin" {
		return []string{"metal"} // Apple, AMD and Intel GPUs on macOS all go through Metal
	}
	switch vendor {
	case VendorNVIDIA:
		return []string{"cuda", "vulkan"}
	case VendorAMD:
		return []string{"rocm", "vulkan"}
	case VendorIntel:
		return []string{"sycl", "vulkan"}
	case VendorApple:
		return []string{"metal"}
	}
	return nil
}

var laptopNameRe = regexp.MustCompile(`(?i)\b(laptop|mobile|max-q)\b|\b(rx|pro|arc)\s+[a-z]?\d{3,4}m\b`)

// isLaptopName reports whether a GPU name marks a mobile part.
func isLaptopName(name string) bool { return laptopNameRe.MatchString(name) }

// cleanTM strips trademark decorations for matching ("Arc(TM) A770" -> "Arc A770").
func cleanTM(s string) string {
	r := strings.NewReplacer("(R)", "", "(r)", "", "(TM)", "", "(tm)", "", "®", "", "™", "")
	return strings.Join(strings.Fields(r.Replace(s)), " ")
}

var amdAPURe = regexp.MustCompile(`(?i)\bradeon\s+(\d{3,4}[ms]\s+)?graphics\b|\bradeon\s+\d{3}m\b|\bradeon\s+\d{4}s\b|` +
	`\bvega\s+\d+\s+(mobile\s+)?graphics\b|` +
	`\b(strix|phoenix\d?|hawk point|krackan|rembrandt|renoir|cezanne|lucienne|barcelo|raphael|granite ridge|mendocino|van gogh|picasso|raven|dali|pollock)\b`)

// amdAPUDeviceIDs are PCI device IDs of AMD APU graphics blocks.
var amdAPUDeviceIDs = map[uint16]bool{
	0x15dd: true, // Raven Ridge
	0x15d8: true, // Picasso
	0x1636: true, // Renoir
	0x1638: true, // Cezanne
	0x164c: true, // Lucienne
	0x15e7: true, // Barcelo
	0x163f: true, // Van Gogh (Steam Deck)
	0x1681: true, // Rembrandt (680M/660M)
	0x164e: true, // Raphael (Ryzen 7000 desktop iGPU)
	0x13c0: true, // Granite Ridge (Ryzen 9000 desktop iGPU)
	0x1506: true, // Mendocino
	0x15bf: true, // Phoenix1 (780M/760M)
	0x15c8: true, // Phoenix2 (740M)
	0x150e: true, // Strix Point (890M/880M)
	0x1114: true, // Krackan Point (860M/840M)
	0x1586: true, // Strix Halo (8060S/8050S/8040S)
}

var amdDiscreteRe = regexp.MustCompile(`(?i)\b(rx|pro|instinct|firepro|vii|w\d{4}|mi\d{2,3}x?)\b`)

// isAMDAPU decides whether an AMD GPU is an integrated APU graphics block.
// Strong signals (known device ID or name) win; otherwise a small VRAM
// carve-out backed by a larger GTT on a part not named like a dGPU.
func isAMDAPU(name string, deviceID uint16, vram, gtt uint64) bool {
	if amdAPUDeviceIDs[deviceID] || amdAPURe.MatchString(cleanTM(name)) {
		return true
	}
	if amdDiscreteRe.MatchString(cleanTM(name)) {
		return false
	}
	return vram > 0 && vram <= 4<<30 && gtt > vram
}

var intelDiscreteRe = regexp.MustCompile(`(?i)\barc\s+(pro\s+)?[ab]\d{2,3}[a-z]?\b|data center gpu|\biris xe max\b|\bdg1\b`)

// isIntelDiscrete reports whether an Intel GPU name is a discrete Arc/Flex/Max
// part. "Arc Graphics" and "Arc 140V" are Meteor/Lunar Lake iGPUs.
func isIntelDiscrete(name string) bool { return intelDiscreteRe.MatchString(cleanTM(name)) }

var intelArcVRAM = []struct {
	re  *regexp.Regexp
	gib uint64
}{
	{regexp.MustCompile(`(?i)\bpro\s+b60\b`), 24},
	{regexp.MustCompile(`(?i)\bpro\s+b50\b`), 16},
	{regexp.MustCompile(`(?i)\bpro\s+a60m?\b`), 12},
	{regexp.MustCompile(`(?i)\bpro\s+a[345]0m?\b`), 6},
	{regexp.MustCompile(`(?i)\bb580\b`), 12},
	{regexp.MustCompile(`(?i)\bb570\b`), 10},
	{regexp.MustCompile(`(?i)\ba770m?\b`), 16},
	{regexp.MustCompile(`(?i)\ba730m\b`), 12},
	{regexp.MustCompile(`(?i)\ba(750|580|570m|550m)\b`), 8},
	{regexp.MustCompile(`(?i)\ba380\b`), 6},
	{regexp.MustCompile(`(?i)\ba(310|370m|350m)\b`), 4},
	{regexp.MustCompile(`(?i)\biris xe max\b`), 4},
}

// intelArcVRAMByName infers dedicated memory of a discrete Intel GPU from its
// model name (Linux i915/xe do not expose it in sysfs). 0 = unknown.
func intelArcVRAMByName(name string) uint64 {
	n := cleanTM(name)
	for _, e := range intelArcVRAM {
		if e.re.MatchString(n) {
			return e.gib << 30
		}
	}
	return 0
}

var nvidiaUnifiedRe = regexp.MustCompile(`(?i)\bgb10\b|\bthor\b|\borin\b|\bjetson\b|\bspark\b`)

// sortGPUs orders discrete GPUs first, then by addressable memory, descending.
func sortGPUs(g []GPU) {
	sort.SliceStable(g, func(i, j int) bool {
		if g[i].Integrated != g[j].Integrated {
			return !g[i].Integrated
		}
		return g[i].VRAMBytes > g[j].VRAMBytes
	})
}

// markLaptops sets Laptop from the GPU name, and for discrete GPUs on a
// machine with a battery.
func markLaptops(gpus []GPU, battery bool) {
	for i := range gpus {
		g := &gpus[i]
		if isLaptopName(g.Name) || (battery && !g.Integrated && !g.Unified) {
			g.Laptop = true
		}
	}
}

func gib(b uint64) string { return fmt.Sprintf("%.1f GiB", float64(b)/(1<<30)) }

func vendorDisplayName(v string) string {
	switch v {
	case VendorNVIDIA:
		return "NVIDIA"
	case VendorAMD:
		return "AMD"
	case VendorIntel:
		return "Intel"
	case VendorApple:
		return "Apple"
	}
	return ""
}

var intelArcWordRe = regexp.MustCompile(`\barc\b`)

// vendorFromName guesses the vendor from a marketing name.
func vendorFromName(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "nvidia") || strings.Contains(n, "geforce") || strings.Contains(n, "quadro") || strings.Contains(n, "tesla"):
		return VendorNVIDIA
	case strings.Contains(n, "amd") || strings.Contains(n, "radeon") || strings.Contains(n, "ati "):
		return VendorAMD
	case strings.Contains(n, "intel") || strings.Contains(n, "iris") || strings.Contains(n, "uhd graphics") || intelArcWordRe.MatchString(n):
		return VendorIntel
	case strings.HasPrefix(n, "apple"):
		return VendorApple
	}
	return VendorOther
}

func vendorFromPCIID(id uint16) string {
	switch id {
	case 0x10de:
		return VendorNVIDIA
	case 0x1002, 0x1022:
		return VendorAMD
	case 0x8086:
		return VendorIntel
	case 0x106b:
		return VendorApple
	}
	return VendorOther
}
