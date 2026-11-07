package calibrationcli

import (
	"context"
	"flag"
	"github.com/aeroknight786/trialm/internal/core"
	"github.com/aeroknight786/trialm/internal/figure"
	"github.com/aeroknight786/trialm/internal/hw"
	"github.com/aeroknight786/trialm/internal/paths"
	"github.com/aeroknight786/trialm/internal/predict"
	"io"
	"os"
	"path/filepath"
)

var bandNumber = figure.BandNumber

func out() io.Writer { return os.Stdout }
func trialmHome() string {
	d, e := paths.Home()
	if e != nil {
		return filepath.Join(os.TempDir(), "trialm")
	}
	return d
}
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if e := fs.Parse(args); e != nil {
			return nil, e
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return pos, nil
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
}

func calibrationTarget(ctx context.Context) (predict.Target, *hw.System, error) {
	d, e := (core.Session{}).Detect(ctx, core.Options{})
	return d.Target, d.System, e
}
