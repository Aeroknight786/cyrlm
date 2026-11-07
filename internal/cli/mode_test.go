package cli

import "testing"

func TestModeSelection(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		in, out bool
		env     string
		want    Mode
	}{{nil, true, true, "", Interactive}, {nil, false, true, "", Plain}, {nil, true, false, "", Plain}, {nil, true, true, "CI", Plain}, {nil, true, true, "TRIALM_NO_INPUT", Plain}, {[]string{"--json"}, true, true, "", JSON}, {[]string{"--plain"}, true, true, "", Plain}, {[]string{"--plain", "--json"}, false, false, "CI", JSON}} {
		if got := SelectMode(tc.args, tc.in, tc.out, func(s string) string {
			if s == tc.env {
				return "1"
			}
			return ""
		}); got != tc.want {
			t.Fatal(tc, got)
		}
	}
}
