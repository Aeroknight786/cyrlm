package server

import (
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
