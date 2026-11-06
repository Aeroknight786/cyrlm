package hw

import (
	"context"
	"errors"
	"runtime"
	"runtime/debug"
	"sync"
	"time"
)

// bandwidthBufBytes is the size of each of the two copy buffers: far beyond
// any last-level cache, and 2× this stays under the 600 MiB budget.
const bandwidthBufBytes = 256 << 20

// MeasureMemoryBandwidth estimates sustained memory bandwidth in GB/s
// (GB = 1e9 bytes), counting bytes read plus bytes written, with a
// multi-threaded copy between two 256 MiB buffers split across GOMAXPROCS
// goroutines. It runs for about d (at least three timed passes after a
// warm-up pass that faults the pages in) and reports the best pass. Memory
// use is bounded at 512 MiB and released before returning. It returns early
// with ctx.Err() if ctx is cancelled before any pass completes.
func MeasureMemoryBandwidth(ctx context.Context, d time.Duration) (gbps float64, err error) {
	return measureBandwidth(ctx, d, bandwidthBufBytes, runtime.GOMAXPROCS(0))
}

func measureBandwidth(ctx context.Context, d time.Duration, bufBytes, workers int) (float64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if workers < 1 {
		workers = 1
	}
	chunk := (bufBytes / workers) &^ 4095
	if chunk == 0 {
		workers, chunk = 1, bufBytes
	}
	size := chunk * workers

	src := make([]byte, size)
	dst := make([]byte, size)
	defer func() {
		src, dst = nil, nil
		debug.FreeOSMemory()
	}()

	pass := func(fn func(s, d []byte)) time.Duration {
		var wg sync.WaitGroup
		wg.Add(workers)
		start := time.Now()
		for w := 0; w < workers; w++ {
			lo, hi := w*chunk, (w+1)*chunk
			go func() {
				defer wg.Done()
				fn(src[lo:hi], dst[lo:hi])
			}()
		}
		wg.Wait()
		return time.Since(start)
	}

	start := time.Now()
	// Warm-up: write real data into src (untouched pages would all map the
	// shared zero page and read impossibly fast) and fault dst in.
	// Done in 4 MiB slices so a cancelled ctx is noticed promptly.
	pass(func(s, d []byte) {
		const step = 4 << 20
		for lo := 0; lo < len(s) && ctx.Err() == nil; lo += step {
			hi := min(lo+step, len(s))
			fillPattern(s[lo:hi])
			copy(d[lo:hi], s[lo:hi])
		}
	})

	best := 0.0
	for n := 0; ; n++ {
		if ctx.Err() != nil {
			break
		}
		el := pass(func(s, d []byte) { copy(d, s) })
		if sec := el.Seconds(); sec > 0 {
			if g := float64(2*size) / sec / 1e9; g > best {
				best = g
			}
		}
		if n >= 2 && time.Since(start) >= d {
			break
		}
	}
	if best == 0 {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		return 0, errors.New("hw: bandwidth measurement produced no timing")
	}
	return best, nil
}

// fillPattern fills b with a non-zero byte pattern by doubling copies.
func fillPattern(b []byte) {
	if len(b) == 0 {
		return
	}
	seed := min(len(b), 4096)
	for i := 0; i < seed; i++ {
		b[i] = byte(i*131 + 7)
	}
	for filled := seed; filled < len(b); filled *= 2 {
		copy(b[filled:], b[:filled])
	}
}
