package hw

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// sysctl names present on every supported Mac.
var macSysctlBase = []string{
	"hw.memsize", "machdep.cpu.brand_string", "hw.ncpu", "hw.logicalcpu",
	"hw.physicalcpu", "hw.pagesize", "hw.model",
}

// sysctl names that only exist on some Macs/versions (an unknown name makes
// sysctl print an error and exit non-zero, so they go in a separate call).
var macSysctlOptional = []string{
	"hw.perflevel0.physicalcpu", "iogpu.wired_limit_mb", "sysctl.proc_translated",
	"hw.optional.arm.FEAT_DotProd", "hw.optional.arm.FEAT_I8MM",
	"hw.optional.arm.FEAT_BF16", "hw.optional.arm.FEAT_SME",
	"hw.optional.avx1_0", "hw.optional.avx2_0", "hw.optional.avx512f", "hw.optional.fma",
}

func (d detector) detectDarwin(ctx context.Context, s *System) {
	var (
		base, opt, sp, swv, vm []byte
		baseErr, spErr         error
	)
	parallel(
		func() { base, baseErr = d.r.Run(ctx, "/usr/sbin/sysctl", macSysctlBase...) },
		func() { opt, _ = d.r.Run(ctx, "/usr/sbin/sysctl", macSysctlOptional...) },
		func() { sp, spErr = d.r.Run(ctx, "/usr/sbin/system_profiler", "SPDisplaysDataType", "-json") },
		func() { swv, _ = d.r.Run(ctx, "/usr/bin/sw_vers", "-productVersion") },
		func() { vm, _ = d.r.Run(ctx, "/usr/bin/vm_stat") },
	)

	kv := parseSysctl(string(base))
	if len(kv) == 0 {
		s.note("sysctl failed: %s", errDetail(base, baseErr))
	}
	for k, v := range parseSysctl(string(opt)) {
		kv[k] = v
	}
	s.RAMBytes, _ = strconv.ParseUint(kv["hw.memsize"], 10, 64)
	s.CPUName = kv["machdep.cpu.brand_string"]
	s.CPUThreads = atoi(firstNonEmpty(kv["hw.logicalcpu"], kv["hw.ncpu"]))
	s.CPUCores = atoi(kv["hw.physicalcpu"])
	if v := strings.TrimSpace(string(swv)); v != "" && v[0] >= '0' && v[0] <= '9' {
		s.OSVersion = "macOS " + v
	}
	if free, ok := parseVMStat(string(vm)); ok {
		s.FreeRAMBytes = free
	}
	if mb, _ := strconv.ParseUint(kv["iogpu.wired_limit_mb"], 10, 64); mb > 0 {
		s.GPUWiredLimitBytes = mb << 20
	}

	gpus, err := parseSystemProfilerDisplays(sp)
	if err != nil || len(gpus) == 0 {
		s.note("system_profiler SPDisplaysDataType failed: %s", errDetail(nil, firstErr(spErr, err)))
	}
	chip := ""
	if strings.HasPrefix(s.CPUName, "Apple ") {
		chip = s.CPUName
	}
	haveApple := false
	for i := range gpus {
		if g := &gpus[i]; g.Vendor == VendorApple {
			haveApple = true
			g.VRAMBytes = s.RAMBytes // the predictor applies the wired limit
			if chip == "" {
				chip = g.Name
			}
		}
	}
	if chip != "" && !haveApple {
		gpus = append(gpus, GPU{Vendor: VendorApple, Name: chip, VRAMBytes: s.RAMBytes, Unified: true, Source: "sysctl"})
		s.note("Apple GPU core count unknown (system_profiler unavailable)")
	}
	if chip != "" {
		s.AppleChip = chip
		s.CPUPerfCores = atoi(kv["hw.perflevel0.physicalcpu"])
	}
	s.CPUFlags = macCPUFlags(kv, chip != "" || d.goarch == "arm64")
	if kv["sysctl.proc_translated"] == "1" {
		s.note("this binary runs under Rosetta 2; the hardware is Apple Silicon (arm64) — use a native arm64 build for accurate results")
	}
	markLaptops(gpus, strings.HasPrefix(kv["hw.model"], "MacBook"))
	s.GPUs = gpus
}

func macCPUFlags(kv map[string]string, appleSilicon bool) []string {
	var flags []string
	on := func(k string) bool { return kv[k] == "1" }
	if appleSilicon {
		flags = append(flags, "neon")
		for _, f := range []struct{ key, name string }{
			{"hw.optional.arm.FEAT_DotProd", "dotprod"}, {"hw.optional.arm.FEAT_I8MM", "i8mm"},
			{"hw.optional.arm.FEAT_BF16", "bf16"}, {"hw.optional.arm.FEAT_SME", "sme"},
		} {
			if on(f.key) {
				flags = append(flags, f.name)
			}
		}
		return flags
	}
	for _, f := range []struct{ key, name string }{
		{"hw.optional.avx1_0", "avx"}, {"hw.optional.avx2_0", "avx2"},
		{"hw.optional.fma", "fma"}, {"hw.optional.avx512f", "avx512f"},
	} {
		if on(f.key) {
			flags = append(flags, f.name)
		}
	}
	return flags
}

