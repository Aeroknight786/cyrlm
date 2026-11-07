package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
		// SIGKILL would orphan the child's llama-bench. An interrupt lets
		// calibrate stop it, save finished rows and delete its temp file.
		cmd.Cancel = func() error {
			if err := cmd.Process.Signal(os.Interrupt); err != nil {
				return cmd.Process.Kill() // Windows has no interrupt signal
			}
			return nil
		}
		cmd.WaitDelay = 15 * time.Second
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

// stop cancels the run and blocks until the child has exited.
func (j *calibrationJob) stop() {
	j.cancel()
	for range j.events {
	}
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

// calibrationStatus turns a calibrate --json event into words: the raw
// messages carry llama-bench flags and probe ids meant for the log.
func calibrationStatus(probe, msg string) string {
	what := probe
	if f := strings.Split(probe, "-"); len(f) >= 4 {
		what = strings.Join(f[1:len(f)-2], "-") + " " + f[len(f)-2] + " · " + f[len(f)-1] + " layers"
	}
	var depth int
	switch {
	case strings.HasPrefix(msg, "Generating or loading"):
		return "Writing a synthetic model · " + what
	case strings.HasPrefix(msg, "Benchmarking"):
		return "Timing reading and writing on this GPU · " + what
	case strings.HasPrefix(msg, "Using finished rows"):
		return "Reusing finished measurements · " + what
	case strings.HasPrefix(msg, "Saved depth"):
		if _, err := fmt.Sscanf(msg, "Saved depth %d", &depth); err == nil {
			return fmt.Sprintf("Measured at %dk context · %s", depth/1024, what)
		}
	}
	return msg
}
