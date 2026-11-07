package hw

import "testing"

func TestParsePower(t *testing.T) {
	settings := "System-wide power settings:\nCurrently in use:\n standby              1\n lowpowermode         1\n sleep                1\n"
	if p := parsePower(settings, "Now drawing from 'Battery Power'\n -InternalBattery-0 82%; discharging"); !p.LowPower || !p.OnBattery {
		t.Fatal(p)
	}
	if p := parsePower(" lowpowermode         0\n", "Now drawing from 'AC Power'"); p.LowPower || p.OnBattery {
		t.Fatal(p)
	}
	if p := parsePower("", ""); p != (Power{}) {
		t.Fatal(p)
	}
}
