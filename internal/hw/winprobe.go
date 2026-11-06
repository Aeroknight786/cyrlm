package hw

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// PowerShell scripts. Each emits JSON; ConvertTo-Json emits a bare object
// instead of an array when there is a single item.
const (
	psVideoScript = `Get-CimInstance Win32_VideoController | Select-Object Name,AdapterRAM,DriverVersion,PNPDeviceID | ConvertTo-Json -Compress`
	// AdapterRAM is a UInt32 and saturates at 4 GiB; the display class keys
	// hold the real 64-bit size.
	psRegistryScript = `Get-ItemProperty 'HKLM:\SYSTEM\CurrentControlSet\Control\Class\{4d36e968-e325-11ce-bfc1-08002be10318}\0*' -ErrorAction SilentlyContinue | Select-Object DriverDesc,MatchingDeviceId,'HardwareInformation.qwMemorySize','HardwareInformation.MemorySize' | ConvertTo-Json -Compress`
	psCPUScript      = `Get-CimInstance Win32_Processor | Select-Object Name,NumberOfCores,NumberOfLogicalProcessors | ConvertTo-Json -Compress`
	psSystemScript   = `$cs = Get-CimInstance Win32_ComputerSystem; $os = Get-CimInstance Win32_OperatingSystem; $b = @(Get-CimInstance Win32_Battery -ErrorAction SilentlyContinue).Count; [pscustomobject]@{TotalPhysicalMemory=$cs.TotalPhysicalMemory; Caption=$os.Caption; Version=$os.Version; FreePhysicalMemory=$os.FreePhysicalMemory; BatteryCount=$b} | ConvertTo-Json -Compress`
)

func (d detector) detectWindows(ctx context.Context, s *System) {
	ps := d.findTool("powershell", winPath(d.r.Getenv("SystemRoot"), `System32\WindowsPowerShell\v1.0\powershell.exe`))
	runPS := func(script string) ([]byte, error) {
		if ps == "" {
			return nil, errors.New("powershell not found")
		}
		return d.r.Run(ctx, ps, "-NoProfile", "-NonInteractive", "-Command", script)
	}
	var (
		video, reg, cpu, sys, smi []byte
		videoErr, cpuErr, sysErr  error
		smiPath                   string
	)
	parallel(
		func() { video, videoErr = runPS(psVideoScript) },
		func() { reg, _ = runPS(psRegistryScript) },
		func() { cpu, cpuErr = runPS(psCPUScript) },
		func() { sys, sysErr = runPS(psSystemScript) },
		func() {
			smiPath = d.findTool("nvidia-smi", `C:\Program Files\NVIDIA Corporation\NVSMI\nvidia-smi.exe`)
			if smiPath != "" {
				smi, _ = d.r.Run(ctx, smiPath, nvidiaSMIArgs...)
			}
		},
	)

	battery := false
	if ws, err := parseWindowsSystem(sys); err == nil && ws.TotalPhysicalMemory > 0 {
		s.RAMBytes = uint64(ws.TotalPhysicalMemory)
		s.FreeRAMBytes = uint64(ws.FreePhysicalMemory) * 1024
		s.OSVersion = strings.TrimSpace(ws.Caption + " " + ws.Version)
		battery = ws.BatteryCount > 0
	} else {
		s.note("could not query Win32_ComputerSystem/Win32_OperatingSystem: %s", errDetail(sys, firstErr(sysErr, err)))
	}

	if name, cores, threads, err := parseWindowsProcessors(cpu); err == nil && threads > 0 {
		s.CPUName, s.CPUCores, s.CPUThreads = name, cores, threads
	} else {
		s.note("could not query Win32_Processor: %s", errDetail(cpu, firstErr(cpuErr, err)))
	}
	if d.goarch == "arm64" {
		s.CPUFlags = []string{"neon"}
	}

	cim, err := parseWindowsVideoControllers(video)
	if err != nil {
		s.note("could not query Win32_VideoController: %s", errDetail(video, firstErr(videoErr, err)))
	}
	regVRAM, _ := parseWindowsRegistryVRAM(reg)
	smiGPUs := parseNvidiaSMI(string(smi))
	gpus, notes := mergeWindowsGPUs(cim, regVRAM, smiGPUs, s.RAMBytes)
	s.Notes = append(s.Notes, notes...)
	markLaptops(gpus, battery)
	s.GPUs = gpus
}

