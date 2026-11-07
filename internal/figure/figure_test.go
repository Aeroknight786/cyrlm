package figure

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPrecisionAndProvenance(t *testing.T) {
	for _, tc := range []struct {
		v    float64
		pct  int
		want string
	}{{12.345, 10, "12.3"}, {12.345, 25, "12"}, {12.345, 26, "~12"}, {.012345, 10, "0.0123"}, {12345, 60, "~12000"}, {0, 60, "0"}} {
		f := Band(tc.v, tc.pct, "tok/s", Simulated, "test")
		if got := f.Number(); got != tc.want {
			t.Errorf("%+v: got %s want %s", f, got, tc.want)
		}
	}
	f := Band(40, 60, "tok/s", Calibrated, "test")
	f.InSample = true
	if !strings.HasSuffix(f.String(), "fit tok/s") {
		t.Fatal(f)
	}
	b, e := json.Marshal(f)
	if e != nil || !strings.Contains(string(b), `"lo":16`) {
		t.Fatal(string(b), e)
	}
}
