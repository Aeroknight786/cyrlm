package api

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// SSEWriter writes Server-Sent Events and flushes after every event.
type SSEWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
	started bool
}

// Started reports whether headers were written.
func (s *SSEWriter) Started() bool { return s.started }

// NewSSEWriter wraps w.
func NewSSEWriter(w http.ResponseWriter) *SSEWriter {
	f, _ := w.(http.Flusher)
	return &SSEWriter{w: w, flusher: f}
}

// Start writes SSE headers once (idempotent).
func (s *SSEWriter) Start() {
	if s.started {
		return
	}
	s.started = true
	h := s.w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	s.w.WriteHeader(http.StatusOK)
	s.Flush()
}

// Event writes "event: <name>\ndata: <json>\n\n". An empty name omits the
// event line (OpenAI style).
func (s *SSEWriter) Event(name string, payload any) error {
	s.Start()
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if name != "" {
		if _, err := fmt.Fprintf(s.w, "event: %s\n", name); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(s.w, "data: %s\n\n", b); err != nil {
		return err
	}
	s.Flush()
	return nil
}

// Raw writes "data: <text>\n\n" verbatim (e.g. "[DONE]").
func (s *SSEWriter) Raw(text string) error {
	s.Start()
	if _, err := fmt.Fprintf(s.w, "data: %s\n\n", text); err != nil {
		return err
	}
	s.Flush()
	return nil
}

// Flush flushes buffered output to the client.
func (s *SSEWriter) Flush() {
	if s.flusher != nil {
		s.flusher.Flush()
	}
}

// WriteJSON writes a JSON body with status.
func WriteJSON(w http.ResponseWriter, status int, payload any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(payload)
}
