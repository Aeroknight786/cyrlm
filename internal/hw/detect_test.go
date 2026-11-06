package hw

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDetectLinuxLaptopNVIDIAIntel(t *testing.T) {
	f := &fakeRunner{
		files: map[string]string{
			"/proc/meminfo":                      fixture(t, "meminfo.txt"),
			"/proc/cpuinfo":                      fixture(t, "cpuinfo-x86-avx512.txt"),
			"/etc/os-release":                    fixture(t, "os-release.txt"),
			"/proc/sys/kernel/osrelease":         "6.8.0-85-generic\n",
			"/sys/class/drm/card0/device/vendor": "0x8086\n",
			"/sys/class/drm/card0/device/device": "0x46a6\n",
			"/sys/class/drm/card0/device/uevent": "DRIVER=i915\nPCI_CLASS=30000\nPCI_SLOT_NAME=0000:00:02.0\n",
			"/sys/class/drm/card1/device/vendor": "0x10de\n",
			"/sys/class/drm/card1/device/device": "0x2860\n",
			"/sys/class/drm/card1/device/uevent": "DRIVER=nvidia\nPCI_SLOT_NAME=0000:01:00.0\n",
			"/sys/class/power_supply/BAT0/type":  "Battery\n",
		},
		paths: map[string]string{"nvidia-smi": "/usr/bin/nvidia-smi", "lspci": "/usr/bin/lspci"},
		cmds: map[string]fakeOut{
			"/usr/bin/nvidia-smi --query-gpu=name,memory.total,memory.free,driver_version --format=csv,noheader,nounits": {out: fixture(t, "nvidia-smi-laptop.txt")},
			"/usr/bin/lspci -D -mm -nn": {out: fixture(t, "lspci-mm-nn.txt")},
		},
	}
	s, err := detector{r: f, goos: "linux", goarch: "amd64", numCPU: 99}.detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.OS != "linux" || s.Arch != "amd64" || s.OSVersion != "Ubuntu 24.04.3 LTS" {
		t.Fatalf("os: %+v", s)
	}
	if s.RAMBytes != 65536000*1024 || s.FreeRAMBytes != 52428800*1024 {
		t.Fatalf("ram: %d free %d", s.RAMBytes, s.FreeRAMBytes)
	}
	if s.CPUCores != 2 || s.CPUThreads != 4 || !contains(s.CPUFlags, "avx512f") || !strings.Contains(s.CPUName, "i3-1115G4") {
		t.Fatalf("cpu: %+v", s)
	}
	if len(s.GPUs) != 2 {
		t.Fatalf("gpus: %+v", s.GPUs)
	}
	n, i := s.GPUs[0], s.GPUs[1]
	if n.Name != "NVIDIA GeForce RTX 4070 Laptop GPU" || !n.Laptop || n.VRAMBytes != 8188*miB || !reflect.DeepEqual(n.Backends, []string{"cuda", "vulkan"}) {
		t.Fatalf("nvidia: %+v", n)
	}
	if i.Name != "Intel Iris Xe Graphics" || !i.Integrated || !i.Unified || i.Laptop || i.VRAMBytes != s.RAMBytes/2 || !reflect.DeepEqual(i.Backends, []string{"sycl", "vulkan"}) {
		t.Fatalf("intel: %+v", i)
	}
	if !hasNote(s, "integrated GPU") {
		t.Fatalf("notes: %v", s.Notes)
	}
}