// parseSysctl parses `sysctl name...` output ("name: value" per line).
func parseSysctl(out string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(line, ":")
		k = strings.TrimSpace(k)
		// Every sysctl name has a dot; this skips "sysctl: unknown oid ..." errors.
		if !ok || !strings.Contains(k, ".") || strings.ContainsAny(k, " \t'") {
			continue
		}
		m[k] = strings.TrimSpace(v)
	}
	return m
}

var vmPageSizeRe = regexp.MustCompile(`page size of (\d+) bytes`)

// parseVMStat returns free+inactive+speculative pages × page size from
// macOS vm_stat output.
func parseVMStat(out string) (uint64, bool) {
	pageSize := uint64(4096)
	if m := vmPageSizeRe.FindStringSubmatch(out); m != nil {
		pageSize, _ = strconv.ParseUint(m[1], 10, 64)
	}
	vals := map[string]uint64{}
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(v), "."), 10, 64)
		if err != nil {
			continue
		}
		vals[strings.Trim(k, `" `)] = n
	}
	free, ok := vals["Pages free"]
	if !ok || pageSize == 0 {
		return 0, false
	}
	return (free + vals["Pages inactive"] + vals["Pages speculative"]) * pageSize, true
}

type spDisplayItem struct {
	Name       string     `json:"_name"`
	Model      string     `json:"sppci_model"`
	Cores      flexString `json:"sppci_cores"`
	Vendor     string     `json:"spdisplays_vendor"`
	VRAM       string     `json:"spdisplays_vram"`
	VRAMShared string     `json:"spdisplays_vram_shared"`
	Bus        string     `json:"sppci_bus"`
}

// parseSystemProfilerDisplays parses `system_profiler SPDisplaysDataType
// -json`. Apple GPUs are returned Unified with VRAMBytes 0 (the caller sets
// total RAM); Intel iGPUs are Integrated+Unified with their shared VRAM;
// AMD/NVIDIA GPUs report dedicated VRAM from spdisplays_vram.
func parseSystemProfilerDisplays(data []byte) ([]GPU, error) {
	var doc struct {
		Items []spDisplayItem `json:"SPDisplaysDataType"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(data), &doc); err != nil {
		return nil, err
	}
	var gpus []GPU
	for _, it := range doc.Items {
		name := strings.TrimSpace(firstNonEmpty(it.Model, it.Name))
		if name == "" {
			continue
		}
		g := GPU{Vendor: macVendor(it.Vendor, name), Name: name, Source: "system_profiler"}
		switch g.Vendor {
		case VendorApple:
			g.Unified = true
			g.Cores = atoi(string(it.Cores))
		case VendorIntel:
			g.Integrated, g.Unified = true, true
			g.VRAMBytes = parseSizeString(firstNonEmpty(it.VRAMShared, it.VRAM))
		default:
			g.VRAMBytes = parseSizeString(firstNonEmpty(it.VRAM, it.VRAMShared))
		}
		gpus = append(gpus, g)
	}
	return gpus, nil
}

func macVendor(v, name string) string {
	l := strings.ToLower(v)
	switch {
	case strings.Contains(l, "apple"):
		return VendorApple
	case strings.Contains(l, "amd") || strings.Contains(l, "ati"):
		return VendorAMD
	case strings.Contains(l, "intel"):
		return VendorIntel
	case strings.Contains(l, "nvidia"):
		return VendorNVIDIA
	}
	return vendorFromName(name)
}

var sizeRe = regexp.MustCompile(`(?i)([\d.]+)\s*(KB|MB|GB|TB)\b`)

// parseSizeString parses "8 GB" / "1536 MB" (binary units, as macOS reports VRAM).
func parseSizeString(s string) uint64 {
	m := sizeRe.FindStringSubmatch(s)
	if m == nil {
		return 0
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0
	}
	shift := map[string]uint{"KB": 10, "MB": 20, "GB": 30, "TB": 40}[strings.ToUpper(m[2])]
	return uint64(v * float64(uint64(1)<<shift))
}

// flexString accepts a JSON string or number.
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*f = flexString(s)
		return nil
	}
	*f = flexString(strings.Trim(string(b), `"`))
	if string(b) == "null" {
		*f = ""
	}
	return nil
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}
