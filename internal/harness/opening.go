package harness

import (
	"sort"
	"sync"

	"github.com/aeroknight786/trialm/internal/predict"
	"github.com/aeroknight786/trialm/internal/store"
)

// Only counts are persisted; no prompt text or tool definitions leave the route.
var openingMu sync.Mutex

const openingFile = "opening-prompts.json"

// RecordOpeningPrompt retains ten successful conversation openings per tool.
func RecordOpeningPrompt(id string, tokens int) error {
	if id == "" || tokens <= 0 {
		return nil
	}
	openingMu.Lock()
	defer openingMu.Unlock()
	s := store.Default()
	unlock, err := lockOpenings(s.Dir)
	if err != nil {
		return err
	}
	defer unlock()
	samples := map[string][]int{}
	_ = s.Read(openingFile, &samples)
	if samples == nil {
		samples = map[string][]int{}
	}
	samples[id] = append(samples[id], tokens)
	if len(samples[id]) > 10 {
		samples[id] = samples[id][len(samples[id])-10:]
	}
	return s.Write(openingFile, samples)
}

// OpeningScenario uses local evidence on Mac; other platforms keep their defaults.
func OpeningScenario(sc predict.Scenario, goos string) predict.Scenario {
	if goos != "darwin" {
		return sc
	}
	openingMu.Lock()
	defer openingMu.Unlock()
	var samples map[string][]int
	if store.Default().Read(openingFile, &samples) != nil {
		return sc
	}
	ns := samples[sc.Harness]
	ns = append([]int(nil), ns...)
	ns = slicesPositive(ns)
	if len(ns) == 0 {
		return sc
	}
	sort.Ints(ns)
	sc.BasePromptTokens = ns[len(ns)/2]
	if len(ns)%2 == 0 {
		sc.BasePromptTokens = ns[len(ns)/2-1] + (ns[len(ns)/2]-ns[len(ns)/2-1])/2
	}
	return sc
}
func slicesPositive(ns []int) []int {
	out := ns[:0]
	for _, n := range ns {
		if n > 0 {
			out = append(out, n)
		}
	}
	return out
}