func TestDetectLinuxDegraded(t *testing.T) {
	// WSL-like box: no /proc/cpuinfo, nvidia-smi fails, an NVIDIA card in
	// sysfs, no lspci.
	f := &fakeRunner{
		files: map[string]string{
			"/proc/meminfo":                      fixture(t, "meminfo.txt"),
			"/proc/sys/kernel/osrelease":         "6.6.87.2-microsoft-standard-WSL2\n",
			"/sys/class/drm/card0/device/vendor": "0x10de\n",
			"/sys/class/drm/card0/device/device": "0x2684\n",
			"/sys/class/drm/card0/device/uevent": "PCI_SLOT_NAME=0000:01:00.0\n",
		},
		paths: map[string]string{"nvidia-smi": "/usr/lib/wsl/lib/nvidia-smi", "nproc": "/usr/bin/nproc"},
		cmds: map[string]fakeOut{
			"--query-gpu":    {out: fixture(t, "nvidia-smi-error.txt"), err: errors.New("exit status 9")},
			"/usr/bin/nproc": {out: "8\n"},
		},
	}
	s, _ := detector{r: f, goos: "linux", goarch: "amd64", numCPU: 99}.detect(context.Background())
	if s.CPUThreads != 8 || s.CPUCores != 8 {
		t.Fatalf("nproc fallback: %+v", s)
	}
	if !strings.HasSuffix(s.OSVersion, "(WSL2)") || !hasNote(s, "WSL2") || !hasNote(s, "couldn't communicate") ||
		!hasNote(s, "nvidia-smi is unavailable") || !hasNote(s, "core count unknown") || !hasNote(s, "/proc/cpuinfo") {
		t.Fatalf("notes: %q (os %q)", s.Notes, s.OSVersion)
	}
	if len(s.GPUs) != 1 || s.GPUs[0].Name != "NVIDIA GPU [10de:2684]" {
		t.Fatalf("gpus: %+v", s.GPUs)
	}
}

func TestDetectLinuxGB10(t *testing.T) {
	f := &fakeRunner{
		files: map[string]string{"/proc/meminfo": "MemTotal: 125829120 kB\nMemAvailable: 120000000 kB\n"},
		paths: map[string]string{"nvidia-smi": "/usr/bin/nvidia-smi"},
		cmds:  map[string]fakeOut{"--query-gpu": {out: fixture(t, "nvidia-smi-gb10.txt")}},
	}
	s, _ := detector{r: f, goos: "linux", goarch: "arm64", numCPU: 20}.detect(context.Background())
	if len(s.GPUs) != 1 || !s.GPUs[0].Unified || s.GPUs[0].VRAMBytes != 125829120*1024 || !hasNote(s, "unified memory") {
		t.Fatalf("gb10: %+v notes %v", s.GPUs, s.Notes)
	}
	if !contains(s.CPUFlags, "neon") {
		t.Fatalf("arm64 flags: %v", s.CPUFlags)
	}
}

func darwinRunner(t *testing.T, base, opt, sp string) *fakeRunner {
	return &fakeRunner{cmds: map[string]fakeOut{
		"/usr/sbin/sysctl hw.memsize":                        {out: fixture(t, base)},
		"/usr/sbin/sysctl hw.perflevel0.physicalcpu":         {out: opt, err: errors.New("exit status 1")},
		"/usr/sbin/system_profiler SPDisplaysDataType -json": {out: fixture(t, sp)},
		"/usr/bin/sw_vers -productVersion":                   {out: "15.6.1\n"},
		"/usr/bin/vm_stat":                                   {out: fixture(t, "vm_stat.txt")},
	}}
}