func winPath(root, rel string) string {
	if root == "" {
		return ""
	}
	return strings.TrimRight(root, `\`) + `\` + rel
}

// mergeWindowsGPUs combines Win32_VideoController entries with the
// registry's 64-bit VRAM sizes and nvidia-smi (preferred for NVIDIA).
func mergeWindowsGPUs(cim []GPU, regVRAM map[string]uint64, smi []GPU, ramBytes uint64) ([]GPU, []string) {
	var out []GPU
	var notes []string
	out = append(out, smi...)
	for _, g := range cim {
		if g.Vendor == VendorNVIDIA && len(smi) > 0 {
			continue
		}
		if v := regVRAM[regKey(g.Name)]; v > 0 {
			g.VRAMBytes = v
			g.Source += "+registry"
		} else if g.VRAMBytes >= 0xFFF00000 {
			notes = append(notes, fmt.Sprintf("%s: VRAM reported by WMI is capped at 4 GiB; the real size may be larger", g.Name))
		}
		integrated := false
		switch g.Vendor {
		case VendorAMD:
			integrated = isAMDAPU(g.Name, 0, 0, 0)
		case VendorIntel:
			integrated = !isIntelDiscrete(g.Name)
			if !integrated && g.VRAMBytes == 0 {
				g.VRAMBytes = intelArcVRAMByName(g.Name)
			}
		case VendorOther:
			integrated = strings.Contains(strings.ToLower(g.Name), "adreno")
		}
		if integrated {
			dedicated := g.VRAMBytes
			g.Integrated, g.Unified = true, true
			g.VRAMBytes = max(dedicated, ramBytes/2)
			notes = append(notes, fmt.Sprintf("%s is an integrated GPU sharing system RAM (%s dedicated); reporting %s (the larger of dedicated memory and Windows' default half-of-RAM shared limit)",
				g.Name, gib(dedicated), gib(g.VRAMBytes)))
		}
		out = append(out, g)
	}
	return out, notes
}

func regKey(name string) string { return strings.ToLower(cleanTM(name)) }

type winVideoController struct {
	Name          string
	AdapterRAM    flexUint
	DriverVersion string
	PNPDeviceID   string
}

var pnpVendorRe = regexp.MustCompile(`(?i)VEN_([0-9A-F]{4})`)

// parseWindowsVideoControllers parses Win32_VideoController JSON (array or
// single object). Virtual/remote adapters are dropped. VRAMBytes is the
// (4 GiB-capped) AdapterRAM; merge with the registry for the real size.
func parseWindowsVideoControllers(data []byte) ([]GPU, error) {
	items, err := decodeOneOrMany[winVideoController](data)
	if err != nil {
		return nil, err
	}
	var gpus []GPU
	for _, v := range items {
		name := strings.TrimSpace(v.Name)
		if name == "" || isVirtualAdapter(name, v.PNPDeviceID) {
			continue
		}
		vendor := VendorOther
		if m := pnpVendorRe.FindStringSubmatch(v.PNPDeviceID); m != nil {
			id, _ := strconv.ParseUint(m[1], 16, 16)
			vendor = vendorFromPCIID(uint16(id))
		}
		if vendor == VendorOther {
			vendor = vendorFromName(name)
		}
		gpus = append(gpus, GPU{
			Vendor:    vendor,
			Name:      name,
			VRAMBytes: uint64(v.AdapterRAM),
			Driver:    strings.TrimSpace(v.DriverVersion),
			Source:    "Win32_VideoController",
		})
	}
	return gpus, nil
}

func isVirtualAdapter(name, pnp string) bool {
	p := strings.ToUpper(pnp)
	for _, pre := range []string{`ROOT\`, `SWD\`, `USB\`} {
		if strings.HasPrefix(p, pre) {
			return true
		}
	}
	n := strings.ToLower(name)
	for _, s := range []string{"microsoft basic", "remote display", "virtual display", "parsec", "displaylink", "citrix", "hyper-v video", "meta virtual"} {
		if strings.Contains(n, s) {
			return true
		}
	}
	return false
}

type winRegAdapter struct {
	DriverDesc       string
	MatchingDeviceID string   `json:"MatchingDeviceId"`
	QwMemorySize     flexUint `json:"HardwareInformation.qwMemorySize"`
	MemorySize       flexUint `json:"HardwareInformation.MemorySize"`
}

// parseWindowsRegistryVRAM parses the display-class registry dump into a map
// from normalized DriverDesc to dedicated memory in bytes.
func parseWindowsRegistryVRAM(data []byte) (map[string]uint64, error) {
	items, err := decodeOneOrMany[winRegAdapter](data)
	if err != nil {
		return nil, err
	}
	m := map[string]uint64{}
	for _, it := range items {
		if strings.TrimSpace(it.DriverDesc) == "" {
			continue
		}
		v := uint64(it.QwMemorySize)
		if v == 0 {
			v = uint64(it.MemorySize)
		}
		if k := regKey(it.DriverDesc); v > m[k] {
			m[k] = v
		}
	}
	return m, nil
}

type winProcessor struct {
	Name                      string
	NumberOfCores             flexUint
	NumberOfLogicalProcessors flexUint
}

// parseWindowsProcessors parses Win32_Processor JSON, summing sockets.
func parseWindowsProcessors(data []byte) (name string, cores, threads int, err error) {
	items, err := decodeOneOrMany[winProcessor](data)
	if err != nil {
		return "", 0, 0, err
	}
	for _, p := range items {
		if name == "" {
			name = strings.Join(strings.Fields(p.Name), " ")
		}
		cores += int(p.NumberOfCores)
		threads += int(p.NumberOfLogicalProcessors)
	}
	return name, cores, threads, nil
}

type winSystem struct {
	TotalPhysicalMemory flexUint // bytes
	Caption, Version    string
	FreePhysicalMemory  flexUint // KiB
	BatteryCount        flexUint
}

// parseWindowsSystem parses the combined ComputerSystem/OperatingSystem/
// Battery object emitted by psSystemScript.
func parseWindowsSystem(data []byte) (winSystem, error) {
	items, err := decodeOneOrMany[winSystem](data)
	if err != nil {
		return winSystem{}, err
	}
	if len(items) == 0 {
		return winSystem{}, errors.New("no output")
	}
	return items[0], nil
}

// decodeOneOrMany decodes a JSON array of T or a single T (ConvertTo-Json's
// single-item form). Empty input yields no items.
func decodeOneOrMany[T any](data []byte) ([]T, error) {
	data = bytes.TrimSpace(bytes.TrimPrefix(bytes.TrimSpace(data), []byte("\xef\xbb\xbf")))
	if len(data) == 0 {
		return nil, nil
	}
	if data[0] == '[' {
		var out []T
		err := json.Unmarshal(data, &out)
		return out, err
	}
	var one T
	if err := json.Unmarshal(data, &one); err != nil {
		return nil, err
	}
	return []T{one}, nil
}

// flexUint leniently decodes an unsigned integer from a JSON number, numeric
// string ("0x..." allowed), little-endian byte array (REG_BINARY), or
// PowerShell's {"value": ...} array wrapper. Anything else decodes as 0.
type flexUint uint64

func (f *flexUint) UnmarshalJSON(b []byte) error {
	*f = 0
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return nil
	}
	switch b[0] {
	case '"':
		var s string
		if json.Unmarshal(b, &s) == nil {
			*f = flexUint(parseUintLoose(strings.TrimSpace(s)))
		}
	case '[':
		var arr []float64
		if json.Unmarshal(b, &arr) == nil && len(arr) <= 8 {
			var v uint64
			for i, x := range arr {
				v |= uint64(byte(x)) << (8 * i)
			}
			*f = flexUint(v)
		}
	case '{':
		var o struct {
			Value json.RawMessage `json:"value"`
		}
		if json.Unmarshal(b, &o) == nil && len(o.Value) > 0 {
			return f.UnmarshalJSON(o.Value)
		}
	default:
		*f = flexUint(parseUintLoose(string(b)))
	}
	return nil
}

func parseUintLoose(s string) uint64 {
	if h, ok := strings.CutPrefix(strings.ToLower(s), "0x"); ok {
		v, _ := strconv.ParseUint(h, 16, 64)
		return v
	}
	if v, err := strconv.ParseUint(s, 10, 64); err == nil {
		return v
	}
	if v, err := strconv.ParseFloat(s, 64); err == nil && v > 0 {
		return uint64(v)
	}
	return 0
}
