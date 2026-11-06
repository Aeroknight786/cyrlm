package specs

import "testing"

func TestLookupGPU(t *testing.T) {
	cases := []struct {
		name   string
		laptop bool
		want   string
		seg    Segment
	}{
		{"NVIDIA GeForce RTX 4090", false, "RTX 4090", NVDesktop},
		{"NVIDIA GeForce RTX 4070 Ti SUPER", false, "RTX 4070 Ti Super", NVDesktop},
		{"NVIDIA GeForce RTX 4070", false, "RTX 4070", NVDesktop},
		{"NVIDIA GeForce RTX 5060 Ti", false, "RTX 5060 Ti", NVDesktop},
		{"NVIDIA GeForce RTX 4090 Laptop GPU", false, "RTX 4090 Laptop", NVLaptop},
		{"NVIDIA GeForce RTX 4060", true, "RTX 4060 Laptop", NVLaptop},
		{"AMD Radeon RX 7900 XTX", false, "RX 7900 XTX", AMDDiscrete},
		{"AMD Radeon RX 7900 XT", false, "RX 7900 XT", AMDDiscrete},
		{"AMD Radeon RX 9070 XT", false, "RX 9070 XT", AMDDiscrete},
		{"AMD Radeon(TM) 8060S Graphics", false, "Ryzen AI Max+ 395 / Radeon 8060S", StrixHalo},
		{"AMD Radeon 780M Graphics", false, "Radeon 780M", AMDiGPU},
		{"Intel(R) Arc(TM) A770 Graphics", false, "Arc A770", IntelArc},
		{"Intel(R) Arc(TM) B580 Graphics", false, "Arc B580", IntelArc},
		{"Intel(R) Arc(TM) Graphics", false, "Intel Arc Graphics (Meteor/Arrow Lake)", InteliGPU},
		{"Intel(R) UHD Graphics 770", false, "Intel UHD Graphics", InteliGPU},
	}
	for _, c := range cases {
		d, ok := LookupGPU(c.name, c.laptop)
		if !ok || d.Name != c.want || d.Segment != c.seg {
			t.Errorf("LookupGPU(%q, %v) = %q/%s ok=%v, want %q/%s", c.name, c.laptop, d.Name, d.Segment, ok, c.want, c.seg)
		}
	}
	if _, ok := LookupGPU("Matrox G200", false); ok {
		t.Error("unknown GPU should not match")
	}
}

func TestLookupApple(t *testing.T) {
	d, ok := LookupApple("Apple M4 Max", 40)
	if !ok || d.BandwidthGBs != 546 || d.Segment != AppleMax || !d.Confident {
		t.Fatalf("M4 Max 40c: %+v", d)
	}
	d, _ = LookupApple("Apple M4 Max", 32)
	if d.BandwidthGBs != 410 {
		t.Fatalf("M4 Max 32c bandwidth %v, want 410 (binned part)", d.BandwidthGBs)
	}
	d, _ = LookupApple("Apple M3 Pro", 0)
	if d.Confident || d.BandwidthGBs != 150 {
		t.Fatalf("unknown core count should be the low bin and not confident: %+v", d)
	}
	if d, _ := LookupApple("Apple M2 Ultra", 76); d.FP16TFLOPS < 27 || d.FP16TFLOPS > 28 {
		t.Fatalf("M2 Ultra 76c TFLOPS %.1f", d.FP16TFLOPS)
	}
	if _, ok := LookupApple("Intel Core i9", 0); ok {
		t.Fatal("non-Apple chip matched")
	}
}

func TestX86CPUFlagsFallback(t *testing.T) {
	// Windows reports no CPU flags; a modern family must not be treated
	// as SSE-only.
	got := X86CPU("AMD EPYC 9V45 96-Core Processor", 2, nil, 0).FP16TFLOPS
	want := X86CPU("AMD EPYC 9V45 96-Core Processor", 2, []string{"avx2"}, 0).FP16TFLOPS
	if got != want {
		t.Fatalf("unknown flags on a modern CPU: %.3f TFLOPS, want AVX2 rate %.3f", got, want)
	}
}
