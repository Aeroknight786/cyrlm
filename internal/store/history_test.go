package store

import "testing"

func TestHistoryRatingAndRetention(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	row := TrialEntry{ID: "a", Model: "qwen"}
	if e := s.SaveTrial(row); e != nil {
		t.Fatal(e)
	}
	row.Rating = 3
	if e := s.SaveTrial(row); e != nil {
		t.Fatal(e)
	}
	rs, e := s.History()
	if e != nil || len(rs) != 1 || rs[0].Rating != 3 {
		t.Fatal(rs, e)
	}
	for _, tc := range []struct{ in, out string }{{"long", "quality"}, {"quality", "balanced"}, {"balanced", "speed"}, {"speed", "speed"}} {
		if TowardSpeed(tc.in) != tc.out {
			t.Fatal(tc)
		}
	}
}

func TestCheckpointCannotReplaceFinalCard(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	row := TrialEntry{ID: "a", Incomplete: true}
	if e := s.SaveTrial(row); e != nil {
		t.Fatal(e)
	}
	row.Incomplete = false
	row.Rating = 3
	if e := s.SaveTrial(row); e != nil {
		t.Fatal(e)
	}
	row.Incomplete = true
	row.Rating = 0
	if e := s.SaveTrial(row); e != nil {
		t.Fatal(e)
	}
	rows, e := s.History()
	if e != nil || len(rows) != 1 || rows[0].Incomplete || rows[0].Rating != 3 {
		t.Fatal(rows, e)
	}
}
