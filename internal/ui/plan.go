package ui

import (
	"context"
	"fmt"
	"github.com/aeroknight786/trialm/internal/figure"
	"io"
	"os"
	"strings"

	"github.com/aeroknight786/trialm/internal/setup"
	"github.com/aeroknight786/trialm/internal/terminal"
)

func PrintPlan(output io.Writer, p setup.Plan) {
	fmt.Fprintf(output, "\nSetup plan %s · %s · %s\n", p.ModelName, p.Quant, p.RuntimeName)
	on := p.OnDisk()
	download := figure.Bytes(p.DownloadBytes-on, "setup:plan").String() + " download"
	if p.DownloadUnknown {
		download += " plus installer downloads of unknown size"
	}
	if on > 0 {
		download += " · " + gb(float64(on)) + " already on disk"
	}
	fmt.Fprintf(output, "%d approval · %d OS password prompts · %s\n", p.Prompts.Approvals, p.Prompts.Passwords, download)
	if p.Prompts.AfterRestart != "" {
		fmt.Fprintln(output, p.Prompts.AfterRestart)
	}
	fmt.Fprintf(output, "%d tokens · %s · %s\n", p.ContextTokens, p.KVCacheType, p.Endpoint)
	for i, s := range p.Steps {
		fmt.Fprintf(output, "%d/%d %s · %s\n", i+1, len(p.Steps), s.Kind, s.Title)
		if len(s.Argv) > 0 {
			fmt.Fprintf(output, "  argv %q\n", s.Argv)
		}
		if s.Request != nil {
			fmt.Fprintf(output, "  %s\n  sha256 %s\n", s.Request.URL, s.Request.SHA256)
		}
		if s.Path != "" {
			fmt.Fprintln(output, "  "+s.Path)
		}
		if s.Kind == setup.RunInstaller {
			fmt.Fprintln(output, s.Content)
		}
	}
	fmt.Fprintln(output, "Kept as they are · "+strings.Join(p.Untouched, ", "))
	for _, warning := range p.Warnings {
		fmt.Fprintln(output, warning)
	}
	fmt.Fprintln(output, "plan_id", p.ID)
}

func ConfirmSetup(output io.Writer, input *os.File) (bool, error) {
	fmt.Fprint(output, "Apply this plan? Y/n ")
	key, err := terminal.Key(input)
	fmt.Fprintln(output)
	if err != nil {
		return false, err
	}
	if key == 3 {
		return false, context.Canceled
	}
	return key == 13 || key == 10 || key == 'y' || key == 'Y', nil
}
