package tui

import (
	_ "embed"
	"encoding/json"
	"strings"

	"github.com/aeroknight786/trialm/internal/figure"
)

//go:embed claims.json
var claimsData []byte

type Claim struct {
	ID         string          `json:"id"`
	SourceName string          `json:"source_name"`
	SourceURL  string          `json:"source_url"`
	Hardware   string          `json:"hardware"`
	Recipe     string          `json:"recipe"`
	Date       string          `json:"date"`
	DateBasis  string          `json:"date_basis"`
	Grade      string          `json:"grade"`
	Figures    []figure.Figure `json:"figures"`
	GPU        string          `json:"gpu"`
	OS         string          `json:"os"`
}

func Claims() []Claim { var cs []Claim; _ = json.Unmarshal(claimsData, &cs); return cs }
func ClaimsFor(gpu, osName string) []Claim {
	var out []Claim
	for _, c := range Claims() {
		if c.OS == osName && strings.Contains(strings.ToLower(gpu), strings.ToLower(c.GPU)) {
			out = append(out, c)
		}
	}
	return out
}
