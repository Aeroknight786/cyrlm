package server

import (
	"context"
	"io"
	"time"

	"github.com/aeroknight786/trialm/internal/api"
	"github.com/aeroknight786/trialm/internal/upstream"
)

// Local binds the protocol bridge before returning. The caller owns Serve and Shutdown.
func Local(endpoint, model, runtime string, contextTokens, port int, log io.Writer) (*Server, int, error) {
	s := New(Config{Bridge: true, Upstream: &upstream.OpenAICompat{BaseURL: endpoint + "/v1", APIKey: "local", Model: model, IdleTimeout: 20 * time.Minute}, RuntimeName: runtime, Log: log,
		Model: api.ModelInfo{Name: model, ContextLength: contextTokens, ModifiedAt: time.Now(), Capabilities: []string{"completion", "tools"}}})
	bound, err := s.Listen("127.0.0.1", port, true)
	return s, bound, err
}

// Close bounds shutdown even when an editor keeps an unfinished request open.
func (s *Server) Close() error {
	if s.httpSrv == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	defer s.httpSrv.Close()
	return s.Shutdown(ctx)
}
