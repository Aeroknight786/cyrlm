package hw

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseNvidiaSMI(t *testing.T) {
	g := parseNvidiaSMI(fixture(t, "nvidia-smi-1gpu.txt"))
	want := []GPU{{Vendor: VendorNVIDIA, Name: "NVIDIA GeForce RTX 4090", VRAMBytes: 24564 * miB,
		FreeVRAMBytes: 23871 * miB, Driver: "580.95.05", Source: "nvidia-smi"}}
	if !reflect.DeepEqual(g, want) {
		t.Fatalf("1 GPU:\n got %+v\nwant %+v", g, want)
	}

	g = parseNvidiaSMI(fixture(t, "nvidia-smi-2gpu.txt"))
	if len(g) != 2 || g[0].Name != "NVIDIA GeForce RTX 3090" || g[1].FreeVRAMBytes != 1310*miB || g[1].VRAMBytes != 24*giB {
		t.Fatalf("2 GPUs: %+v", g)
	}

	g = parseNvidiaSMI(fixture(t, "nvidia-smi-laptop.txt"))
	if len(g) != 1 || g[0].Name != "NVIDIA GeForce RTX 4070 Laptop GPU" || !isLaptopName(g[0].Name) || g[0].Driver != "581.57" {
		t.Fatalf("laptop: %+v", g)
	}

	g = parseNvidiaSMI(fixture(t, "nvidia-smi-gb10.txt"))
	if len(g) != 1 || !g[0].Unified || g[0].VRAMBytes != 0 || g[0].FreeVRAMBytes != 0 {
		t.Fatalf("GB10: %+v", g)
	}

	if g := parseNvidiaSMI(fixture(t, "nvidia-smi-error.txt")); len(g) != 0 {
		t.Fatalf("error output parsed as GPUs: %+v", g)
	}
	if g := parseNvidiaSMI("No devices were found\n"); len(g) != 0 {
		t.Fatalf("no devices: %+v", g)
	}
}

func TestParseMeminfo(t *testing.T) {
	m, ok := parseMeminfo(fixture(t, "meminfo.txt"))
	if !ok || m.Total != 65536000*1024 || m.Available != 52428800*1024 {
		t.Fatalf("got %+v ok=%v", m, ok)
	}
	// Kernels before 3.14 have no MemAvailable.
	m, ok = parseMeminfo("MemTotal: 1000 kB\nMemFree: 100 kB\nBuffers: 10 kB\nCached: 50 kB\n")
	if !ok || m.Available != 160*1024 {
		t.Fatalf("fallback: %+v", m)
	}
	if _, ok := parseMeminfo("garbage"); ok {
		t.Fatal("garbage parsed")
	}
}

func TestParseCPUInfoX86(t *testing.T) {
	ci := parseCPUInfo(fixture(t, "cpuinfo-x86-avx512.txt"))
	if ci.Name != "11th Gen Intel(R) Core(TM) i3-1115G4 @ 3.00GHz" || ci.Cores != 2 || ci.Threads != 4 {
		t.Fatalf("got %+v", ci)
	}
	if want := []string{"avx", "avx2", "fma", "f16c", "avx512f", "avx512_vnni"}; !reflect.DeepEqual(ci.Flags, want) {
		t.Fatalf("flags %v, want %v", ci.Flags, want)
	}
}

func TestParseCPUInfoAMX(t *testing.T) {
	ci := parseCPUInfo("processor\t: 0\nmodel name\t: Intel(R) Xeon(R) Platinum 8480+\nphysical id\t: 0\ncore id\t: 0\nflags\t\t: avx avx2 avx512f avx512_bf16 amx_bf16 amx_tile amx_int8\n\n" +
		"processor\t: 1\nmodel name\t: Intel(R) Xeon(R) Platinum 8480+\nphysical id\t: 1\ncore id\t: 0\nflags\t\t: avx avx2 avx512f avx512_bf16 amx_bf16 amx_tile amx_int8\n")
	if ci.Cores != 2 || !contains(ci.Flags, "amx") || !contains(ci.Flags, "avx512_bf16") {
		t.Fatalf("got %+v", ci)
	}
}

