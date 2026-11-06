package hw

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ---- Linux orchestration -------------------------------------------------

var nvidiaSMIArgs = []string{
	"--query-gpu=name,memory.total,memory.free,driver_version",
	"--format=csv,noheader,nounits",
}

var amdgpuIDsPaths = []string{
	"/usr/share/libdrm/amdgpu.ids",
	"/opt/amdgpu/share/libdrm/amdgpu.ids",
	"/usr/local/share/libdrm/amdgpu.ids",
}

const linuxDRMRoot = "/sys/class/drm"

func (d detector) detectLinux(ctx context.Context, s *System) {
	var (
		meminfo, cpuinfo, osRel, kernel []byte
		memErr, cpuErr                  error
		smiPath                         string
		smiOut                          []byte
		smiErr                          error
		cards                           []drmCard
		pci                             map[string]pciName
		amdIDs                          map[uint32]string
		battery                         bool
	)
	parallel(
		func() { meminfo, memErr = d.r.ReadFile("/proc/meminfo") },
		func() { cpuinfo, cpuErr = d.r.ReadFile("/proc/cpuinfo") },
		func() {
			var err error
			if osRel, err = d.r.ReadFile("/etc/os-release"); err != nil {
				osRel, _ = d.r.ReadFile("/usr/lib/os-release")
			}
		},
		func() { kernel, _ = d.r.ReadFile("/proc/sys/kernel/osrelease") },
		func() {
			smiPath = d.findTool("nvidia-smi", "/usr/lib/wsl/lib/nvidia-smi", "/usr/bin/nvidia-smi")
			if smiPath != "" {
				smiOut, smiErr = d.r.Run(ctx, smiPath, nvidiaSMIArgs...)
			}
		},
		func() {
			cards = scanDRM(d.r, linuxDRMRoot)
			var needNames, haveAMD bool
			for _, c := range cards {
				v := vendorFromPCIID(c.VendorID)
				needNames = needNames || (v != VendorOther && c.ProductName == "")
				haveAMD = haveAMD || v == VendorAMD
			}
			if needNames {
				if p := d.findTool("lspci", "/usr/sbin/lspci", "/sbin/lspci"); p != "" {
					out, _ := d.r.Run(ctx, p, "-D", "-mm", "-nn")
					pci = parseLspciMM(string(out))
				}
			}
			if haveAMD {
				for _, f := range amdgpuIDsPaths {
					if b, err := d.r.ReadFile(f); err == nil {
						amdIDs = parseAMDGPUIDs(string(b))
						break
					}
				}
			}
		},
		func() {
			m, _ := d.r.Glob("/sys/class/power_supply/BAT*")
			battery = len(m) > 0
		},
	)

	// Memory.
	if mi, ok := parseMeminfo(string(meminfo)); ok {
		s.RAMBytes, s.FreeRAMBytes = mi.Total, mi.Available
	} else {
		s.note("could not read /proc/meminfo: %s", errOr(memErr, "unrecognized format"))
	}

	// CPU.
	ci := parseCPUInfo(string(cpuinfo))
	if cpuErr != nil {
		s.note("could not read /proc/cpuinfo: %v", cpuErr)
	}
	s.CPUName, s.CPUCores, s.CPUThreads, s.CPUFlags = ci.Name, ci.Cores, ci.Threads, ci.Flags
	if d.goarch == "arm64" && !contains(s.CPUFlags, "neon") {
		s.CPUFlags = append([]string{"neon"}, s.CPUFlags...)
	}
	if s.CPUThreads == 0 {
		if p := d.findTool("nproc"); p != "" {
			out, _ := d.r.Run(ctx, p)
			s.CPUThreads, _ = strconv.Atoi(strings.TrimSpace(string(out)))
		}
	}

	// OS.
	krel := strings.TrimSpace(string(kernel))
	s.OSVersion = parseOSRelease(string(osRel))
	if s.OSVersion == "" && krel != "" {
		s.OSVersion = "Linux " + krel
	}
	if wsl := strings.Contains(strings.ToLower(krel), "microsoft"); wsl {
		s.OSVersion = strings.TrimSpace(s.OSVersion + " (WSL2)")
		s.note("running under WSL2: NVIDIA GPUs are visible through nvidia-smi; AMD/Intel GPU details are not exposed in sysfs here")
	}

	// GPUs: nvidia-smi first, then sysfs DRM for everything else.
	var gpus []GPU
	haveSMI := false
	if smiPath != "" {
		if g := parseNvidiaSMI(string(smiOut)); len(g) > 0 {
			haveSMI = true
			gpus = append(gpus, g...)
		} else {
			s.note("nvidia-smi is installed but reported no usable GPU: %s", errDetail(smiOut, smiErr))
		}
	}
	for i := range gpus {
		if g := &gpus[i]; g.Unified && g.VRAMBytes == 0 && s.RAMBytes > 0 {
			g.VRAMBytes = s.RAMBytes
			s.note("%s uses unified memory (nvidia-smi reports no dedicated VRAM); reporting total RAM %s as GPU-addressable", g.Name, gib(s.RAMBytes))
		}
	}
	dg, notes := linuxGPUsFromDRM(cards, pci, amdIDs, s.RAMBytes, haveSMI)
	gpus = append(gpus, dg...)
	s.Notes = append(s.Notes, notes...)
	markLaptops(gpus, battery)
	s.GPUs = gpus
}

