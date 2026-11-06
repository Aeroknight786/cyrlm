package hw

import (
	"encoding/json"
	"testing"
)

func TestParseWindowsVideoControllersArray(t *testing.T) {
	g, err := parseWindowsVideoControllers([]byte(fixture(t, "win-video-array.json")))
	if err != nil {
		t.Fatal(err)
	}
	if len(g) != 2 { // the remote display adapter is dropped
		t.Fatalf("got %+v", g)
	}
	if n := g[0]; n.Vendor != VendorNVIDIA || n.Name != "NVIDIA GeForce RTX 4090" || n.VRAMBytes != 4293918720 || n.Driver != "32.0.15.8157" {
		t.Fatalf("nvidia: %+v", n)
	}
	if i := g[1]; i.Vendor != VendorIntel || i.Name != "Intel(R) UHD Graphics 770" || i.VRAMBytes != 128*miB {
		t.Fatalf("intel: %+v", i)
	}
}

func TestParseWindowsVideoControllersSingle(t *testing.T) {
	g, err := parseWindowsVideoControllers([]byte(fixture(t, "win-video-single.json")))
	if err != nil {
		t.Fatal(err)
	}
	if len(g) != 1 || g[0].Vendor != VendorAMD || g[0].Name != "AMD Radeon 780M Graphics" || g[0].VRAMBytes != 512*miB {
		t.Fatalf("got %+v", g)
	}
	if g, err := parseWindowsVideoControllers(nil); err != nil || len(g) != 0 {
		t.Fatalf("empty: %v %v", g, err)
	}
}

func TestParseWindowsRegistryVRAM(t *testing.T) {
	m, err := parseWindowsRegistryVRAM([]byte(fixture(t, "win-registry.json")))
	if err != nil {
		t.Fatal(err)
	}
	if m["nvidia geforce rtx 4090"] != 25757220864 || m["intel uhd graphics 770"] != 128*miB {
		t.Fatalf("got %v", m)
	}
	m, err = parseWindowsRegistryVRAM([]byte(fixture(t, "win-registry-single.json")))
	if err != nil || m["amd radeon 780m graphics"] != 512*miB {
		t.Fatalf("single: %v %v", m, err)
	}
}

func TestFlexUint(t *testing.T) {
	cases := map[string]uint64{
		`25757220864`:                    25757220864,
		`"25757220864"`:                  25757220864,
		`"0x200000000"`:                  8 * giB,
		`1.7179869184E+10`:               16 * giB,
		`[0,0,0,32]`:                     512 * miB,
		`{"value":[0,0,0,32],"Count":4}`: 512 * miB,
		`null`:                           0,
		`"n/a"`:                          0,
		`-5`:                             0,
	}
	for in, want := range cases {
		var f flexUint
		if err := json.Unmarshal([]byte(in), &f); err != nil || uint64(f) != want {
			t.Errorf("flexUint(%s) = %d, %v; want %d", in, f, err, want)
		}
	}
}

func TestParseWindowsProcessorsAndSystem(t *testing.T) {
	name, cores, threads, err := parseWindowsProcessors([]byte(fixture(t, "win-processor.json")))
	if err != nil || name != "13th Gen Intel(R) Core(TM) i9-13900K" || cores != 24 || threads != 32 {
		t.Fatalf("single: %q %d %d %v", name, cores, threads, err)
	}
	name, cores, threads, err = parseWindowsProcessors([]byte(fixture(t, "win-processor-2socket.json")))
	if err != nil || name != "Intel(R) Xeon(R) Gold 6430" || cores != 64 || threads != 128 {
		t.Fatalf("2 sockets: %q %d %d %v", name, cores, threads, err)
	}
	ws, err := parseWindowsSystem([]byte("\xef\xbb\xbf" + fixture(t, "win-system.json")))
	if err != nil || ws.TotalPhysicalMemory != 68440920064 || ws.FreePhysicalMemory != 41234560 || ws.Caption != "Microsoft Windows 11 Pro" || ws.BatteryCount != 0 {
		t.Fatalf("system: %+v %v", ws, err)
	}
	if _, err := parseWindowsSystem(nil); err == nil {
		t.Fatal("want error for empty output")
	}
}

func TestMergeWindowsGPUs(t *testing.T) {
	cim, _ := parseWindowsVideoControllers([]byte(fixture(t, "win-video-array.json")))
	reg, _ := parseWindowsRegistryVRAM([]byte(fixture(t, "win-registry.json")))
	ram := uint64(68440920064)

	// No nvidia-smi: registry gives the real 24 GiB.
	g, notes := mergeWindowsGPUs(cim, reg, nil, ram)
	if len(g) != 2 || g[0].VRAMBytes != 25757220864 || g[0].Source != "Win32_VideoController+registry" || g[0].Integrated {
		t.Fatalf("no smi: %+v", g)
	}
	if i := g[1]; !i.Integrated || !i.Unified || i.VRAMBytes != ram/2 {
		t.Fatalf("intel iGPU: %+v", i)
	}
	if len(notes) != 1 {
		t.Fatalf("notes: %v", notes)
	}

	// No registry either: AdapterRAM cap is noted.
	_, notes = mergeWindowsGPUs(cim, nil, nil, ram)
	if len(notes) != 2 {
		t.Fatalf("cap note missing: %v", notes)
	}

	// nvidia-smi replaces the WMI NVIDIA entry.
	smi := parseNvidiaSMI(fixture(t, "nvidia-smi-1gpu.txt"))
	g, _ = mergeWindowsGPUs(cim, reg, smi, ram)
	if len(g) != 2 || g[0].Source != "nvidia-smi" || g[0].FreeVRAMBytes == 0 {
		t.Fatalf("smi: %+v", g)
	}
}
