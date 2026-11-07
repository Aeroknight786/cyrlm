// Command trialm lets you feel a local model before downloading it.
package main

import (
	"context"
	"os"

	"github.com/aeroknight786/trialm/internal/cli"
)

var version = "0.3.0-dev"

func main() { os.Exit(cli.Main(context.Background(), os.Args, version)) }
