// Package hw detects the local machine: OS, CPU, RAM, GPUs and installed
// local-LLM runtimes. Detection shells out to standard platform tools with
// short timeouts and parses their output with pure functions (unit-tested
// against captured fixtures in testdata/). It never needs admin rights and
// never sends data anywhere.
package hw

// GPU vendors.
const (
	VendorNVIDIA = "nvidia"
	VendorAMD    = "amd"
	VendorIntel  = "intel"
	VendorApple  = "apple"
	VendorOther  = "other"
)

// GPU is one graphics/accelerator device.
type GPU struct {
	Vendor string `json:"vendor"`
	// Name is the marketing name as reported, e.g. "NVIDIA GeForce RTX 4090",
	// "Apple M4 Pro", "AMD Radeon RX 7900 XTX", "AMD Radeon 8060S Graphics".
	Name string `json:"name"`
	// VRAMBytes is dedicated memory. For unified-memory devices (Apple, AMD
	// APUs, Intel iGPUs) it is the memory the GPU can address, see Unified.
	VRAMBytes uint64 `json:"vram_bytes"`
	// FreeVRAMBytes is currently free dedicated memory when known (0 = unknown).
	FreeVRAMBytes uint64 `json:"free_vram_bytes,omitempty"`
	// Unified is true when the GPU shares system RAM (Apple Silicon, APUs,
	// iGPUs, NVIDIA GB10/Grace).
	Unified bool `json:"unified"`
	// Integrated is true for iGPUs/APUs (not Apple, which is reported as
	// Unified but not Integrated).
	Integrated bool `json:"integrated"`
	// Cores is the GPU core count when known (Apple GPU cores).
	Cores int `json:"cores,omitempty"`
	// Driver is the driver version when known.
	Driver string `json:"driver,omitempty"`
	// Backends the hardware can use: "cuda", "metal", "rocm", "vulkan",
	// "sycl". This is capability, not proof that a working build exists.
	Backends []string `json:"backends,omitempty"`
	// Laptop is true when the name or platform indicates a mobile GPU.
	Laptop bool `json:"laptop,omitempty"`
	// Source describes how the device was detected (e.g. "nvidia-smi").
	Source string `json:"source,omitempty"`
}

// System is the detected machine.
type System struct {
	OS   string `json:"os"`   // runtime.GOOS
	Arch string `json:"arch"` // runtime.GOARCH
	// OSVersion is a human-readable OS version when known.
	OSVersion string `json:"os_version,omitempty"`
	CPUName   string `json:"cpu_name"`
	// CPUCores is physical cores (performance + efficiency on Apple).
	CPUCores int `json:"cpu_cores"`
	// CPUThreads is logical processors.
	CPUThreads int `json:"cpu_threads"`
	// CPUPerfCores is Apple performance cores (0 when not applicable).
	CPUPerfCores int `json:"cpu_perf_cores,omitempty"`
	// CPUFlags of interest when known: "avx2", "avx512f", "neon", "amx".
	CPUFlags     []string `json:"cpu_flags,omitempty"`
	RAMBytes     uint64   `json:"ram_bytes"`
	FreeRAMBytes uint64   `json:"free_ram_bytes,omitempty"`
	GPUs         []GPU    `json:"gpus"`
	// AppleChip is e.g. "Apple M4 Pro" (empty when not Apple Silicon).
	AppleChip string `json:"apple_chip,omitempty"`
	// GPUWiredLimitBytes is the macOS GPU wired-memory limit
	// (sysctl iogpu.wired_limit_mb) when explicitly set; 0 = macOS default.
	GPUWiredLimitBytes uint64 `json:"gpu_wired_limit_bytes,omitempty"`
	// Notes are human-readable detection caveats.
	Notes []string `json:"notes,omitempty"`
}

// Runtime identifiers for installed-runtime detection.
const (
	RuntimeOllama   = "ollama"
	RuntimeLMStudio = "lmstudio"
	RuntimeLlamaCpp = "llamacpp"
	RuntimeMLXLM    = "mlx-lm"
	RuntimeVLLM     = "vllm"
	RuntimeJan      = "jan"
	RuntimeLemonade = "lemonade"
	RuntimeDocker   = "docker-model-runner"
)

// InstalledRuntime is a local-LLM runtime found on the machine.
type InstalledRuntime struct {
	// ID is one of the Runtime* constants.
	ID string `json:"id"`
	// Version as reported, when known (e.g. "0.12.6", "b6765").
	Version string `json:"version,omitempty"`
	// Path is the executable or app bundle found.
	Path string `json:"path,omitempty"`
	// Running is true when its local server answered on its default port.
	Running bool `json:"running"`
	// Endpoint is the server URL when Running.
	Endpoint string `json:"endpoint,omitempty"`
	// Backend is the compute backend when it can be determined
	// ("cuda", "metal", "vulkan", "rocm", "cpu"), else empty.
	Backend string `json:"backend,omitempty"`
	// Source describes how it was found.
	Source string `json:"source,omitempty"`
}
