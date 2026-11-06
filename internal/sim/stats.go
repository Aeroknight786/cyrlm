package sim

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Record is one simulated request.
type Record struct {
	N             int       `json:"n"`
	At            time.Time `json:"at"`
	Protocol      string    `json:"protocol"`
	ClientModel   string    `json:"client_model"`
	PromptTokens  int       `json:"prompt_tokens"`
	CachedTokens  int       `json:"cached_tokens"`
	PrefillTokens int       `json:"prefill_tokens"`
	OutputTokens  int       `json:"output_tokens"`
	Cold          bool      `json:"cold"`
	LoadReason    string    `json:"load_reason,omitempty"`
	QueueSec      float64   `json:"queue_s"`
	LoadSec       float64   `json:"load_s"`
	PrefillSec    float64   `json:"prefill_s"`
	TTFTSec       float64   `json:"ttft_s"`
	TotalSec      float64   `json:"total_s"`
	DecodeTPS     float64   `json:"decode_tps"`
	CloudSec      float64   `json:"cloud_s"`
	CloudLagSec   float64   `json:"cloud_lag_s"`
	Finish        string    `json:"finish"`
	Truncated     string    `json:"truncated,omitempty"`
	ContextError  string    `json:"context_error,omitempty"`
	CacheMiss     string    `json:"cache_miss,omitempty"`
	Error         string    `json:"error,omitempty"`
	Cancelled     bool      `json:"cancelled,omitempty"`
}

// Stats accumulates the session.
type Stats struct {
	mu      sync.Mutex
	Started time.Time `json:"started"`
	Records []Record  `json:"records"`
}

func newStats() *Stats { return &Stats{Started: time.Now()} }

// Add appends a record and returns its number.
func (s *Stats) Add(r Record) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	r.N = len(s.Records) + 1
	s.Records = append(s.Records, r)
	return r.N
}

// Snapshot returns a copy of the records.
func (s *Stats) Snapshot() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Record(nil), s.Records...)
}

// Summary is the session summary.
type Summary struct {
	Requests      int     `json:"requests"`
	SimulatedSec  float64 `json:"simulated_s"`
	CloudSec      float64 `json:"cloud_s"`
	PromptTokens  int     `json:"prompt_tokens"`
	CachedTokens  int     `json:"cached_tokens"`
	OutputTokens  int     `json:"output_tokens"`
	ColdLoads     int     `json:"cold_loads"`
	Truncations   int     `json:"truncations"`
	ContextErrors int     `json:"context_errors"`
	CacheMisses   int     `json:"cache_misses"`
	CloudLagSec   float64 `json:"cloud_lag_s"`
	MedianTTFT    float64 `json:"median_ttft_s"`
	MaxTTFT       float64 `json:"max_ttft_s"`
}

// Summarize computes the session summary.
func (s *Stats) Summarize() Summary {
	recs := s.Snapshot()
	var sum Summary
	var ttfts []float64
	for _, r := range recs {
		sum.Requests++
		sum.SimulatedSec += r.TotalSec + r.QueueSec
		sum.CloudSec += r.CloudSec
		sum.PromptTokens += r.PromptTokens
		sum.CachedTokens += r.CachedTokens
		sum.OutputTokens += r.OutputTokens
		sum.CloudLagSec += r.CloudLagSec
		if r.Cold {
			sum.ColdLoads++
		}
		if r.Truncated != "" {
			sum.Truncations++
		}
		if r.ContextError != "" {
			sum.ContextErrors++
		}
		if r.CacheMiss != "" {
			sum.CacheMisses++
		}
		if r.TTFTSec > 0 {
			ttfts = append(ttfts, r.TTFTSec)
			if r.TTFTSec > sum.MaxTTFT {
				sum.MaxTTFT = r.TTFTSec
			}
		}
	}
	if len(ttfts) > 0 {
		sortFloats(ttfts)
		sum.MedianTTFT = ttfts[len(ttfts)/2]
	}
	return sum
}

func sortFloats(a []float64) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

// Report renders the summary for the terminal.
func (sum Summary) Report(device, runtimeName, model string) string {
	if sum.Requests == 0 {
		return "No requests were simulated."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Session summary: %s on %s via %s\n", model, device, runtimeName)
	fmt.Fprintf(&b, "  %d requests · %s of model time on your device · the cloud needed %s\n",
		sum.Requests, FormatDur(sum.SimulatedSec), FormatDur(sum.CloudSec))
	if sum.PromptTokens > 0 {
		fmt.Fprintf(&b, "  prompt cache: %d%% of %s prompt tokens reused · %s generated\n",
			100*sum.CachedTokens/sum.PromptTokens, Kilo(sum.PromptTokens), Kilo(sum.OutputTokens))
	}
	fmt.Fprintf(&b, "  time to first token: median %s, worst %s\n", FormatDur(sum.MedianTTFT), FormatDur(sum.MaxTTFT))
	if sum.ColdLoads > 0 {
		fmt.Fprintf(&b, "  model (re)loads: %d\n", sum.ColdLoads)
	}
	if sum.CacheMisses > 0 {
		fmt.Fprintf(&b, "  prompt-cache misses: %d (see the log lines marked \"cache miss\")\n", sum.CacheMisses)
	}
	if sum.Truncations > 0 {
		fmt.Fprintf(&b, "  ⚠ %d prompts were truncated to fit the context window\n", sum.Truncations)
	}
	if sum.ContextErrors > 0 {
		fmt.Fprintf(&b, "  ⚠ %d requests were rejected for exceeding the context window\n", sum.ContextErrors)
	}
	if sum.CloudLagSec > 1 {
		fmt.Fprintf(&b, "  note: the cloud was slower than your simulated device for %s in total; those moments ran at cloud speed\n", FormatDur(sum.CloudLagSec))
	}
	return b.String()
}

// FormatDur formats seconds compactly ("850 ms", "4.2 s", "3 min 05 s").
func FormatDur(s float64) string {
	switch {
	case s < 1:
		return fmt.Sprintf("%.0f ms", s*1000)
	case s < 10:
		return fmt.Sprintf("%.1f s", s)
	case s < 90:
		return fmt.Sprintf("%.0f s", s)
	case s < 3600:
		return fmt.Sprintf("%d min %02d s", int(s)/60, int(s)%60)
	}
	return fmt.Sprintf("%d h %02d min", int(s)/3600, (int(s)%3600)/60)
}

// Kilo formats a token count ("950", "12.3k").
func Kilo(n int) string {
	if n < 1000 {
		return fmt.Sprint(n)
	}
	if n < 100000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%.0fk", float64(n)/1000)
}
