package hw

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMeasureBandwidthQuick(t *testing.T) {
	start := time.Now()
	gbps, err := measureBandwidth(context.Background(), 50*time.Millisecond, 16<<20, 2)
	if err != nil || gbps <= 0 {
		t.Fatalf("got %v GB/s, err %v", gbps, err)
	}
	t.Logf("%.1f GB/s (16 MiB buffers, 2 workers) in %v", gbps, time.Since(start).Round(time.Millisecond))
}

func TestMeasureBandwidthCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := MeasureMemoryBandwidth(ctx, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestFillPattern(t *testing.T) {
	b := make([]byte, 10000)
	fillPattern(b)
	for i, x := range b {
		if x != byte((i%4096)*131+7) {
			t.Fatalf("byte %d = %d", i, x)
		}
	}
}