func TestParseCPUInfoARM(t *testing.T) {
	ci := parseCPUInfo(fixture(t, "cpuinfo-arm64.txt"))
	if ci.Name != "Cortex-A76" || ci.Cores != 4 || ci.Threads != 4 {
		t.Fatalf("got %+v", ci)
	}
	if want := []string{"neon", "dotprod"}; !reflect.DeepEqual(ci.Flags, want) {
		t.Fatalf("flags %v, want %v", ci.Flags, want)
	}
	// big.LITTLE SoC (DGX Spark-like): both part names.
	ci = parseCPUInfo("processor\t: 0\nFeatures\t: fp asimd asimddp i8mm bf16 sve sve2\nCPU implementer\t: 0x41\nCPU part\t: 0xd87\n\n" +
		"processor\t: 1\nFeatures\t: fp asimd asimddp i8mm bf16 sve sve2\nCPU implementer\t: 0x41\nCPU part\t: 0xd85\n")
	if ci.Name != "Cortex-A725 + Cortex-X925" || ci.Cores != 2 || !reflect.DeepEqual(ci.Flags, []string{"neon", "dotprod", "i8mm", "bf16", "sve", "sve2"}) {
		t.Fatalf("big.LITTLE: %+v", ci)
	}
}

func TestParseOSRelease(t *testing.T) {
	if got := parseOSRelease(fixture(t, "os-release.txt")); got != "Ubuntu 24.04.3 LTS" {
		t.Fatalf("got %q", got)
	}
	if got := parseOSRelease("NAME=Arch\nVERSION='rolling'\n"); got != "Arch rolling" {
		t.Fatalf("fallback got %q", got)
	}
}

