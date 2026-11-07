package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/aeroknight786/trialm/internal/store"
)

func TestChatServerParentPipe(t *testing.T) {
	home := t.TempDir()
	exe, _ := os.Executable()
	args := []string{"try", "qwen3:8b", "--serve", "--json", "--upstream", "mock", "--for", "generic", "--use", "chat", "--device", "RTX 4090", "--ram", "64", "--port", "0"}
	raw, _ := json.Marshal(args)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe)
	cmd.Env = append(os.Environ(), goldenChildEnv+"="+string(raw), "TRIALM_HOME="+home, "HOME="+filepath.Join(home, "home"), "XDG_CONFIG_HOME="+filepath.Join(home, "config"), "TRIALM_PARENT_PIPE=1", "TRIALM_OFFLINE=1", "OPENROUTER_API_KEY=")
	in, e := cmd.StdinPipe()
	if e != nil {
		t.Fatal(e)
	}
	pipe, e := cmd.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { _ = cmd.Process.Kill() }()
	sc := bufio.NewScanner(pipe)
	ready := false
	for sc.Scan() {
		var event struct {
			Type string
			Data struct{ Ready bool }
		}
		_ = json.Unmarshal(sc.Bytes(), &event)
		if event.Type == "progress" && event.Data.Ready {
			ready = true
			break
		}
	}
	if !ready {
		t.Fatal("server never ready", stderr.String())
	}
	_ = in.Close()
	for sc.Scan() {
	}
	if e = cmd.Wait(); e != nil {
		t.Fatal(e, stderr.String())
	}
	rows, e := (store.Store{Dir: home}).History()
	if e != nil || len(rows) != 1 {
		t.Fatal(rows, e)
	}
}
