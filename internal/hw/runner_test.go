package hw

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLoopbackGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"version":"0.12.6"}`))
	}))
	defer srv.Close()
	st, body, err := osRunner{}.HTTPGet(context.Background(), srv.URL+"/api/version")
	if err != nil || st != 200 || jsonStringField(body, "version") != "0.12.6" {
		t.Fatalf("loopback: %d %s %v", st, body, err)
	}
	for _, u := range []string{"http://example.com/", "http://10.0.0.1:11434/api/version", "::bad"} {
		if _, _, err := (osRunner{}).HTTPGet(context.Background(), u); err == nil {
			t.Errorf("%s: want error", u)
		} else if strings.HasPrefix(u, "http") && !strings.Contains(err.Error(), "non-loopback") {
			t.Errorf("%s: %v", u, err)
		}
	}
}

func TestOSRunnerRun(t *testing.T) {
	if _, err := (osRunner{}).Run(context.Background(), "cyrlm-no-such-tool-xyz"); err == nil {
		t.Fatal("missing tool should fail")
	}
}

func TestVendorFromName(t *testing.T) {
	for name, want := range map[string]string{
		"NVIDIA GeForce RTX 4090":   VendorNVIDIA,
		"Radeon RX 7900 XTX":        VendorAMD,
		"Intel(R) Arc(TM) A770":     VendorIntel,
		"Arc B580":                  VendorIntel,
		"Apple M4 Max":              VendorApple,
		"Parsec Virtual Display":    VendorOther,
		"Qualcomm(R) Adreno(TM) X1": VendorOther,
	} {
		if got := vendorFromName(name); got != want {
			t.Errorf("vendorFromName(%q) = %q, want %q", name, got, want)
		}
	}
}
