package cli

import (
	"context"
	"os"
)

func Main(ctx context.Context, args []string, v string) int {
	_ = ctx
	os.Args = args
	version = v
	runLegacy()
	return 0
}
