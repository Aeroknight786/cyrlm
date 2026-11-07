package ui

import (
	"fmt"
	"github.com/aeroknight786/trialm/internal/harness"
	"io"
	"strings"
)

func PrintPrompt(w io.Writer, template, h string) error {
	p := template
	if h != "" {
		hh, err := harness.Get(h)
		if err != nil {
			return err
		}
		p = strings.ReplaceAll(p, "<HARNESS>", hh.ID)
	} else {
		p = strings.ReplaceAll(p, "You are running in <HARNESS> — use that as `--harness`", "Work out which coding tool you are running in and use it wherever <your-tool> appears as `--harness`")
		p = strings.ReplaceAll(p, "<HARNESS>", "<your-tool>")
	}
	fmt.Fprint(w, p)
	return nil
}
