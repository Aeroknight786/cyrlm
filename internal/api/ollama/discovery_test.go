package ollama

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aeroknight786/trialm/internal/api"
	"github.com/aeroknight786/trialm/internal/llm"
)

var model = api.ModelInfo{
	Name: "gpt-oss:20b", Family: "gptoss", ParameterSize: "20.9B", Quantization: "MXFP4",
	ContextLength: 131072, SizeBytes: 13780173734, Capabilities: []string{"completion", "tools", "thinking"},
	ModifiedAt: time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC),
}

func decode(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("%s: %v", body, err)
	}
	return m
}

func TestWriteVersion(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := WriteVersion(rec, "0.12.6"); err != nil {
		t.Fatal(err)
	}
	if canon(t, decode(t, rec.Body.Bytes())) != `{"version":"0.12.6"}` {
		t.Errorf("%s", rec.Body)
	}
}

func TestWriteTags(t *testing.T) {
	sum := sha256.Sum256([]byte("gpt-oss:20b"))
	digest := hex.EncodeToString(sum[:])
	if Digest("gpt-oss:20b") != digest {
		t.Fatal("digest")
	}
	rec := httptest.NewRecorder()
	if err := WriteTags(rec, []api.ModelInfo{model}); err != nil {
		t.Fatal(err)
	}
	want := `{"models":[{"name":"gpt-oss:20b","model":"gpt-oss:20b","modified_at":"2026-08-05T10:00:00Z","size":13780173734,"digest":"` + digest +
		`","details":{"parent_model":"","format":"gguf","family":"gptoss","families":["gptoss"],"parameter_size":"20.9B","quantization_level":"MXFP4"}}]}`
	if got := canon(t, decode(t, rec.Body.Bytes())); got != llm.CanonicalJSON(want) {
		t.Errorf("tags:\n got %s\nwant %s", got, llm.CanonicalJSON(want))
	}

	rec = httptest.NewRecorder()
	_ = WriteTags(rec, nil)
	if canon(t, decode(t, rec.Body.Bytes())) != `{"models":[]}` {
		t.Errorf("empty tags %s", rec.Body)
	}
}

func TestWriteShow(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := WriteShow(rec, model); err != nil {
		t.Fatal(err)
	}
	got := decode(t, rec.Body.Bytes())
	want := `{"license":"","modelfile":"# simulated by triaLM","parameters":"num_ctx 131072","template":"",` +
		`"details":{"parent_model":"","format":"gguf","family":"gptoss","families":["gptoss"],"parameter_size":"20.9B","quantization_level":"MXFP4"},` +
		`"model_info":{"general.architecture":"gptoss","general.parameter_count":20900000000,"gptoss.context_length":131072},` +
		`"capabilities":["completion","tools","thinking"],"modified_at":"2026-08-05T10:00:00Z"}`
	if canon(t, got) != llm.CanonicalJSON(want) {
		t.Errorf("show:\n got %s\nwant %s", canon(t, got), llm.CanonicalJSON(want))
	}

	// Unparseable size: no parameter_count; no family/capabilities: defaults.
	rec = httptest.NewRecorder()
	_ = WriteShow(rec, api.ModelInfo{Name: "x", ParameterSize: "big", ContextLength: 4096})
	got = decode(t, rec.Body.Bytes())
	info := got["model_info"].(map[string]any)
	if _, ok := info["general.parameter_count"]; ok || info["llama.context_length"].(float64) != 4096 {
		t.Errorf("model_info %v", info)
	}
	if canon(t, got["capabilities"]) != `["completion"]` {
		t.Errorf("capabilities %v", got["capabilities"])
	}
}

func TestParseParameterCount(t *testing.T) {
	for in, want := range map[string]int64{"20.9B": 20_900_000_000, "8B": 8e9, "1.5b": 1_500_000_000, "567M": 567e6, "1.2T": 1.2e12, "350K": 350e3, "7000": 7000} {
		if got, ok := ParseParameterCount(in); !ok || got != want {
			t.Errorf("%s: %d %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "B", "big", "-3B"} {
		if _, ok := ParseParameterCount(in); ok {
			t.Errorf("%q should not parse", in)
		}
	}
}

func TestWritePS(t *testing.T) {
	exp := time.Date(2026, 11, 6, 12, 5, 0, 0, time.UTC)
	rec := httptest.NewRecorder()
	if err := WritePS(rec, []api.ModelInfo{model}, exp); err != nil {
		t.Fatal(err)
	}
	want := `{"models":[{"name":"gpt-oss:20b","model":"gpt-oss:20b","size":13780173734,"digest":"` + Digest("gpt-oss:20b") +
		`","details":{"parent_model":"","format":"gguf","family":"gptoss","families":["gptoss"],"parameter_size":"20.9B","quantization_level":"MXFP4"},` +
		`"expires_at":"2026-11-06T12:05:00Z","size_vram":13780173734,"context_length":131072}]}`
	if got := canon(t, decode(t, rec.Body.Bytes())); got != llm.CanonicalJSON(want) {
		t.Errorf("ps:\n got %s\nwant %s", got, llm.CanonicalJSON(want))
	}
	rec = httptest.NewRecorder()
	_ = WritePS(rec, nil, time.Time{})
	if canon(t, decode(t, rec.Body.Bytes())) != `{"models":[]}` {
		t.Errorf("empty ps %s", rec.Body)
	}
}

func TestDecodeShowRequest(t *testing.T) {
	for body, want := range map[string]string{
		`{"model":"gpt-oss:20b"}`:              "gpt-oss:20b",
		`{"name":"llama3.2"}`:                  "llama3.2",
		`{"model":"a","name":"b","verbose":1}`: "a",
	} {
		if got, err := DecodeShowRequest([]byte(body)); err != nil || got != want {
			t.Errorf("%s: %q %v", body, got, err)
		}
	}
	for _, body := range []string{`{}`, `nope`} {
		if _, err := DecodeShowRequest([]byte(body)); err == nil {
			t.Errorf("%s: expected error", body)
		}
	}
}
