package server

import (
	"context"
	"fmt"
	"io"
	"time"
)

// Meter observes an already serving local route until the caller is interrupted.
func Meter(ctx context.Context, output io.Writer, s *Server) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		fmt.Fprintf(output, "Requests %d · local\nCache unknown\nWait engine-managed\nSpend local\n", s.bridgeN.Load())
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:

		}
	}
}
