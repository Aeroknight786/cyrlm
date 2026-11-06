// Package server is the local HTTP server that impersonates a local LLM
// runtime. It speaks Ollama's native API, OpenAI Chat Completions, OpenAI
// Responses and Anthropic Messages on one port, forwards each request to the
// upstream model, and releases the response at the simulated device's pace.
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/aeroknight786/cyrlm/internal/api"
	"github.com/aeroknight786/cyrlm/internal/api/anthropic"
	"github.com/aeroknight786/cyrlm/internal/api/ollama"
	"github.com/aeroknight786/cyrlm/internal/api/openai"
	"github.com/aeroknight786/cyrlm/internal/api/responses"
	"github.com/aeroknight786/cyrlm/internal/llm"
	"github.com/aeroknight786/cyrlm/internal/sim"
	"github.com/aeroknight786/cyrlm/internal/tok"
	"github.com/aeroknight786/cyrlm/internal/upstream"
)

// OllamaVersion is the version reported on /api/version. Clients such as
// Codex --oss check for a recent Ollama.
const OllamaVersion = "0.13.5"

// Config configures the server.
type Config struct {
	Device   *sim.Device
	Upstream upstream.Client
	// Model describes the simulated model for discovery endpoints.
	Model api.ModelInfo
	// DeviceName and RuntimeName are used in logs and the summary.
	DeviceName  string
	RuntimeName string
	// Log receives one human-readable line (or block) per request.
	Log io.Writer
	// AllowOrigins lists extra browser origins allowed to call the server.
	AllowOrigins []string
	// MaxBodyBytes caps request bodies (default 32 MiB).
	MaxBodyBytes int64
}

// Server is the simulated runtime.
type Server struct {
	cfg       Config
	mux       *http.ServeMux
	chat      api.Frontend
	anth      *anthropic.Frontend
	resp      api.Frontend
	ollChat   api.Frontend
	ollGen    api.Frontend
	logMu     sync.Mutex
	listener  net.Listener
	httpSrv   *http.Server
	startedAt time.Time
}

// New creates a server.
func New(cfg Config) *Server {
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = 32 << 20
	}
	if cfg.Log == nil {
		cfg.Log = io.Discard
	}
	s := &Server{
		cfg:       cfg,
		mux:       http.NewServeMux(),
		chat:      openai.New(),
		anth:      anthropic.New(),
		resp:      responses.New(),
		ollChat:   ollama.NewChat(),
		ollGen:    ollama.NewGenerate(),
		startedAt: time.Now(),
	}
	s.routes()
	return s
}

func (s *Server) routes() {
	m := s.mux
	m.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, "Ollama is running (simulated by cyrlm)")
	})
	m.HandleFunc("HEAD /{$}", func(w http.ResponseWriter, r *http.Request) {})
	m.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		api.WriteJSON(w, 200, map[string]string{"status": "ok"})
	})
	// Generation.
	m.HandleFunc("POST /v1/chat/completions", s.gen(s.chat))
	m.HandleFunc("POST /chat/completions", s.gen(s.chat))
	m.HandleFunc("POST /v1/messages", s.gen(s.anth))
	m.HandleFunc("POST /v1/responses", s.gen(s.resp))
	m.HandleFunc("POST /responses", s.gen(s.resp))
	m.HandleFunc("POST /api/chat", s.gen(s.ollChat))
	m.HandleFunc("POST /api/generate", s.gen(s.ollGen))
	m.HandleFunc("POST /v1/messages/count_tokens", s.countTokens)
	// Discovery.
	m.HandleFunc("GET /v1/models", func(w http.ResponseWriter, r *http.Request) {
		openai.WriteModels(w, []api.ModelInfo{s.cfg.Model})
	})
	m.HandleFunc("GET /models", func(w http.ResponseWriter, r *http.Request) {
		openai.WriteModels(w, []api.ModelInfo{s.cfg.Model})
	})
	m.HandleFunc("GET /v1/models/{id...}", func(w http.ResponseWriter, r *http.Request) {
		api.WriteJSON(w, 200, map[string]any{"id": r.PathValue("id"), "object": "model", "created": s.startedAt.Unix(), "owned_by": "cyrlm"})
	})
	m.HandleFunc("GET /api/version", func(w http.ResponseWriter, r *http.Request) { ollama.WriteVersion(w, OllamaVersion) })
	m.HandleFunc("GET /api/tags", func(w http.ResponseWriter, r *http.Request) { ollama.WriteTags(w, []api.ModelInfo{s.cfg.Model}) })
	m.HandleFunc("POST /api/show", s.show)
	m.HandleFunc("GET /api/ps", func(w http.ResponseWriter, r *http.Request) {
		loaded, exp := s.cfg.Device.Loaded()
		var models []api.ModelInfo
		if loaded {
			models = []api.ModelInfo{s.cfg.Model}
			if exp.IsZero() {
				exp = time.Now().Add(24 * 365 * time.Hour)
			}
		}
		ollama.WritePS(w, models, exp)
	})
	m.HandleFunc("POST /api/pull", func(w http.ResponseWriter, r *http.Request) {
		api.WriteJSON(w, 200, map[string]string{"status": "success"})
	})
	// Introspection.
	m.HandleFunc("GET /cyrlm/stats", func(w http.ResponseWriter, r *http.Request) {
		st := s.cfg.Device.Stats()
		api.WriteJSON(w, 200, map[string]any{
			"device": s.cfg.DeviceName, "runtime": s.cfg.RuntimeName, "model": s.cfg.Model.Name,
			"upstream": s.cfg.Upstream.Describe(), "summary": st.Summarize(), "requests": st.Snapshot(),
		})
	})
}

