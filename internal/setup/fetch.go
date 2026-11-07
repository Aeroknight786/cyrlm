package setup

import (
	"context"
	"errors"
	"io"
	"net"
	"time"

	"github.com/aeroknight786/trialm/internal/setup/download"
)

func fetchRetry(ctx context.Context, client Fetcher, request download.Request) (download.Result, error) {
	var result download.Result
	var transferred int64
	for attempt := 0; attempt < 3; attempt++ {
		next, err := client.Fetch(ctx, request)
		transferred += next.Bytes
		result = next
		result.Bytes = transferred
		var network net.Error
		retry := errors.As(err, &network) || errors.Is(err, io.ErrUnexpectedEOF)
		if err == nil || !retry || attempt == 2 {
			return result, err
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return result, ctx.Err()
		case <-timer.C:
		}
	}
	return result, nil
}
