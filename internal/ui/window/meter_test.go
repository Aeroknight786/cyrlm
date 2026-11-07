package window

import (
	"context"
	"testing"
	"time"

	"github.com/aeroknight786/trialm/internal/store"
	"github.com/aeroknight786/trialm/internal/trial"
)

func TestMeterCheckpointsAndFinalCard(t *testing.T) {
	s := store.Store{Dir: t.TempDir()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = runStore(s).Write("active.json", map[string]string{"ID": "a"})
	row := store.TrialEntry{At: time.Now(), Model: "fake"}
	done := Track(ctx, s, "a", 60, row, func() trial.Report {
		return trial.Report{Requests: 2, SpendKnown: true, SpendUSD: .01, LocalSeconds: 10}
	})
	deadline := time.Now().Add(time.Second)
	for ReadMeter(s).Request == "" {
		if time.Now().After(deadline) {
			t.Fatal("no meter")
		}
		time.Sleep(time.Millisecond)
	}
	rows, e := s.History()
	if e != nil || len(rows) != 1 || !rows[0].Incomplete || rows[0].Report.Requests != 2 {
		t.Fatal(rows, e)
	}
	row = rows[0]
	row.Incomplete = false
	if e = s.SaveTrial(row); e != nil {
		t.Fatal(e)
	}
	cancel()
	<-done
}
