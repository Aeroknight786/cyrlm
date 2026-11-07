package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/aeroknight786/trialm/internal/calib"
	"github.com/aeroknight786/trialm/internal/figure"
	"github.com/aeroknight786/trialm/internal/store"
)

type calibrationEventMsg struct {
	message string
	at      time.Time
	done    bool
	err     error
	event   calib.Event
}
type calibrationJob struct {
	events   chan calibrationEventMsg
	cancel   context.CancelFunc
	fraction float64
	eta      string
}

func newCalibration(parent context.Context, exe string) *calibrationJob {
	ctx, cancel := context.WithCancel(parent)
	job := &calibrationJob{events: make(chan calibrationEventMsg, 32), cancel: cancel, eta: "estimating"}
	go func() {
		defer close(job.events)
		defer cancel()
		cmd := exec.CommandContext(ctx, exe, "calibrate", "--json")
		cmd.Env = childEnv(os.Environ())
		home := store.Default().Dir
		_ = os.MkdirAll(filepath.Join(home, "logs"), 0700)
		log, err := os.OpenFile(filepath.Join(home, "logs", "calibration.log"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if err != nil {
			job.events <- calibrationEventMsg{done: true, err: err}
			return
		}
		defer log.Close()
		cmd.Stderr = log
		pipe, err := cmd.StdoutPipe()
		if err == nil {
			err = cmd.Start()
		}
		if err != nil {
			job.events <- calibrationEventMsg{done: true, err: err}
			return
		}
		sc := bufio.NewScanner(pipe)
		sc.Buffer(make([]byte, 4096), 1024*1024)
		for sc.Scan() {
			var ev calib.Event
			if json.Unmarshal(sc.Bytes(), &ev) == nil {
				select {
				case job.events <- calibrationEventMsg{message: ev.Message, event: ev, at: time.Now()}:
				case <-ctx.Done():
				}
			}
		}
		err = cmd.Wait()
		if sc.Err() != nil {
			err = sc.Err()
		}
		select {
		case job.events <- calibrationEventMsg{done: true, err: err}:
		case <-ctx.Done():
		}
	}()
	return job
}
func (j *calibrationJob) next() tea.Cmd {
	return func() tea.Msg {
		v, ok := <-j.events
		if !ok {
			return calibrationEventMsg{done: true}
		}
		return v
	}
}
func (j *calibrationJob) record(ev calib.Event) {
	if ev.Total > 0 {
		j.fraction = float64(max(0, ev.Step-1)) / float64(ev.Total)
	}
	j.eta = figure.Band(ev.ETA, 25, "s", figure.Simulated, "calibration:remaining").String()
	if ev.Total > 0 {
		j.eta = fmt.Sprintf("%s · %d/%d", j.eta, ev.Step, ev.Total)
	}
}
