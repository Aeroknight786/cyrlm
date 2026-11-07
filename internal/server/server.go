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
	"sync/atomic"
	"time"

	"github.com/aeroknight786/trialm/internal/api"
	"github.com/aeroknight786/trialm/internal/api/anthropic"
	"github.com/aeroknight786/trialm/internal/api/ollama"
	"github.com/aeroknight786/trialm/internal/api/openai"
	"github.com/aeroknight786/trialm/internal/api/responses"
	"github.com/aeroknight786/trialm/internal/llm"
	"github.com/aeroknight786/trialm/internal/sim"
	"github.com/aeroknight786/trialm/internal/tok"
	"github.com/aeroknight786/trialm/internal/upstream"
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
	// Bridge relays requests to a real local runtime with no simulation:
	// the harness speaks its own protocol (Responses, Anthropic, Ollama),
	// the runtime gets plain Chat Completions. Device may be nil.
	Bridge bool
}

// Server is the simulated runtime.
type Server struct {
	cfg          Config
	mux          *http.ServeMux
	chat         api.Frontend
	anth         *anthropic.Frontend
	resp         api.Frontend
	ollChat      api.Frontend
	ollGen       api.Frontend
	logMu        sync.Mutex
	listener     net.Listener
	httpSrv      *http.Server
	startedAt    time.Time
	bridgeN      atomic.Int64
	warnedNumCtx sync.Once
	warnedSystem sync.Once
}