// findTool returns name's path on PATH, else the first existing candidate.
func (d detector) findTool(name string, candidates ...string) string {
	if p, err := d.r.LookPath(name); err == nil {
		return p
	}
	for _, c := range candidates {
		if c != "" && d.r.Exists(c) {
			return c
		}
	}
	return ""
}

func errOr(err error, fallback string) string {
	if err != nil {
		return err.Error()
	}
	return fallback
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// ---- nvidia-smi -----------------------------------------------------------

// parseNvidiaSMI parses the output of
// `nvidia-smi --query-gpu=name,memory.total,memory.free,driver_version
// --format=csv,noheader,nounits` (memory in MiB). Lines that are not GPU
// records (error messages) are ignored. A device that reports no dedicated
// memory and is a known unified part (GB10, Jetson/Thor) is marked Unified
// with VRAMBytes 0; the orchestrator fills in system RAM.
func parseNvidiaSMI(out string) []GPU {
	var gpus []GPU
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimSpace(line), ",")
		if len(f) < 4 {
			continue
		}
		n := len(f)
		name := strings.TrimSpace(strings.Join(f[:n-3], ","))
		total, ok1 := smiMiB(f[n-3])
		free, ok2 := smiMiB(f[n-2])
		if name == "" || !ok1 || !ok2 {
			continue
		}
		g := GPU{
			Vendor:        VendorNVIDIA,
			Name:          name,
			VRAMBytes:     total,
			FreeVRAMBytes: free,
			Driver:        smiValue(f[n-1]),
			Source:        "nvidia-smi",
		}
		if nvidiaUnifiedRe.MatchString(name) {
			g.Unified = true
		}
		gpus = append(gpus, g)
	}
	return gpus
}

func isNA(s string) bool {
	switch strings.ToLower(strings.Trim(strings.TrimSpace(s), "[]")) {
	case "n/a", "not supported", "unknown error", "insufficient permissions":
		return true
	}
	return false
}

func smiValue(s string) string {
	if s = strings.TrimSpace(s); isNA(s) {
		return ""
	}
	return s
}

