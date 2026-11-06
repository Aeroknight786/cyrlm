package account

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"
)

func TestConfigRoundTripAndPermissions(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CYRLM_HOME", dir)
	t.Setenv("CYRLM_KEY", "")
	t.Setenv("CYRLM_API_URL", "")
	c := Load()
	if c.Share != ShareAsk || c.LoggedIn() || c.CanShare() {
		t.Fatalf("defaults: %+v", c)
	}
	c.Key, c.APIURL, c.Share = "cyrlm_abcdef123456", "https://example.invalid", ShareAlways
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	// Windows has no Unix mode bits; the user-profile ACL protects the file there.
	if goruntime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0 {
		t.Fatalf("config must be owner-only, got %v", fi.Mode().Perm())
	}
	got := Load()
	if got.Key != c.Key || got.Share != ShareAlways || !got.CanShare() {
		t.Fatalf("reload: %+v", got)
	}
	if m := got.MaskedKey(); strings.Contains(m, "abcdef123") {
		t.Fatalf("masked key leaks: %s", m)
	}
	t.Setenv("CYRLM_KEY", "envkey")
	if Load().Key != "envkey" {
		t.Fatal("CYRLM_KEY should override the file")
	}
}

func sampleReport() Report {
	return Report{Schema: 1, ID: NewID(), CyrlmVersion: "test", CreatedAt: time.Now(), Runtime: "ollama", Model: "qwen3.6-35b-a3b", Quant: "Q4_K_M",
		Machine:   Machine{OS: "darwin", Arch: "arm64", CPU: "Apple M4 Pro", RAMGB: 48, GPU: "Apple M4 Pro", GPUCores: 20, Unified: true, DeviceSpec: "Apple M4 Pro 20c", Segment: "apple_pro"},
		DecodeTPS: NewMetric(48, 51), PromptSec: NewMetric(10, 11)}
}

func TestReportsSaveLastAndPrivacy(t *testing.T) {
	t.Setenv("CYRLM_HOME", t.TempDir())
	r := sampleReport()
	p, err := SaveReport(r)
	if err != nil {
		t.Fatal(err)
	}
	lp, lr, err := LastReport()
	if err != nil || lp != p || lr.ID != r.ID {
		t.Fatalf("LastReport = %s %v %v", lp, lr.ID, err)
	}
	if lr.DecodeTPS.ErrorPct != 6.3 {
		t.Fatalf("error pct %.1f, want 6.3", lr.DecodeTPS.ErrorPct)
	}
	// The serialized report must not contain identifying data.
	b, _ := json.Marshal(lr)
	home, _ := os.UserHomeDir()
	host, _ := os.Hostname()
	for _, bad := range []string{home, host, "127.0.0.1", "localhost"} {
		if bad != "" && strings.Contains(string(b), bad) {
			t.Fatalf("report contains %q", bad)
		}
	}
}

func TestUpload(t *testing.T) {
	var gotAuth string
	var got Report
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/v1/measurements":
			json.NewDecoder(r.Body).Decode(&got)
			w.Write([]byte(`{"id":"m_123"}`))
		case "/v1/me":
			w.Write([]byte(`{"account":"dev@example.com","credits_usd":4.5}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := Config{APIURL: srv.URL, Key: "k1"}
	id, err := c.Upload(context.Background(), sampleReport())
	if err != nil || id != "m_123" || gotAuth != "Bearer k1" || got.Model != "qwen3.6-35b-a3b" {
		t.Fatalf("upload: id=%s err=%v auth=%s model=%s", id, err, gotAuth, got.Model)
	}
	me, err := c.CheckKey(context.Background())
	if err != nil || me.CreditsUSD != 4.5 {
		t.Fatalf("me: %+v %v", me, err)
	}
	if _, err := (Config{Key: "k"}).Upload(context.Background(), sampleReport()); err == nil {
		t.Fatal("upload without a server URL must fail clearly")
	}
}