// New creates a server.
func New(cfg Config) *Server {
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = 32 << 20
	}
	if cfg.Log == nil {
		cfg.Log = io.Discard
	}
	if cfg.Bridge && strings.EqualFold(cfg.RuntimeName, "ollama") {
		if client, ok := cfg.Upstream.(*upstream.OpenAICompat); ok {
			clone := *client
			clone.Ollama = true
			cfg.Upstream = &clone
		}
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
		fmt.Fprint(w, "Ollama is running (simulated by triaLM)")
	})
	m.HandleFunc("HEAD /api/hello", func(w http.ResponseWriter, r *http.Request) {})
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
		api.WriteJSON(w, 200, map[string]any{"id": r.PathValue("id"), "object": "model", "created": s.startedAt.Unix(), "owned_by": "trialm"})
	})
	m.HandleFunc("GET /api/version", func(w http.ResponseWriter, r *http.Request) { ollama.WriteVersion(w, OllamaVersion) })
	m.HandleFunc("GET /api/tags", func(w http.ResponseWriter, r *http.Request) { ollama.WriteTags(w, []api.ModelInfo{s.cfg.Model}) })
	m.HandleFunc("POST /api/show", s.show)
	m.HandleFunc("GET /api/ps", func(w http.ResponseWriter, r *http.Request) {
		loaded, exp := true, time.Time{}
		if s.cfg.Device != nil {
			loaded, exp = s.cfg.Device.Loaded()
		}
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
	m.HandleFunc("GET /trialm/stats", func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.Device == nil {
			api.WriteJSON(w, 200, map[string]any{"bridge": s.cfg.Upstream.Describe(), "requests": s.bridgeN.Load()})
			return
		}
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
		http.Error(w, "triaLM only accepts requests addressed to localhost", http.StatusForbidden)
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
	if s.cfg.Device == nil {
		s.anth.WriteCountTokens(w, tok.Render(req, tok.RenderOptions{}).Len())
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
		// Ollama load/unload probes carry no messages.
		if len(req.Messages) == 0 {
			fe.WriteResponse(w, meta, &llm.Response{FinishReason: "load"}, llm.Timings{})
			return
		}
		if s.cfg.Bridge {
			s.relay(w, r, fe, req, meta)
			return
		}
		dev := s.cfg.Device
		clock := dev.Config().Clock
		arrival := clock.Now()
		rec := sim.Record{At: arrival, Protocol: fe.Name(), ClientModel: req.Model}

		prep, err := dev.Prepare(req)
		var cerr *sim.ContextError
		if errors.As(err, &cerr) {
			rec.ContextError = cerr.Message
			rec.PromptTokens = cerr.PromptTokens
			rec.Finish = "context_error"
			n := dev.Stats().Add(rec)
			s.logWarnings(n, fe.Name(), req)
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
			s.logWarnings(n, fe.Name(), req)
			s.logf("#%d %s  ✗ upstream error: %v\n", n, fe.Name(), err)
			status, kind := upstreamErrorStatus(err)
			fe.WriteError(w, status, kind, "triaLM upstream: "+err.Error())
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
		meta.CachedPromptTokens = plan.CachedTokens

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
				st.Fail("api_error", "triaLM upstream: "+res.Err.Error())
			default:
				st.Finish(res.Resp.FinishReason, s.usage(plan, res), sim.Timings(plan, res))
			}
		} else {
			res = dev.Pace(r.Context(), plan, prep, timed, func(llm.Event) error { return nil })
			switch {
			case res.Cancelled:
			case res.Err != nil:
				fe.WriteError(w, http.StatusBadGateway, "api_error", "triaLM upstream: "+res.Err.Error())
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

// relay forwards one request to the local runtime and streams its answer
// back in the harness's protocol, without simulated timing.
func (s *Server) relay(w http.ResponseWriter, r *http.Request, fe api.Frontend, req *llm.Request, meta api.StreamMeta) {
	n := s.bridgeN.Add(1)
	start := time.Now()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	s.logWarnings(int(n), fe.Name(), req)
	ctxN := s.cfg.Model.ContextLength
	if client, ok := s.cfg.Upstream.(*upstream.OpenAICompat); ok && client.Ollama {
		if req.NumCtx > 0 {
			ctxN = req.NumCtx
		} else if ctxN > 0 {
			req.NumCtx = ctxN
		}
	}
	if ctxN > 0 {
		room := max(256, ctxN-tok.Render(req, tok.RenderOptions{}).Len())
		if req.MaxTokens > room {
			req.MaxTokens = room
		}
	}
	events, err := s.cfg.Upstream.Stream(ctx, req)
	if err != nil {
		s.logf("#%d %s  ✗ runtime error: %v\n", n, fe.Name(), err)
		status, kind, msg := bridgeError(err, s.cfg.Upstream.Describe())
		fe.WriteError(w, status, kind, msg)
		return
	}
	var st api.Stream
	var resp llm.Response
	var first time.Time
	for ev := range events {
		switch ev.Type {
		case llm.EventError:
			s.logf("#%d %s  ✗ runtime error: %v\n", n, fe.Name(), ev.Err)
			status, kind, msg := bridgeError(ev.Err, s.cfg.Upstream.Describe())
			if st != nil {
				st.Fail(kind, msg)
			} else {
				fe.WriteError(w, status, kind, msg)
			}
			return
		case llm.EventDone:
			resp.Apply(ev)
		default:
			if first.IsZero() {
				first = time.Now()
			}
			resp.Apply(ev)
			if req.Stream {
				if st == nil {
					st = fe.NewStream(w, meta)
					if st.Start() != nil {
						return
					}
				}
				if st.Send(ev) != nil {
					return
				}
			}
		}
	}
	if resp.FinishReason == "" {
		resp.FinishReason = llm.FinishStop
	}
	if len(resp.ToolCalls) > 0 && resp.FinishReason == llm.FinishStop {
		resp.FinishReason = llm.FinishToolCalls
	}
	end := time.Now()
	tm := llm.Timings{Total: end.Sub(start), EvalTokens: resp.Usage.CompletionTokens, PromptEvalTokens: resp.Usage.PromptTokens - resp.Usage.CachedPromptTokens}
	if !first.IsZero() {
		tm.PromptEval, tm.Eval = first.Sub(start), end.Sub(first)
	}
	if req.Stream {
		if st == nil {
			st = fe.NewStream(w, meta)
			if st.Start() != nil {
				return
			}
		}
		st.Finish(resp.FinishReason, resp.Usage, tm)
	} else {
		fe.WriteResponse(w, meta, &resp, tm)
	}
	line := fmt.Sprintf("#%d %s  prompt %d · first token %s · %d tok · %s", n, fe.Name(), resp.Usage.PromptTokens, roundDur(tm.PromptEval), resp.Usage.CompletionTokens, roundDur(tm.Total))
	if len(resp.ToolCalls) > 0 {
		line += fmt.Sprintf(" · %d tool call(s)", len(resp.ToolCalls))
	}
	s.logf("%s\n", line)
}

// bridgeError maps a local runtime failure to what the harness should see.
// Harnesses retry 5xx answers (Codex shows "high demand"), which only
// repeats a deterministic failure such as a chat-template exception, so
// those become 400s with the runtime's own message. A runtime that is still
// loading (503) stays retryable.
func bridgeError(err error, where string) (int, string, string) {
	var he *upstream.HTTPError
	if errors.As(err, &he) {
		if he.Status == http.StatusServiceUnavailable {
			return he.Status, "api_error", "local runtime is busy or still loading the model: " + err.Error()
		}
		if he.Status >= 500 {
			return http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("local runtime (%s) failed on this request: %v", where, err)
		}
		status, kind := upstreamErrorStatus(err)
		return status, kind, "local runtime: " + err.Error()
	}
	return http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("cannot reach the local runtime (%s): %v — is it running?", where, err)
}

func roundDur(d time.Duration) time.Duration {
	if d > time.Second {
		return d.Round(100 * time.Millisecond)
	}
	return d.Round(time.Millisecond)
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
	s.logWarnings(n, rec.Protocol, prep.Req)
	var b strings.Builder
	for _, note := range pl.Notes {
		if strings.HasPrefix(note, "upstream prompt_tokens=") {
			fmt.Fprintf(&b, "#%d %s  ! %s\n", n, rec.Protocol, note)
		}
	}
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
	for _, note := range pl.Notes {
		fmt.Fprintf(&b, "     ↳ %s\n", note)
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
			return l.Addr().(*net.TCPAddr).Port, nil
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
	if s.cfg.Device == nil {
		return fmt.Sprintf("trialm bridge relayed %d request(s) to %s\n", s.bridgeN.Load(), s.cfg.Upstream.Describe())
	}
	return s.cfg.Device.Stats().Summarize().Report(s.cfg.DeviceName, s.cfg.RuntimeName, s.cfg.Model.Name)
}

// StatsJSON returns the stats as JSON.
func (s *Server) StatsJSON() ([]byte, error) {
	if s.cfg.Device == nil {
		return []byte("[]"), nil
	}
	return json.MarshalIndent(s.cfg.Device.Stats().Snapshot(), "", "  ")
}

// logWarnings makes lossy conversions visible on both trial and bridge paths.
func (s *Server) logWarnings(n int, proto string, req *llm.Request) {
	for _, msg := range req.Warnings {
		s.logf("#%d %s  ! %s\n", n, proto, msg)
	}
	if s.cfg.Bridge && req.Protocol == llm.ProtocolOllama && req.NumCtx > 0 {
		client, native := s.cfg.Upstream.(*upstream.OpenAICompat)
		if native && client.Ollama && s.cfg.Model.ContextLength > 0 && req.NumCtx != s.cfg.Model.ContextLength {
			s.warnedNumCtx.Do(func() {
				s.logf("#%d %s  ! options.num_ctx=%d differs from the configured context (%d); Ollama may reload the model\n", n, proto, req.NumCtx, s.cfg.Model.ContextLength)
			})
		} else if !native || !client.Ollama {
			s.warnedNumCtx.Do(func() {
				ctxN := s.cfg.Model.ContextLength
				if ctxN > 0 {
					s.logf("#%d %s  ! options.num_ctx=%d ignored: the runtime's context is fixed at %d (set it on the runtime)\n", n, proto, req.NumCtx, ctxN)
				} else {
					s.logf("#%d %s  ! options.num_ctx=%d ignored: set the context on the runtime\n", n, proto, req.NumCtx)
				}
			})
		}
	}
	pastLeading := false
	for _, m := range req.Messages {
		if m.Role != llm.RoleSystem {
			pastLeading = true
			continue
		}
		if pastLeading {
			s.warnedSystem.Do(func() {
				s.logf("#%d %s  ! mid-conversation system message merged into a user turn for the runtime's chat template\n", n, proto)
			})
			break
		}
	}
}