func smiMiB(s string) (uint64, bool) {
	s = strings.TrimSpace(s)
	if isNA(s) {
		return 0, true
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v < 0 {
		return 0, false
	}
	return uint64(v * (1 << 20)), true
}

// ---- /proc ----------------------------------------------------------------

type memInfo struct{ Total, Available uint64 }

// parseMeminfo parses /proc/meminfo (values in kB) into bytes.
func parseMeminfo(data string) (memInfo, bool) {
	kv := map[string]uint64{}
	for _, line := range strings.Split(data, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		f := strings.Fields(v)
		if len(f) == 0 {
			continue
		}
		n, err := strconv.ParseUint(f[0], 10, 64)
		if err != nil {
			continue
		}
		if len(f) > 1 && strings.EqualFold(f[1], "kB") {
			n *= 1024
		}
		kv[strings.TrimSpace(k)] = n
	}
	m := memInfo{Total: kv["MemTotal"]}
	if a, ok := kv["MemAvailable"]; ok {
		m.Available = a
	} else {
		m.Available = kv["MemFree"] + kv["Buffers"] + kv["Cached"]
	}
	return m, m.Total > 0
}

type cpuInfo struct {
	Name           string
	Cores, Threads int
	Flags          []string
}

// x86 /proc/cpuinfo flags of interest, in output order.
var x86Flags = []struct{ flag, name string }{
	{"avx", "avx"}, {"avx2", "avx2"}, {"fma", "fma"}, {"f16c", "f16c"},
	{"avx512f", "avx512f"}, {"avx512_vnni", "avx512_vnni"}, {"avx512_bf16", "avx512_bf16"},
	{"avx_vnni", "avx_vnni"}, {"amx_tile", "amx"},
}

// ARM /proc/cpuinfo "Features" of interest.
var armFeatures = []struct{ flag, name string }{
	{"asimd", "neon"}, {"asimddp", "dotprod"}, {"i8mm", "i8mm"}, {"bf16", "bf16"},
	{"sve", "sve"}, {"sve2", "sve2"}, {"sme", "sme"},
}

// parseCPUInfo parses /proc/cpuinfo. Physical cores are unique
// (physical id, core id) pairs; ARM, which has no SMT and no such fields,
// counts one core per processor. Name falls back to ARM part names.
func parseCPUInfo(data string) cpuInfo {
	var (
		ci                    cpuInfo
		coreSet               = map[[2]string]bool{}
		physIDs               = map[string]bool{}
		physID, coreID        string
		haveCoreID            bool
		cpuCores              int
		flags, features       string
		modelName, board, hwn string
		implementer           string
		parts                 []string
		isARM                 bool
	)
	flush := func() {
		if haveCoreID {
			coreSet[[2]string{physID, coreID}] = true
		}
		physID, coreID, haveCoreID = "", "", false
	}
	for _, line := range strings.Split(data, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			if strings.TrimSpace(line) == "" {
				flush()
			}
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "processor":
			if _, err := strconv.Atoi(v); err == nil {
				ci.Threads++
			}
		case "model name":
			if modelName == "" {
				modelName = v
			}
		case "physical id":
			physID = v
			physIDs[v] = true
		case "core id":
			coreID, haveCoreID = v, true
		case "cpu cores":
			if n, err := strconv.Atoi(v); err == nil && cpuCores == 0 {
				cpuCores = n
			}
		case "flags":
			if flags == "" {
				flags = v
			}
		case "Features":
			isARM = true
			if features == "" {
				features = v
			}
		case "CPU implementer":
			isARM = true
			implementer = v
		case "CPU part":
			if p := armPartName(implementer, v); p != "" && !contains(parts, p) {
				parts = append(parts, p)
			}
		case "Model":
			board = v
		case "Hardware":
			hwn = v
		}
	}
	flush()

	switch {
	case len(coreSet) > 0:
		ci.Cores = len(coreSet)
	case cpuCores > 0 && len(physIDs) > 0:
		ci.Cores = cpuCores * len(physIDs)
	case isARM:
		ci.Cores = ci.Threads
	}

	ci.Name = modelName
	if (ci.Name == "" || strings.HasPrefix(ci.Name, "ARMv")) && len(parts) > 0 {
		ci.Name = strings.Join(parts, " + ")
	}
	for _, alt := range []string{board, hwn} {
		if ci.Name == "" {
			ci.Name = alt
		}
	}

	set := map[string]bool{}
	for _, f := range strings.Fields(flags + " " + features) {
		set[f] = true
	}
	table := x86Flags
	if isARM {
		table = armFeatures
	}
	for _, e := range table {
		if set[e.flag] {
			ci.Flags = append(ci.Flags, e.name)
		}
	}
	return ci
}