func TestDetectDarwinAppleSilicon(t *testing.T) {
	f := darwinRunner(t, "sysctl-m4pro.txt", fixture(t, "sysctl-m4pro-optional.txt"), "sp-displays-m4pro.json")
	s, err := detector{r: f, goos: "darwin", goarch: "arm64", numCPU: 14}.detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.AppleChip != "Apple M4 Pro" || s.CPUName != "Apple M4 Pro" || s.CPUCores != 14 || s.CPUThreads != 14 || s.CPUPerfCores != 10 {
		t.Fatalf("cpu: %+v", s)
	}
	if s.RAMBytes != 48*giB || s.FreeRAMBytes != uint64(81920+819200+40960)*16384 || s.OSVersion != "macOS 15.6.1" || s.GPUWiredLimitBytes != 0 {
		t.Fatalf("mem/os: %+v", s)
	}
	if !reflect.DeepEqual(s.CPUFlags, []string{"neon", "dotprod", "i8mm", "bf16", "sme"}) {
		t.Fatalf("flags: %v", s.CPUFlags)
	}
	want := []GPU{{Vendor: VendorApple, Name: "Apple M4 Pro", VRAMBytes: 48 * giB, Unified: true, Cores: 20,
		Backends: []string{"metal"}, Source: "system_profiler"}}
	if !reflect.DeepEqual(s.GPUs, want) {
		t.Fatalf("gpus:\n got %+v\nwant %+v", s.GPUs, want)
	}
	if len(s.Notes) != 0 {
		t.Fatalf("unexpected notes: %v", s.Notes)
	}

	// Explicit wired limit; system_profiler unavailable.
	f = darwinRunner(t, "sysctl-m4pro.txt", "iogpu.wired_limit_mb: 40960\nhw.perflevel0.physicalcpu: 10\n", "sp-displays-m4pro.json")
	f.cmds["/usr/sbin/system_profiler SPDisplaysDataType -json"] = fakeOut{err: errors.New("system_profiler: timed out")}
	s, _ = detector{r: f, goos: "darwin", goarch: "arm64"}.detect(context.Background())
	if s.GPUWiredLimitBytes != 40960*miB || len(s.GPUs) != 1 || s.GPUs[0].Name != "Apple M4 Pro" || s.GPUs[0].Cores != 0 || !hasNote(s, "core count unknown") {
		t.Fatalf("fallback: %+v", s)
	}
}

func TestDetectDarwinIntelAMD(t *testing.T) {
	f := darwinRunner(t, "sysctl-intel.txt", fixture(t, "sysctl-intel-optional.txt"), "sp-displays-intel-amd.json")
	s, _ := detector{r: f, goos: "darwin", goarch: "amd64"}.detect(context.Background())
	if s.AppleChip != "" || s.CPUCores != 8 || s.CPUThreads != 16 || s.CPUPerfCores != 0 || !reflect.DeepEqual(s.CPUFlags, []string{"avx", "avx2", "fma"}) {
		t.Fatalf("cpu: %+v", s)
	}
	if len(s.GPUs) != 2 {
		t.Fatalf("gpus: %+v", s.GPUs)
	}
	a, i := s.GPUs[0], s.GPUs[1]
	if a.Vendor != VendorAMD || a.VRAMBytes != 8*giB || !a.Laptop || !reflect.DeepEqual(a.Backends, []string{"metal"}) {
		t.Fatalf("amd: %+v", a)
	}
	if i.Vendor != VendorIntel || !i.Integrated || i.Laptop {
		t.Fatalf("intel: %+v", i)
	}
}

func windowsRunner(t *testing.T, video, reg, cpu, sys string, smi bool) *fakeRunner {
	f := &fakeRunner{
		env:   map[string]string{"SystemRoot": `C:\Windows`},
		paths: map[string]string{"powershell": `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`},
		cmds: map[string]fakeOut{
			"Win32_VideoController": {out: fixture(t, video)},
			"qwMemorySize":          {out: fixture(t, reg)},
			"Win32_Processor":       {out: fixture(t, cpu)},
			"Win32_ComputerSystem":  {out: fixture(t, sys)},
		},
	}
	if smi {
		f.paths["nvidia-smi"] = `C:\Windows\System32\nvidia-smi.exe`
		f.cmds["--query-gpu"] = fakeOut{out: fixture(t, "nvidia-smi-1gpu.txt")}
	}
	return f
}

