package setup

import (
	"bufio"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aeroknight786/trialm/internal/setup/download"
	"github.com/aeroknight786/trialm/internal/verify"
)

type interruptedDownload struct{ resumeDownloader }

func (d *interruptedDownload) Fetch(ctx context.Context, r download.Request) (download.Result, error) {
	if _, err := os.Stat(r.Dest + ".part"); errors.Is(err, os.ErrNotExist) {
		if err = os.MkdirAll(filepath.Dir(r.Dest), 0700); err != nil {
			return download.Result{}, err
		}
		if err = os.WriteFile(r.Dest+".part", d.data[:61], 0600); err != nil {
			return download.Result{}, err
		}
		d.progress(download.Progress{Done: 61, Total: 100})
		fmt.Println("download 61%")
		<-ctx.Done()
		return download.Result{}, ctx.Err()
	}
	return d.resumeDownloader.Fetch(ctx, r)
}
func TestSetupSignalChild(t *testing.T) {
	if os.Getenv("TRIALM_SETUP_SIGNAL_CHILD") != "1" {
		t.Skip("subprocess helper")
	}
	home := os.Getenv("TRIALM_SETUP_SIGNAL_HOME")
	data := []byte(strings.Repeat("x", 100))
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	downloader := &interruptedDownload{resumeDownloader: resumeDownloader{data: data}}
	executor := &Executor{Root: filepath.Join(home, ".trialm"), Home: home, Downloader: downloader, Ready: func(context.Context, verify.ReadyOptions) (verify.Report, error) {
		return verify.Report{Ready: true}, nil
	}}
	plan := basicPlan(Step{ID: "model", Kind: Fetch, Request: &download.Request{URL: "https://huggingface.co/o/r/resolve/rev/model.gguf", SHA256: digest, Size: 100, Dest: "~/.trialm/models/model.gguf"}})
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	journal, err := executor.Apply(ctx, plan, Policy{PlanID: plan.ID})
	if errors.Is(err, context.Canceled) && journal.State == "paused" {
		os.Exit(130)
	}
	if err != nil || journal.State != "ready" || !downloader.resumed {
		t.Fatal(journal.State, downloader.resumed, err)
	}
	fmt.Println("Ready resumed")
}
func TestSIGINTMidFetchThenRerunResumes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Process.Signal cannot deliver SIGINT on Windows")
	}
	executor := testExecutor(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := func() *exec.Cmd {
		cmd := exec.CommandContext(ctx, exe, "-test.run=^TestSetupSignalChild$", "-test.v")
		cmd.Env = append(os.Environ(), "TRIALM_SETUP_SIGNAL_CHILD=1", "TRIALM_SETUP_SIGNAL_HOME="+executor.Home)
		return cmd
	}
	first := command()
	output, err := first.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = first.Start(); err != nil {
		t.Fatal(err)
	}
	defer first.Process.Kill()
	scanner := bufio.NewScanner(output)
	interrupted := false
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), "download 61%") {
			if err = first.Process.Signal(os.Interrupt); err != nil {
				t.Fatal(err)
			}
			interrupted = true
			break
		}
	}
	err = first.Wait()
	var exited *exec.ExitError
	if !interrupted || !errors.As(err, &exited) || exited.ExitCode() != 130 {
		t.Fatal("SIGINT did not pause setup", interrupted, err)
	}
	saved, err := executor.Status("test")
	if err != nil || saved.State != "paused" || saved.Steps[0].Progress.Done != 61 {
		t.Fatal(saved.State, err)
	}
	t.Log("SIGINT paused at download 61% with exit 130")
	second := command()
	b, err := second.CombinedOutput()
	if err != nil || !strings.Contains(string(b), "Ready resumed") {
		t.Fatalf("rerun: %s %v", b, err)
	}
	saved, err = executor.Status("test")
	if err != nil || saved.State != "ready" || saved.Prompts.Approvals != 1 {
		t.Fatal(saved.State, saved.Prompts, err)
	}
	t.Log("rerun resumed the partial file and reached Ready with one approval")
}