var armParts = map[string]string{
	"0x41:0xd03": "Cortex-A53", "0x41:0xd04": "Cortex-A35", "0x41:0xd05": "Cortex-A55",
	"0x41:0xd07": "Cortex-A57", "0x41:0xd08": "Cortex-A72", "0x41:0xd09": "Cortex-A73",
	"0x41:0xd0a": "Cortex-A75", "0x41:0xd0b": "Cortex-A76", "0x41:0xd0c": "Neoverse-N1",
	"0x41:0xd0d": "Cortex-A77", "0x41:0xd40": "Neoverse-V1", "0x41:0xd41": "Cortex-A78",
	"0x41:0xd44": "Cortex-X1", "0x41:0xd46": "Cortex-A510", "0x41:0xd47": "Cortex-A710",
	"0x41:0xd48": "Cortex-X2", "0x41:0xd49": "Neoverse-N2", "0x41:0xd4b": "Cortex-A78C",
	"0x41:0xd4d": "Cortex-A715", "0x41:0xd4e": "Cortex-X3", "0x41:0xd4f": "Neoverse-V2",
	"0x41:0xd80": "Cortex-A520", "0x41:0xd81": "Cortex-A720", "0x41:0xd82": "Cortex-X4",
	"0x41:0xd84": "Neoverse-V3", "0x41:0xd85": "Cortex-X925", "0x41:0xd87": "Cortex-A725",
	"0x41:0xd8e": "Neoverse-N3",
	"0x51:0x001": "Qualcomm Oryon", "0xc0:0xac3": "Ampere-1", "0xc0:0xac4": "Ampere-1a",
	"0x48:0xd01": "HiSilicon Kunpeng 920", "0x46:0x001": "Fujitsu A64FX", "0x4e:0x004": "NVIDIA Carmel",
}

func armPartName(implementer, part string) string {
	impl, p := strings.ToLower(implementer), strings.ToLower(part)
	if n, ok := armParts[impl+":"+p]; ok {
		return n
	}
	if impl == "0x61" {
		return "Apple"
	}
	return ""
}

// parseOSRelease returns PRETTY_NAME (or NAME VERSION) from /etc/os-release.
func parseOSRelease(data string) string {
	kv := map[string]string{}
	for _, line := range strings.Split(data, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || strings.HasPrefix(k, "#") {
			continue
		}
		if uq, err := strconv.Unquote(v); err == nil {
			v = uq
		} else {
			v = strings.Trim(v, `"'`)
		}
		kv[k] = v
	}
	if p := kv["PRETTY_NAME"]; p != "" {
		return p
	}
	return strings.TrimSpace(kv["NAME"] + " " + kv["VERSION"])
}

// ---- sysfs DRM, lspci, amdgpu.ids ------------------------------------------

type drmCard struct {
	Card                          string // "card1"
	VendorID, DeviceID            uint16
	Revision                      uint8
	Slot                          string // PCI slot with domain, e.g. "0000:03:00.0"
	Driver                        string // amdgpu, i915, xe, nvidia, ...
	ProductName                   string // amdgpu product_name when the board exposes one
	VRAMTotal, VRAMUsed, GTTTotal uint64
}

var drmCardRe = regexp.MustCompile(`^card(\d+)$`)