func TestDetectWindowsRTX4090(t *testing.T) {
	f := windowsRunner(t, "win-video-array.json", "win-registry.json", "win-processor.json", "win-system.json", true)
	s, err := detector{r: f, goos: "windows", goarch: "amd64"}.detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !f.called(`run C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe -NoProfile -NonInteractive -Command`) {
		t.Fatalf("powershell not invoked as expected: %v", f.calls)
	}
	if s.RAMBytes != 68440920064 || s.FreeRAMBytes != 41234560*1024 || s.OSVersion != "Microsoft Windows 11 Pro 10.0.26100" {
		t.Fatalf("system: %+v", s)
	}
	if s.CPUName != "13th Gen Intel(R) Core(TM) i9-13900K" || s.CPUCores != 24 || s.CPUThreads != 32 {
		t.Fatalf("cpu: %+v", s)
	}
	if len(s.GPUs) != 2 {
		t.Fatalf("gpus: %+v", s.GPUs)
	}
	n, i := s.GPUs[0], s.GPUs[1]
	if n.Source != "nvidia-smi" || n.VRAMBytes != 24564*miB || n.Laptop {
		t.Fatalf("nvidia: %+v", n)
	}
	if i.Name != "Intel(R) UHD Graphics 770" || !i.Integrated || i.VRAMBytes != s.RAMBytes/2 {
		t.Fatalf("intel: %+v", i)
	}
}

func TestDetectWindowsRadeon780MLaptop(t *testing.T) {
	f := windowsRunner(t, "win-video-single.json", "win-registry-single.json", "win-processor.json", "win-system-laptop.json", false)
	f.paths = map[string]string{} // powershell found via %SystemRoot%
	f.exists = map[string]bool{`C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`: true}
	s, _ := detector{r: f, goos: "windows", goarch: "amd64"}.detect(context.Background())
	if len(s.GPUs) != 1 {
		t.Fatalf("gpus: %+v notes %v", s.GPUs, s.Notes)
	}
	g := s.GPUs[0]
	if g.Vendor != VendorAMD || !g.Integrated || !g.Unified || g.VRAMBytes != s.RAMBytes/2 || g.Laptop || !reflect.DeepEqual(g.Backends, []string{"rocm", "vulkan"}) {
		t.Fatalf("780M: %+v", g)
	}
	if !hasNote(s, "integrated GPU") {
		t.Fatalf("notes: %v", s.Notes)
	}
}

func TestDetectWindowsNoPowerShell(t *testing.T) {
	s, err := detector{r: &fakeRunner{}, goos: "windows", goarch: "amd64", numCPU: 12}.detect(context.Background())
	if err != nil || s == nil || s.CPUThreads != 12 || !hasNote(s, "powershell not found") || !hasNote(s, "RAM could not be detected") {
		t.Fatalf("got %+v err %v", s, err)
	}
	if b, _ := json.Marshal(s); !strings.Contains(string(b), `"gpus":[]`) {
		t.Fatalf("gpus should marshal as []: %s", b)
	}
}

func TestDetectUnknownOSAndCancel(t *testing.T) {
	s, err := detector{r: &fakeRunner{}, goos: "plan9", goarch: "amd64", numCPU: 3}.detect(context.Background())
	if err != nil || s.CPUThreads != 3 || !hasNote(s, "not implemented for plan9") {
		t.Fatalf("got %+v err %v", s, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s, err = detector{r: &fakeRunner{}, goos: "linux", goarch: "amd64", numCPU: 3}.detect(ctx)
	if s == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v %v", s, err)
	}
}

// TestDetectRealMachine runs the real probes on the test host and logs the
// result (go test -v -run Real). It only checks invariants.
func TestDetectRealMachine(t *testing.T) {
	start := time.Now()
	s, err := Detect(context.Background())
	el := time.Since(start)
	if err != nil || s == nil {
		t.Fatalf("Detect: %v", err)
	}
	if s.OS != runtime.GOOS || s.CPUThreads <= 0 || el > 6*time.Second {
		t.Fatalf("bad result after %v: %+v", el, s)
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	t.Logf("Detect() in %v:\n%s", el.Round(time.Millisecond), b)

	start = time.Now()
	rts := DetectRuntimes(context.Background())
	el = time.Since(start)
	if rts == nil || el > 6*time.Second {
		t.Fatalf("DetectRuntimes: %v after %v", rts, el)
	}
	b, _ = json.MarshalIndent(rts, "", "  ")
	t.Logf("DetectRuntimes() in %v:\n%s", el.Round(time.Millisecond), b)
}
