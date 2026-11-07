//go:build !darwin

package harness

// Opening observation is disabled outside Mac. Keep direct helper calls usable.
func lockOpenings(string) (func(), error) { return func() {}, nil }