// ServeHTTP applies localhost protections, then routes.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !localHost(r.Host) {
		http.Error(w, "cyrlm only accepts requests addressed to localhost", http.StatusForbidden)
		return
	}
	if o := r.Header.Get("Origin"); o != "" && !s.originAllowed(o) {
		http.Error(w, "browser origin not allowed (use --allow-origin)", http.StatusForbidden)
		return
	}
	s.mux.ServeHTTP(w, r)
}

func localHost(hostport string) bool {
	h := hostport
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		h = host
	}
	h = strings.Trim(h, "[]")
	if h == "localhost" || h == "host.docker.internal" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func (s *Server) originAllowed(o string) bool {
	for _, a := range s.cfg.AllowOrigins {
		if a == "*" || strings.EqualFold(a, o) {
			return true
		}
	}
	if o == "null" {
		return false
	}
	if i := strings.Index(o, "://"); i >= 0 {
		return localHost(o[i+3:])
	}
	return false
}

func (s *Server) readBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	return io.ReadAll(http.MaxBytesReader(w, r.Body, s.cfg.MaxBodyBytes))
}

func (s *Server) show(w http.ResponseWriter, r *http.Request) {
	body, err := s.readBody(w, r)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if _, err := ollama.DecodeShowRequest(body); err != nil {
		api.WriteJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	ollama.WriteShow(w, s.cfg.Model)
}

func (s *Server) countTokens(w http.ResponseWriter, r *http.Request) {
	body, err := s.readBody(w, r)
	if err != nil {
		s.anth.WriteError(w, 400, "invalid_request_error", err.Error())
		return
	}
	req, err := s.anth.DecodeRequest(body)
	if err != nil {
		s.anth.WriteError(w, 400, "invalid_request_error", err.Error())
		return
	}
	seq := tok.Render(req, s.cfg.Device.Config().Render)
	s.anth.WriteCountTokens(w, int(float64(seq.Len())*s.cfg.Device.PromptRatio()))
}

func newID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// gen handles a generation request for one protocol.
func (s *Server) gen(fe api.Frontend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dev := s.cfg.Device
		clock := dev.Config().Clock
		arrival := clock.Now()
		body, err := s.readBody(w, r)
		if err != nil {
			fe.WriteError(w, http.StatusRequestEntityTooLarge, "invalid_request_error", err.Error())
			return
		}
		req, err := fe.DecodeRequest(body)
		if err != nil {
			fe.WriteError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
		meta := api.StreamMeta{ID: newID(), Model: req.Model, Created: time.Now(), IncludeUsage: req.IncludeUsage}
		if meta.Model == "" {
			meta.Model = s.cfg.Model.Name
		}
		rec := sim.Record{At: arrival, Protocol: fe.Name(), ClientModel: req.Model}

		// Ollama load/unload probes carry no messages.
		if len(req.Messages) == 0 {
			fe.WriteResponse(w, meta, &llm.Response{FinishReason: "load"}, llm.Timings{})
			return
		}

		prep, err := dev.Prepare(req)
		var cerr *sim.ContextError
		if errors.As(err, &cerr) {
			rec.ContextError = cerr.Message
			rec.PromptTokens = cerr.PromptTokens
			rec.Finish = "context_error"
			n := dev.Stats().Add(rec)
			s.logf("#%d %s  ✗ rejected: %s\n", n, fe.Name(), cerr.Message)
			fe.WriteError(w, http.StatusBadRequest, "context_length_exceeded", cerr.Message)
			return
		}
		if err != nil {
			fe.WriteError(w, http.StatusInternalServerError, "api_error", err.Error())
			return
		}

		upCtx, cancel := context.WithCancel(r.Context())
		defer cancel()
		events, err := s.cfg.Upstream.Stream(upCtx, prep.Req)
		if err != nil {
			rec.Error = err.Error()
			n := dev.Stats().Add(rec)
			s.logf("#%d %s  ✗ upstream error: %v\n", n, fe.Name(), err)
			status, kind := upstreamErrorStatus(err)
			fe.WriteError(w, status, kind, "cyrlm upstream: "+err.Error())
			return
		}
		timed := sim.Buffer(upCtx, events, clock)

		release, queued, err := dev.Acquire(r.Context())
		if err != nil {
			return
		}
		defer release()
		plan := dev.Plan(prep)
		meta.PromptTokens = plan.PromptTokens

		var res sim.PaceResult
		if req.Stream {
			st := fe.NewStream(w, meta)
			if err := st.Start(); err != nil {
				return
			}
			res = dev.Pace(r.Context(), plan, prep, timed, st.Send)
			switch {
			case res.Cancelled:
			case res.Err != nil:
				st.Fail("api_error", "cyrlm upstream: "+res.Err.Error())
			default:
				st.Finish(res.Resp.FinishReason, s.usage(plan, res), sim.Timings(plan, res))
			}
		} else {
			res = dev.Pace(r.Context(), plan, prep, timed, func(llm.Event) error { return nil })
			switch {
			case res.Cancelled:
			case res.Err != nil:
				fe.WriteError(w, http.StatusBadGateway, "api_error", "cyrlm upstream: "+res.Err.Error())
			default:
				// Hold the response until the simulated device would finish.
				_ = clock.SleepUntil(r.Context(), res.LastTokenAt)
				resp := res.Resp
				resp.Usage = s.usage(plan, res)
				fe.WriteResponse(w, meta, &resp, sim.Timings(plan, res))
			}
		}
		cancel()
		end := clock.Now()
		dev.Commit(plan, &res.Resp, res.GeneratedPieces, end)

		// Record.
		rec.PromptTokens, rec.CachedTokens, rec.PrefillTokens = plan.PromptTokens, plan.CachedTokens, plan.PrefillTokens
		rec.Cold, rec.LoadReason = plan.Cold, plan.LoadReason
		rec.QueueSec = queued.Seconds()
		rec.LoadSec = plan.LoadDur.Seconds()
		rec.PrefillSec = (plan.PrefillDur + plan.RestoreDur).Seconds()
		if !res.FirstTokenAt.IsZero() {
			rec.TTFTSec = res.FirstTokenAt.Sub(plan.Start).Seconds()
		}
		if !res.LastTokenAt.IsZero() {
			rec.TotalSec = res.LastTokenAt.Sub(plan.Start).Seconds()
		}
		u := s.usage(plan, res)
		rec.OutputTokens = u.CompletionTokens
		if gen := res.LastTokenAt.Sub(res.FirstTokenAt).Seconds(); gen > 0 && rec.OutputTokens > 1 {
			rec.DecodeTPS = float64(rec.OutputTokens-1) / gen
		}
		if !res.UpstreamDoneAt.IsZero() {
			rec.CloudSec = res.UpstreamDoneAt.Sub(arrival).Seconds()
		}
		rec.CloudLagSec = res.CloudLag.Seconds()
		rec.Finish = res.Resp.FinishReason
		rec.Cancelled = res.Cancelled
		if res.Err != nil {
			rec.Error = res.Err.Error()
		}
		if prep.Truncated {
			rec.Truncated = prep.TruncNote
		}
		if plan.Miss != nil {
			rec.CacheMiss = plan.Miss.Reason
		}
		n := dev.Stats().Add(rec)
		s.logRecord(n, rec, plan, prep, res)
	}
}

// upstreamErrorStatus maps an upstream failure to the status and error
// type the harness should see. Client errors (bad request, auth, billing,
// rate limit) keep their 4xx status so harnesses don't retry them; only
// server and transport failures become 502.
func upstreamErrorStatus(err error) (int, string) {
	var he *upstream.HTTPError
	if !errors.As(err, &he) {
		return http.StatusBadGateway, "api_error"
	}
	switch {
	case he.Status == 401 || he.Status == 403:
		return he.Status, "authentication_error"
	case he.Status == 402:
		return he.Status, "billing_error"
	case he.Status == 429:
		return he.Status, "rate_limit_error"
	case he.Status >= 400 && he.Status < 500:
		return he.Status, "invalid_request_error"
	}
	return http.StatusBadGateway, "api_error"
}

// usage reports token counts as the local runtime would: the upstream's real
// prompt/completion counts, with the simulated local cache hit scaled to them.
func (s *Server) usage(pl *sim.Plan, res sim.PaceResult) llm.Usage {
	u := res.Resp.Usage
	if u.PromptTokens == 0 {
		u.PromptTokens = pl.PromptTokens
	}
	if pl.PromptTokens > 0 {
		u.CachedPromptTokens = int(float64(pl.CachedTokens) / float64(pl.PromptTokens) * float64(u.PromptTokens))
	}
	if u.CompletionTokens == 0 {
		u.CompletionTokens = int(float64(res.GeneratedPieces) * s.cfg.Device.OutputRatio())
	}
	return u
}

func (s *Server) logf(format string, a ...any) {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	fmt.Fprintf(s.cfg.Log, format, a...)
}

func (s *Server) logRecord(n int, rec sim.Record, pl *sim.Plan, prep *sim.Prepared, res sim.PaceResult) {
	var b strings.Builder
	fmt.Fprintf(&b, "#%-3d %-11s prompt %s", n, rec.Protocol, sim.Kilo(rec.PromptTokens))
	if rec.CachedTokens > 0 {
		fmt.Fprintf(&b, " (cached %s, new %s)", sim.Kilo(rec.CachedTokens), sim.Kilo(rec.PrefillTokens))
	}
	if rec.TTFTSec > 0 {
		fmt.Fprintf(&b, " · first token %s", sim.FormatDur(rec.TTFTSec))
	}
	if rec.OutputTokens > 0 {
		fmt.Fprintf(&b, " · %s tok", sim.Kilo(rec.OutputTokens))
		if rec.DecodeTPS > 0 {
			fmt.Fprintf(&b, " @ %.0f tok/s", rec.DecodeTPS)
		}
	}
	if rec.TotalSec > 0 {
		fmt.Fprintf(&b, " · %s", sim.FormatDur(rec.TotalSec+rec.QueueSec))
	}
	if rec.CloudSec > 0 {
		fmt.Fprintf(&b, " (cloud %s)", sim.FormatDur(rec.CloudSec))
	}
	b.WriteString("\n")
	if rec.QueueSec > 0.5 {
		fmt.Fprintf(&b, "     ↳ waited %s for the device (another request was running)\n", sim.FormatDur(rec.QueueSec))
	}
	if pl.Cold {
		fmt.Fprintf(&b, "     ↳ load %s: %s\n", sim.FormatDur(rec.LoadSec), pl.LoadReason)
	}
	if pl.Miss != nil {
		fmt.Fprintf(&b, "     ↳ cache miss at token %d of a %d-token cached prompt: %s\n", pl.Miss.At, pl.Miss.Previous, pl.Miss.Reason)
		fmt.Fprintf(&b, "       before: …%s\n       was:    %s…\n       now:    %s…\n", oneLine(pl.Miss.Before), oneLine(pl.Miss.Was), oneLine(pl.Miss.Now))
	}
	if prep.Truncated {
		fmt.Fprintf(&b, "     ↳ ⚠ %s\n", prep.TruncNote)
	}
	if res.StoppedAtContext {
		fmt.Fprintf(&b, "     ↳ ⚠ generation stopped: the context window (%d tokens) is full\n", prep.Ctx)
	}
	if rec.CloudLagSec > 0.5 {
		fmt.Fprintf(&b, "     ↳ the cloud was %s slower than your simulated device here (shown at cloud speed)\n", sim.FormatDur(rec.CloudLagSec))
	}
	if rec.Error != "" {
		fmt.Fprintf(&b, "     ↳ ✗ %s\n", rec.Error)
	}
	if rec.Cancelled {
		b.WriteString("     ↳ cancelled by the client\n")
	}
	s.logf("%s", b.String())
}

func oneLine(s string) string {
	s = strings.NewReplacer("\n", "⏎", "\r", "", "\t", " ").Replace(s)
	if len(s) > 90 {
		s = s[:90]
	}
	return s
}

// Listen binds addr. When the port is busy and fallback is true, the next
// ports up to +10 are tried.
func (s *Server) Listen(host string, port int, fallback bool) (int, error) {
	var lastErr error
	for p := port; p <= port+10; p++ {
		l, err := net.Listen("tcp", net.JoinHostPort(host, fmt.Sprint(p)))
		if err == nil {
			s.listener = l
			return p, nil
		}
		lastErr = err
		if !fallback {
			break
		}
	}
	return 0, lastErr
}

// Serve serves on the bound listener until Shutdown.
func (s *Server) Serve() error {
	s.httpSrv = &http.Server{Handler: s, ReadHeaderTimeout: 10 * time.Second}
	err := s.httpSrv.Serve(s.listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Shutdown stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpSrv == nil {
		return nil
	}
	return s.httpSrv.Shutdown(ctx)
}

// Summary returns the session summary text.
func (s *Server) Summary() string {
	return s.cfg.Device.Stats().Summarize().Report(s.cfg.DeviceName, s.cfg.RuntimeName, s.cfg.Model.Name)
}

// StatsJSON returns the stats as JSON.
func (s *Server) StatsJSON() ([]byte, error) {
	return json.MarshalIndent(s.cfg.Device.Stats().Snapshot(), "", "  ")
}
