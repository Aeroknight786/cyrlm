package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aeroknight786/trialm/internal/store"
)

func archive(t *testing.T, goos string) []byte {
	t.Helper()
	var b bytes.Buffer
	const payload = "new binary bytes"
	if goos == "windows" {
		z := zip.NewWriter(&b)
		f, err := z.Create("trialm_windows_amd64/trialm.exe")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.Write([]byte(payload))
		if err = z.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		g := gzip.NewWriter(&b)
		tr := tar.NewWriter(g)
		if err := tr.WriteHeader(&tar.Header{Name: "trialm_linux_amd64/trialm", Mode: 0755, Size: int64(len(payload))}); err != nil {
			t.Fatal(err)
		}
		_, _ = tr.Write([]byte(payload))
		_ = tr.Close()
		_ = g.Close()
	}
	return b.Bytes()
}
func TestVerifiedUpdateUsesTemporaryTarget(t *testing.T) {
	for _, goos := range []string{"linux", "windows"} {
		for _, valid := range []bool{true, false} {
			t.Run(fmt.Sprint(goos, "/", valid), func(t *testing.T) {
				b := archive(t, goos)
				name := "trialm_" + goos + "_amd64.tar.gz"
				if goos == "windows" {
					name = "trialm_windows_amd64.zip"
				}
				sum := fmt.Sprintf("%x", sha256.Sum256(b))
				if !valid {
					sum = fmt.Sprintf("%064d", 0)
				}
				var fake *httptest.Server
				fake = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/latest":
						_ = json.NewEncoder(w).Encode(Release{Version: "v0.3.0", Assets: []Asset{{name, fake.URL + "/binary"}, {"checksums.txt", fake.URL + "/sums"}}})
					case "/binary":
						_, _ = w.Write(b)
					case "/sums":
						fmt.Fprintf(w, "%s  %s\n", sum, name)
					default:
						w.WriteHeader(404)
					}
				}))
				defer fake.Close()
				c := Client{LatestURL: fake.URL + "/latest", HTTP: fake.Client(), OS: goos, Arch: "amd64"}
				r, err := c.Check(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				target := filepath.Join(t.TempDir(), "trialm")
				if err = os.WriteFile(target, []byte("old"), 0755); err != nil {
					t.Fatal(err)
				}
				err = c.Install(context.Background(), r, target)
				got, _ := os.ReadFile(target)
				if valid {
					if err != nil || string(got) != "new binary bytes" {
						t.Fatal(string(got), err)
					}
				} else {
					if err == nil || string(got) != "old" {
						t.Fatal("unverified binary replaced target", string(got), err)
					}
				}
			})
		}
	}
}
func TestNoticeDailyAndAsync(t *testing.T) {
	s := store.Store{Dir: t.TempDir()}
	now := time.Now()
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if Claim(s, now) {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatal("daily claim raced", wins.Load())
	}
	release := make(chan struct{})
	entered := make(chan struct{})
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		_ = json.NewEncoder(w).Encode(Release{Version: "v0.4.0"})
	}))
	defer fake.Close()
	done := make(chan error, 1)
	c := Client{LatestURL: fake.URL, HTTP: fake.Client()}
	go func() { done <- c.Refresh(context.Background(), s) }()
	<-entered
	if n := Cached(s); n.Version != "" || n.CheckedAt.IsZero() {
		t.Fatal(n)
	} // cache reads never wait for network
	if Claim(s, now.Add(time.Hour)) {
		t.Fatal("checked twice in a day")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if Cached(s).Version != "v0.4.0" {
		t.Fatal(Cached(s))
	}
	if !Claim(s, now.Add(25*time.Hour)) {
		t.Fatal("daily claim never expired")
	}
	for _, tc := range []struct {
		a, b string
		want bool
	}{{"v0.3.0", "v0.2.1", true}, {"v0.2.1", "v0.3.0", false}, {"v0.3.0", "0.3.0", false}, {"v0.3.0", "0.3.0-dev", true}, {"garbage", "dev", false}} {
		if Newer(tc.a, tc.b) != tc.want {
			t.Fatal(tc)
		}
	}
}
func TestArchiveMissingBinaryAndBadChecksumPreserveTarget(t *testing.T) {
	if _, err := extract([]byte("invalid archive"), "trialm", ".tar.gz"); err == nil {
		t.Fatal("accepted invalid archive")
	}
	if _, err := extract(archive(t, "linux"), "../trialm", ".tar.gz"); err == nil {
		t.Fatal("unexpected archive entry selected")
	}
	c := Client{}
	target := filepath.Join(t.TempDir(), "trialm")
	_ = os.WriteFile(target, []byte("original"), 0700)
	if err := c.Install(context.Background(), Release{}, target); err == nil {
		t.Fatal("accepted missing assets")
	}
	b, _ := os.ReadFile(target)
	if string(b) != "original" {
		t.Fatal(string(b))
	}
}
