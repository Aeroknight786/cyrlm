package store

import (
	"errors"
	"os"
	"time"

	"github.com/aeroknight786/trialm/internal/trial"
)

// TrialEntry keeps the card after a child trial returns the terminal.
type TrialEntry struct {
	ID      string       `json:"id"`
	At      time.Time    `json:"at"`
	Model   string       `json:"model"`
	Quant   string       `json:"quant"`
	Runtime string       `json:"runtime"`
	Band    int          `json:"band"`
	Rating  int          `json:"rating"`
	Report  trial.Report `json:"report"`
}

func (s Store) History() ([]TrialEntry, error) {
	var rows []TrialEntry
	err := s.Read("history.json", &rows)
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	return rows, err
}
func (s Store) SaveTrial(row TrialEntry) error {
	rows, err := s.History()
	if err != nil {
		return err
	}
	for i := range rows {
		if rows[i].ID == row.ID {
			rows[i] = row
			return s.Write("history.json", rows)
		}
	}
	rows = append(rows, row)
	if len(rows) > 100 {
		rows = rows[len(rows)-100:]
	}
	return s.Write("history.json", rows)
}
func TowardSpeed(p string) string {
	switch p {
	case "long":
		return "quality"
	case "quality":
		return "balanced"
	default:
		return "speed"
	}
}

// Recipe is the stable numbered-listing wire format shared by CLI and TUI.
type Recipe struct {
	Model   string `json:"model"`
	Quant   string `json:"quant"`
	Runtime string `json:"runtime"`
	Harness string `json:"harness"`
}