func TestParseLspciAndPrettyNames(t *testing.T) {
	m := parseLspciMM(fixture(t, "lspci-mm-nn.txt"))
	if len(m) != 9 {
		t.Fatalf("got %d entries: %v", len(m), m)
	}
	if d := m["0000:03:00.0"].Device; d != "Navi 31 [Radeon RX 7900 XT/7900 XTX/7900 GRE/7900M] [744c]" {
		t.Fatalf("device field %q", d)
	}
	// Without -D the domain is implied.
	if m2 := parseLspciMM(`01:00.0 "VGA compatible controller [0300]" "NVIDIA Corporation [10de]" "AD102 [GeForce RTX 4090] [2684]" -ra1 "ASUSTeK [1043]" "Device [889d]"`); m2["0000:01:00.0"].Device == "" {
		t.Fatalf("no-domain slot not normalized: %v", m2)
	}
	cases := []struct{ vendor, raw, want string }{
		{"AMD", m["0000:03:00.0"].Device, "AMD Radeon RX 7900 XT/7900 XTX/7900 GRE/7900M"},
		{"AMD", m["0000:c5:00.0"].Device, "AMD Radeon Graphics / Radeon 8050S / 8060S Graphics"},
		{"AMD", m["0000:13:00.0"].Device, "AMD Raphael"},
		{"Intel", m["0000:00:02.0"].Device, "Intel Iris Xe Graphics"},
		{"Intel", "Meteor Lake-P [Intel Arc Graphics] [7d55]", "Intel Arc Graphics"},
		{"Intel", m["0000:04:00.0"].Device, "Intel Arc A770"},
		{"NVIDIA", "AD102 [GeForce RTX 4090] [2684]", "NVIDIA GeForce RTX 4090"},
		{"AMD", "Device [7550]", ""},
	}
	for _, c := range cases {
		if got := prettyPCIName(c.vendor, c.raw); got != c.want {
			t.Errorf("prettyPCIName(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
}

func TestParseAMDGPUIDs(t *testing.T) {
	m := parseAMDGPUIDs(fixture(t, "amdgpu.ids"))
	if len(m) != 6 || m[0x744c<<8|0xc8] != "AMD Radeon RX 7900 XTX" || m[0x1586<<8|0xc1] != "AMD Radeon 8060S Graphics" {
		t.Fatalf("got %v", m)
	}
}

// amdDesktopTree is a desktop with an RX 7900 XTX, the Ryzen 7000 iGPU
// (Raphael), an ASPEED BMC, a simpledrm card and a connector directory.
func amdDesktopTree(t *testing.T) string {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"card0/device/uevent":              "DRIVER=simple-framebuffer\nOF_NAME=framebuffer\n",
		"card1/device/vendor":              "0x1002\n",
		"card1/device/device":              "0x744c\n",
		"card1/device/revision":            "0xc8\n",
		"card1/device/uevent":              "DRIVER=amdgpu\nPCI_CLASS=30000\nPCI_ID=1002:744C\nPCI_SUBSYS_ID=1DA2:471E\nPCI_SLOT_NAME=0000:03:00.0\nMODALIAS=pci:v00001002d0000744Csv00001DA2sd0000471Ebc03sc00i00\n",
		"card1/device/mem_info_vram_total": "25753026560\n",
		"card1/device/mem_info_vram_used":  "1073741824\n",
		"card1/device/mem_info_gtt_total":  "33372110848\n",
		"card1-DP-1/status":                "connected\n",
		"card2/device/vendor":              "0x1002\n",
		"card2/device/device":              "0x164e\n",
		"card2/device/revision":            "0xc5\n",
		"card2/device/uevent":              "DRIVER=amdgpu\nPCI_SLOT_NAME=0000:13:00.0\n",
		"card2/device/mem_info_vram_total": "536870912\n",
		"card2/device/mem_info_vram_used":  "41943040\n",
		"card2/device/mem_info_gtt_total":  "33372110848\n",
		"card3/device/vendor":              "0x1a03\n",
		"card3/device/device":              "0x2000\n",
		"card3/device/uevent":              "DRIVER=ast\nPCI_SLOT_NAME=0000:05:00.0\n",
		"renderD128/device/vendor":         "0x1002\n",
	})
	return root
}

func TestDRMAMDDiscreteAndRaphael(t *testing.T) {
	r := &fakeRunner{realFS: true}
	cards := scanDRM(r, amdDesktopTree(t))
	if len(cards) != 3 { // card1, card2, card3 (simpledrm and connector skipped)
		t.Fatalf("cards: %+v", cards)
	}
	if c := cards[0]; c.Card != "card1" || c.DeviceID != 0x744c || c.Revision != 0xc8 || c.Slot != "0000:03:00.0" || c.Driver != "amdgpu" || c.VRAMTotal != 25753026560 {
		t.Fatalf("card1: %+v", c)
	}
	gpus, notes := linuxGPUsFromDRM(cards, parseLspciMM(fixture(t, "lspci-mm-nn.txt")), parseAMDGPUIDs(fixture(t, "amdgpu.ids")), 64*giB, false)
	if len(gpus) != 2 {
		t.Fatalf("gpus: %+v", gpus)
	}
	d := gpus[0]
	if d.Name != "AMD Radeon RX 7900 XTX" || d.Vendor != VendorAMD || d.Integrated || d.Unified ||
		d.VRAMBytes != 25753026560 || d.FreeVRAMBytes != 25753026560-1073741824 || d.Source != "sysfs" {
		t.Fatalf("dGPU: %+v", d)
	}
	a := gpus[1]
	if a.Name != "AMD Raphael" || !a.Integrated || !a.Unified || a.VRAMBytes != 33372110848 {
		t.Fatalf("iGPU: %+v", a)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "integrated APU") {
		t.Fatalf("notes: %v", notes)
	}
}

func TestDRMStrixHaloAPU(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"card0/device/vendor":              "0x1002\n",
		"card0/device/device":              "0x1586\n",
		"card0/device/revision":            "0xc1\n",
		"card0/device/uevent":              "DRIVER=amdgpu\nPCI_SLOT_NAME=0000:c5:00.0\n",
		"card0/device/mem_info_vram_total": "536870912\n",
		"card0/device/mem_info_gtt_total":  "103079215104\n",
	})
	r := &fakeRunner{realFS: true}
	cards := scanDRM(r, root)

	// With amdgpu.ids: exact marketing name.
	gpus, notes := linuxGPUsFromDRM(cards, nil, parseAMDGPUIDs(fixture(t, "amdgpu.ids")), 128*giB, false)
	if len(gpus) != 1 {
		t.Fatalf("gpus: %+v", gpus)
	}
	g := gpus[0]
	if g.Name != "AMD Radeon 8060S Graphics" || !g.Integrated || !g.Unified || g.VRAMBytes != 96*giB {
		t.Fatalf("Strix Halo: %+v", g)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "512 MiB") && !strings.Contains(notes[0], "0.5 GiB") {
		t.Fatalf("notes: %v", notes)
	}

	// Without amdgpu.ids: lspci name, still an APU (by device ID and name).
	gpus, _ = linuxGPUsFromDRM(cards, parseLspciMM(fixture(t, "lspci-mm-nn.txt")), nil, 128*giB, false)
	if gpus[0].Name != "AMD Radeon Graphics / Radeon 8050S / 8060S Graphics" || !gpus[0].Integrated {
		t.Fatalf("lspci fallback: %+v", gpus[0])
	}

	// Unknown device ID, no names at all: carve-out heuristic.
	cards[0].DeviceID = 0x9999
	gpus, _ = linuxGPUsFromDRM(cards, nil, nil, 128*giB, false)
	if gpus[0].Name != "AMD GPU [1002:9999]" || !gpus[0].Integrated {
		t.Fatalf("heuristic: %+v", gpus[0])
	}
}

