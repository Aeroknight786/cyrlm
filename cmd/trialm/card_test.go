package main

import "testing"

func TestBandNumber(t *testing.T) {
	for _, tc := range []struct {
		value float64
		band  int
		want  string
	}{{12.345, 10, "12.3"}, {12.345, 25, "12"}, {12.345, 26, "~12"}} {
		if got := bandNumber(tc.value, tc.band); got != tc.want {
			t.Errorf("got %s want %s", got, tc.want)
		}
	}
}
