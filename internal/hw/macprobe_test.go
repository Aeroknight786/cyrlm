package hw

import (
	"reflect"
	"testing"
)

func TestParseSysctl(t *testing.T) {
	kv := parseSysctl(fixture(t, "sysctl-m4pro.txt") + "sysctl: unknown oid 'hw.nope'\n")
	if kv["hw.memsize"] != "51539607552" || kv["machdep.cpu.brand_string"] != "Apple M4 Pro" || kv["hw.physicalcpu"] != "14" {
		t.Fatalf("got %v", kv)
	}
	if _, ok := kv["sysctl"]; ok {
		t.Fatalf("error line parsed as key: %v", kv)
	}
	kv = parseSysctl(fixture(t, "sysctl-intel.txt"))
	if kv["machdep.cpu.brand_string"] != "Intel(R) Core(TM) i9-9980HK CPU @ 2.40GHz" || kv["hw.model"] != "MacBookPro16,1" {
		t.Fatalf("intel: %v", kv)
	}
}

func TestMacCPUFlags(t *testing.T) {
	kv := parseSysctl(fixture(t, "sysctl-m4pro-optional.txt"))
	if got := macCPUFlags(kv, true); !reflect.DeepEqual(got, []string{"neon", "dotprod", "i8mm", "bf16", "sme"}) {
		t.Fatalf("apple: %v", got)
	}
	kv = parseSysctl(fixture(t, "sysctl-intel-optional.txt"))
	if got := macCPUFlags(kv, false); !reflect.DeepEqual(got, []string{"avx", "avx2", "fma"}) {
		t.Fatalf("intel: %v", got)
	}
}

func TestParseVMStat(t *testing.T) {
	free, ok := parseVMStat(fixture(t, "vm_stat.txt"))
	if want := uint64(81920+819200+40960) * 16384; !ok || free != want {
		t.Fatalf("got %d ok=%v, want %d", free, ok, want)
	}
	if _, ok := parseVMStat(""); ok {
		t.Fatal("empty parsed")
	}
}

func TestParseSystemProfilerM4Pro(t *testing.T) {
	g, err := parseSystemProfilerDisplays([]byte(fixture(t, "sp-displays-m4pro.json")))
	if err != nil {
		t.Fatal(err)
	}
	want := []GPU{{Vendor: VendorApple, Name: "Apple M4 Pro", Unified: true, Cores: 20, Source: "system_profiler"}}
	if !reflect.DeepEqual(g, want) {
		t.Fatalf("got %+v", g)
	}
}

func TestParseSystemProfilerIntelMacAMD(t *testing.T) {
	g, err := parseSystemProfilerDisplays([]byte(fixture(t, "sp-displays-intel-amd.json")))
	if err != nil {
		t.Fatal(err)
	}
	if len(g) != 2 {
		t.Fatalf("got %+v", g)
	}
	if i := g[0]; i.Vendor != VendorIntel || !i.Integrated || !i.Unified || i.VRAMBytes != 1536*miB {
		t.Fatalf("intel: %+v", i)
	}
	if a := g[1]; a.Vendor != VendorAMD || a.Name != "AMD Radeon Pro 5500M" || a.Integrated || a.VRAMBytes != 8*giB {
		t.Fatalf("amd: %+v", a)
	}
	if _, err := parseSystemProfilerDisplays([]byte("not json")); err == nil {
		t.Fatal("want error")
	}
}

func TestParseSizeString(t *testing.T) {
	for in, want := range map[string]uint64{"8 GB": 8 * giB, "1536 MB": 1536 * miB, "4096MB": 4 * giB, "": 0, "spdisplays_dynamic": 0} {
		if got := parseSizeString(in); got != want {
			t.Errorf("parseSizeString(%q) = %d, want %d", in, got, want)
		}
	}
}