func TestDRMIntelAndNVIDIA(t *testing.T) {
	cards := []drmCard{
		{Card: "card0", VendorID: 0x8086, DeviceID: 0x46a6, Slot: "0000:00:02.0", Driver: "i915"},
		{Card: "card1", VendorID: 0x10de, DeviceID: 0x2860, Slot: "0000:01:00.0", Driver: "nvidia"},
		{Card: "card2", VendorID: 0x8086, DeviceID: 0x56a0, Slot: "0000:04:00.0", Driver: "xe"},
	}
	pci := parseLspciMM(fixture(t, "lspci-mm-nn.txt"))
	gpus, notes := linuxGPUsFromDRM(cards, pci, nil, 32*giB, false)
	if len(gpus) != 3 || len(notes) != 3 {
		t.Fatalf("gpus %+v notes %v", gpus, notes)
	}
	if g := gpus[0]; g.Name != "Intel Iris Xe Graphics" || !g.Integrated || !g.Unified || g.VRAMBytes != 16*giB {
		t.Fatalf("iGPU: %+v", g)
	}
	if g := gpus[1]; g.Name != "NVIDIA GeForce RTX 4070 Max-Q / Mobile" || g.VRAMBytes != 0 || !strings.Contains(notes[1], "nvidia-smi") {
		t.Fatalf("nvidia: %+v %v", g, notes)
	}
	if g := gpus[2]; g.Name != "Intel Arc A770" || g.Integrated || g.VRAMBytes != 16*giB {
		t.Fatalf("Arc: %+v", g)
	}
	// NVIDIA is skipped when nvidia-smi already reported it.
	gpus, _ = linuxGPUsFromDRM(cards, pci, nil, 32*giB, true)
	if len(gpus) != 2 {
		t.Fatalf("smi dedupe: %+v", gpus)
	}
}

func TestGPUClassification(t *testing.T) {
	apu := []struct {
		name      string
		dev       uint16
		vram, gtt uint64
		want      bool
	}{
		{"AMD Radeon RX 7900 XTX", 0x744c, 24 * giB, 32 * giB, false},
		{"AMD Radeon RX 6500 XT", 0x743f, 4 * giB, 16 * giB, false},
		{"AMD Radeon Pro 5500M", 0, 8 * giB, 0, false},
		{"AMD Radeon 780M Graphics", 0, 0, 0, true},
		{"AMD Radeon(TM) Graphics", 0, 0, 0, true},
		{"AMD Radeon(TM) RX Vega 10 Graphics", 0, 0, 0, true},
		{"AMD Radeon 890M", 0, 0, 0, true},
		{"", 0x1586, 0, 0, true},
		{"AMD GPU", 0x1234, 512 * miB, 16 * giB, true},
	}
	for _, c := range apu {
		if got := isAMDAPU(c.name, c.dev, c.vram, c.gtt); got != c.want {
			t.Errorf("isAMDAPU(%q, %#x) = %v, want %v", c.name, c.dev, got, c.want)
		}
	}
	intel := map[string]bool{
		"Intel(R) Arc(TM) A770 Graphics":   true,
		"Intel Arc B580":                   true,
		"Intel(R) Arc(TM) Pro B60":         true,
		"Intel Arc Graphics":               false,
		"Intel(R) Arc(TM) 140V GPU (16GB)": false,
		"Intel(R) UHD Graphics 770":        false,
		"Intel Iris Xe Graphics":           false,
	}
	for n, want := range intel {
		if got := isIntelDiscrete(n); got != want {
			t.Errorf("isIntelDiscrete(%q) = %v", n, got)
		}
	}
	vram := map[string]uint64{"Intel Arc B580": 12, "Intel(R) Arc(TM) A770 Graphics": 16, "Intel Arc A750": 8, "Intel Arc Pro B60": 24, "Intel Arc A770M": 16, "Intel Arc Graphics": 0}
	for n, want := range vram {
		if got := intelArcVRAMByName(n); got != want*giB {
			t.Errorf("intelArcVRAMByName(%q) = %d", n, got)
		}
	}
	laptop := map[string]bool{
		"NVIDIA GeForce RTX 4090 Laptop GPU":        true,
		"NVIDIA GeForce RTX 2080 with Max-Q Design": true,
		"AMD Radeon RX 7600M XT":                    true,
		"AMD Radeon Pro 5500M":                      true,
		"Intel Arc A770M":                           true,
		"NVIDIA GeForce RTX 4090":                   false,
		"AMD Radeon RX 7900 XTX":                    false,
		"AMD Radeon 780M Graphics":                  false,
	}
	for n, want := range laptop {
		if got := isLaptopName(n); got != want {
			t.Errorf("isLaptopName(%q) = %v", n, got)
		}
	}
}

func TestSortGPUs(t *testing.T) {
	g := []GPU{
		{Name: "igpu", Integrated: true, VRAMBytes: 32 * giB},
		{Name: "small", VRAMBytes: 8 * giB},
		{Name: "big", VRAMBytes: 24 * giB},
	}
	sortGPUs(g)
	if g[0].Name != "big" || g[1].Name != "small" || g[2].Name != "igpu" {
		t.Fatalf("order: %v", g)
	}
}
