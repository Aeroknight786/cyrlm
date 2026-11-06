package ollama

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/aeroknight786/trialm/internal/api"
)

// defaultFamily is reported when a ModelInfo has no Family, so clients that
// read "<arch>.context_length" still find a key.
const defaultFamily = "llama"

type modelDetails struct {
	ParentModel       string   `json:"parent_model"`
	Format            string   `json:"format"`
	Family            string   `json:"family"`
	Families          []string `json:"families"`
	ParameterSize     string   `json:"parameter_size"`
	QuantizationLevel string   `json:"quantization_level"`
}

func details(m api.ModelInfo) modelDetails {
	fam := family(m)
	return modelDetails{
		Format:            "gguf",
		Family:            fam,
		Families:          []string{fam},
		ParameterSize:     m.ParameterSize,
		QuantizationLevel: m.Quantization,
	}
}

func family(m api.ModelInfo) string {
	if m.Family != "" {
		return m.Family
	}
	return defaultFamily
}

func modifiedAt(m api.ModelInfo) string {
	t := m.ModifiedAt
	if t.IsZero() {
		t = time.Now()
	}
	return t.Format(time.RFC3339Nano)
}

// Digest returns the stable fake digest reported for a model name: the
// lowercase hex SHA-256 of the name.
func Digest(name string) string {
	sum := sha256.Sum256([]byte(name))
	return hex.EncodeToString(sum[:])
}

// WriteVersion writes the /api/version response {"version":v}.
func WriteVersion(w http.ResponseWriter, version string) error {
	return api.WriteJSON(w, http.StatusOK, struct {
		Version string `json:"version"`
	}{version})
}

type tagModel struct {
	Name       string       `json:"name"`
	Model      string       `json:"model"`
	ModifiedAt string       `json:"modified_at"`
	Size       int64        `json:"size"`
	Digest     string       `json:"digest"`
	Details    modelDetails `json:"details"`
}

// WriteTags writes the /api/tags response listing models.
func WriteTags(w http.ResponseWriter, models []api.ModelInfo) error {
	out := struct {
		Models []tagModel `json:"models"`
	}{Models: []tagModel{}}
	for _, m := range models {
		out.Models = append(out.Models, tagModel{
			Name: m.Name, Model: m.Name, ModifiedAt: modifiedAt(m),
			Size: m.SizeBytes, Digest: Digest(m.Name), Details: details(m),
		})
	}
	return api.WriteJSON(w, http.StatusOK, out)
}

type showResponse struct {
	License      string         `json:"license"`
	Modelfile    string         `json:"modelfile"`
	Parameters   string         `json:"parameters"`
	Template     string         `json:"template"`
	Details      modelDetails   `json:"details"`
	ModelInfo    map[string]any `json:"model_info"`
	Capabilities []string       `json:"capabilities"`
	ModifiedAt   string         `json:"modified_at"`
}

// WriteShow writes the /api/show response for m. Zed and JetBrains read
// model_info["<general.architecture>.context_length"] and capabilities.
func WriteShow(w http.ResponseWriter, m api.ModelInfo) error {
	fam := family(m)
	info := map[string]any{
		"general.architecture":  fam,
		fam + ".context_length": m.ContextLength,
	}
	if n, ok := ParseParameterCount(m.ParameterSize); ok {
		info["general.parameter_count"] = n
	}
	caps := append([]string(nil), m.Capabilities...)
	if len(caps) == 0 {
		caps = []string{"completion"}
	}
	return api.WriteJSON(w, http.StatusOK, showResponse{
		Modelfile:    "# simulated by triaLM",
		Parameters:   fmt.Sprintf("num_ctx %d", m.ContextLength),
		Details:      details(m),
		ModelInfo:    info,
		Capabilities: caps,
		ModifiedAt:   modifiedAt(m),
	})
}

// ParseParameterCount converts a display size such as "20.9B", "567M",
// "1.5b" or "1.2T" into a parameter count.
func ParseParameterCount(size string) (int64, bool) {
	s := strings.TrimSpace(size)
	if s == "" {
		return 0, false
	}
	mult := 1.0
	switch s[len(s)-1] {
	case 'K', 'k':
		mult = 1e3
	case 'M', 'm':
		mult = 1e6
	case 'B', 'b', 'G', 'g':
		mult = 1e9
	case 'T', 't':
		mult = 1e12
	}
	if mult != 1 {
		s = s[:len(s)-1]
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || f <= 0 || math.IsInf(f, 0) || math.IsNaN(f) {
		return 0, false
	}
	return int64(math.Round(f * mult)), true
}

type psModel struct {
	Name          string       `json:"name"`
	Model         string       `json:"model"`
	Size          int64        `json:"size"`
	Digest        string       `json:"digest"`
	Details       modelDetails `json:"details"`
	ExpiresAt     string       `json:"expires_at"`
	SizeVRAM      int64        `json:"size_vram"`
	ContextLength int          `json:"context_length,omitempty"`
}

// WritePS writes the /api/ps response: the loaded models (pass none for an
// empty list), all fully resident in VRAM until expiresAt.
func WritePS(w http.ResponseWriter, models []api.ModelInfo, expiresAt time.Time) error {
	out := struct {
		Models []psModel `json:"models"`
	}{Models: []psModel{}}
	for _, m := range models {
		out.Models = append(out.Models, psModel{
			Name: m.Name, Model: m.Name, Size: m.SizeBytes, Digest: Digest(m.Name),
			Details: details(m), ExpiresAt: expiresAt.Format(time.RFC3339Nano),
			SizeVRAM: m.SizeBytes, ContextLength: m.ContextLength,
		})
	}
	return api.WriteJSON(w, http.StatusOK, out)
}

// DecodeShowRequest parses an /api/show body, accepting {"model":...} or the
// legacy {"name":...}.
func DecodeShowRequest(body []byte) (string, error) {
	var req struct {
		Model string `json:"model"`
		Name  string `json:"name"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return "", fmt.Errorf("invalid JSON body: %v", err)
	}
	if req.Model != "" {
		return req.Model, nil
	}
	if req.Name != "" {
		return req.Name, nil
	}
	return "", errors.New("model is required")
}