// scanDRM reads root/card*/device/ attributes (root is /sys/class/drm in
// production, a fixture tree in tests). Cards without a PCI vendor ID
// (simpledrm, connectors) are skipped; duplicate PCI slots are collapsed.
func scanDRM(r runner, root string) []drmCard {
	matches, _ := r.Glob(root + "/card*")
	type numbered struct {
		n    int
		path string
	}
	var list []numbered
	for _, m := range matches {
		sub := drmCardRe.FindStringSubmatch(path.Base(strings.ReplaceAll(m, `\`, "/")))
		if sub == nil {
			continue
		}
		n, _ := strconv.Atoi(sub[1])
		list = append(list, numbered{n, m})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].n < list[j].n })

	read := func(p string) string {
		b, err := r.ReadFile(p)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(b))
	}
	seen := map[string]bool{}
	var cards []drmCard
	for _, e := range list {
		dev := e.path + "/device/"
		vendor, err := strconv.ParseUint(read(dev+"vendor"), 0, 16)
		if err != nil {
			continue
		}
		c := drmCard{Card: path.Base(strings.ReplaceAll(e.path, `\`, "/")), VendorID: uint16(vendor)}
		if v, err := strconv.ParseUint(read(dev+"device"), 0, 16); err == nil {
			c.DeviceID = uint16(v)
		}
		if v, err := strconv.ParseUint(read(dev+"revision"), 0, 8); err == nil {
			c.Revision = uint8(v)
		}
		for _, line := range strings.Split(read(dev+"uevent"), "\n") {
			if k, v, ok := strings.Cut(line, "="); ok {
				switch k {
				case "PCI_SLOT_NAME":
					c.Slot = normalizePCISlot(v)
				case "DRIVER":
					c.Driver = v
				}
			}
		}
		c.ProductName = read(dev + "product_name")
		c.VRAMTotal, _ = strconv.ParseUint(read(dev+"mem_info_vram_total"), 10, 64)
		c.VRAMUsed, _ = strconv.ParseUint(read(dev+"mem_info_vram_used"), 10, 64)
		c.GTTTotal, _ = strconv.ParseUint(read(dev+"mem_info_gtt_total"), 10, 64)
		if c.Slot != "" {
			if seen[c.Slot] {
				continue
			}
			seen[c.Slot] = true
		}
		cards = append(cards, c)
	}
	return cards
}

func normalizePCISlot(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if strings.Count(s, ":") == 1 {
		s = "0000:" + s
	}
	return s
}

// pciName holds raw lspci -mm vendor/device fields.
type pciName struct{ Vendor, Device string }

// parseLspciMM parses `lspci -D -mm -nn` (or without -D) into a map keyed
// by normalized slot ("0000:03:00.0").
func parseLspciMM(out string) map[string]pciName {
	m := map[string]pciName{}
	for _, line := range strings.Split(out, "\n") {
		toks := lspciTokens(line)
		if len(toks) < 4 || toks[0].quoted {
			continue
		}
		var q []string
		for _, t := range toks[1:] {
			if t.quoted {
				q = append(q, t.s)
			}
		}
		if len(q) < 3 {
			continue
		}
		m[normalizePCISlot(toks[0].s)] = pciName{Vendor: q[1], Device: q[2]}
	}
	return m
}

type lspciTok struct {
	s      string
	quoted bool
}

func lspciTokens(line string) []lspciTok {
	var toks []lspciTok
	i := 0
	for i < len(line) {
		switch {
		case line[i] == ' ' || line[i] == '\t':
			i++
		case line[i] == '"':
			j := strings.IndexByte(line[i+1:], '"')
			if j < 0 {
				toks = append(toks, lspciTok{line[i+1:], true})
				return toks
			}
			toks = append(toks, lspciTok{line[i+1 : i+1+j], true})
			i += j + 2
		default:
			j := strings.IndexAny(line[i:], " \t")
			if j < 0 {
				j = len(line) - i
			}
			toks = append(toks, lspciTok{line[i : i+j], false})
			i += j
		}
	}
	return toks
}

var (
	pciTrailingIDRe = regexp.MustCompile(`\s*\[[0-9a-fA-F]{4}\]\s*$`)
	pciBracketRe    = regexp.MustCompile(`\[([^\[\]]+)\]`)
)

// prettyPCIName turns an lspci device field such as
// "Navi 31 [Radeon RX 7900 XT/7900 XTX/7900 GRE/7900M] [744c]" into a
// marketing name ("AMD Radeon RX 7900 XT/7900 XTX/7900 GRE/7900M").
// It returns "" for unnamed devices ("Device [7550]").
func prettyPCIName(vendorName, raw string) string {
	s := strings.TrimSpace(pciTrailingIDRe.ReplaceAllString(raw, ""))
	if m := pciBracketRe.FindAllStringSubmatch(s, -1); len(m) > 0 {
		s = strings.TrimSpace(m[len(m)-1][1])
	}
	if s == "" || strings.EqualFold(s, "Device") {
		return ""
	}
	if vendorName != "" && !strings.HasPrefix(strings.ToLower(s), strings.ToLower(vendorName)) {
		s = vendorName + " " + s
	}
	return s
}

// parseAMDGPUIDs parses libdrm's amdgpu.ids ("744C,\tC8,\tAMD Radeon RX
// 7900 XTX") into a map keyed by device<<8 | revision.
func parseAMDGPUIDs(data string) map[uint32]string {
	m := map[uint32]string{}
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		f := strings.SplitN(line, ",", 3)
		if len(f) != 3 {
			continue
		}
		dev, err1 := strconv.ParseUint(strings.TrimSpace(f[0]), 16, 16)
		rev, err2 := strconv.ParseUint(strings.TrimSpace(f[1]), 16, 8)
		name := strings.TrimSpace(f[2])
		if err1 != nil || err2 != nil || name == "" {
			continue
		}
		m[uint32(dev)<<8|uint32(rev)] = name
	}
	return m
}

// linuxGPUsFromDRM turns scanned DRM cards into GPUs. NVIDIA cards are
// skipped when nvidia-smi already reported them.
func linuxGPUsFromDRM(cards []drmCard, pci map[string]pciName, amdIDs map[uint32]string, ramBytes uint64, haveNVSMI bool) ([]GPU, []string) {
	var gpus []GPU
	var notes []string
	for _, c := range cards {
		vendor := vendorFromPCIID(c.VendorID)
		if vendor == VendorOther || vendor == VendorApple { // BMC (ASPEED), virtio, VMware, ...
			continue
		}
		if vendor == VendorNVIDIA && haveNVSMI {
			continue
		}
		vname := vendorDisplayName(vendor)
		name := c.ProductName
		if name == "" && vendor == VendorAMD {
			if n := amdIDs[uint32(c.DeviceID)<<8|uint32(c.Revision)]; n != "" {
				name = n
				if !strings.HasPrefix(strings.ToUpper(name), "AMD") {
					name = "AMD " + name
				}
			}
		}
		raw := name
		if p, ok := pci[c.Slot]; ok {
			raw += " " + p.Device
			if name == "" {
				name = prettyPCIName(vname, p.Device)
			}
		}
		if name == "" {
			name = fmt.Sprintf("%s GPU [%04x:%04x]", vname, c.VendorID, c.DeviceID)
		}
		g := GPU{Vendor: vendor, Name: name, Driver: c.Driver, Source: "sysfs"}

		switch vendor {
		case VendorAMD:
			if isAMDAPU(raw, c.DeviceID, c.VRAMTotal, c.GTTTotal) {
				g.Integrated, g.Unified = true, true
				g.VRAMBytes = max(c.VRAMTotal, c.GTTTotal)
				if g.VRAMBytes == 0 {
					g.VRAMBytes = ramBytes / 2
				}
				notes = append(notes, fmt.Sprintf("%s is an integrated APU sharing system RAM (%s dedicated carve-out, %s GTT); reporting %s as GPU-addressable memory (raise with the BIOS UMA setting or amdgpu.gttsize / ttm.pages_limit)",
					name, gib(c.VRAMTotal), gib(c.GTTTotal), gib(g.VRAMBytes)))
			} else {
				g.VRAMBytes = c.VRAMTotal
				if c.VRAMUsed > 0 && c.VRAMUsed < c.VRAMTotal {
					g.FreeVRAMBytes = c.VRAMTotal - c.VRAMUsed
				}
				if c.VRAMTotal == 0 {
					notes = append(notes, fmt.Sprintf("%s: VRAM size not exposed (driver %q)", name, c.Driver))
				}
			}
		case VendorIntel:
			igpuSlot := strings.HasSuffix(c.Slot, ":00:02.0")
			if isIntelDiscrete(name) || (c.Slot != "" && !igpuSlot) {
				g.VRAMBytes = intelArcVRAMByName(name)
				if g.VRAMBytes > 0 {
					notes = append(notes, fmt.Sprintf("%s: VRAM (%s) inferred from the model name; the Linux driver does not report it", name, gib(g.VRAMBytes)))
				} else {
					notes = append(notes, fmt.Sprintf("%s: discrete Intel GPU with unknown VRAM", name))
				}
			} else {
				g.Integrated, g.Unified = true, true
				g.VRAMBytes = ramBytes / 2
				notes = append(notes, fmt.Sprintf("%s is an integrated GPU sharing system RAM; reporting half of RAM (%s) as GPU-addressable memory", name, gib(g.VRAMBytes)))
			}
		case VendorNVIDIA:
			notes = append(notes, fmt.Sprintf("%s found in sysfs but nvidia-smi is unavailable or failed; VRAM unknown (is the NVIDIA driver installed?)", name))
		}
		gpus = append(gpus, g)
	}
	return gpus, notes
}
