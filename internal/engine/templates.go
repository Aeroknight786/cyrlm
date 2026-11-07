package engine

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type Asset struct {
	Support []Asset `json:"support,omitempty"`
	URL     string  `json:"url"`
	SHA256  string  `json:"sha256"`
	Size    int64   `json:"size"`
	Root    string  `json:"root"`
	Entry   string  `json:"entry"`
}

var data struct {
	OllamaRelease string              `json:"ollama_release"`
	MinimumBuild  int                 `json:"minimum_build"`
	MaximumBuild  int                 `json:"maximum_build"`
	OtherAssets   map[string]Asset    `json:"other_assets"`
	LMSInstaller  Installer           `json:"lms_installer"`
	Release       string              `json:"release"`
	Assets        map[string]Asset    `json:"assets"`
	Templates     map[string][]string `json:"templates"`
}

func init() {
	if err := json.Unmarshal(profilesJSON, &data); err != nil {
		panic(err)
	}
}
func Release() string { return data.Release }
func ReleaseAsset(goos, arch, backend string) (Asset, error) {
	a, ok := data.Assets[goos+"/"+arch+"/"+backend]
	if !ok {
		return a, fmt.Errorf("no pinned llama.cpp asset for %s/%s %s", goos, arch, backend)
	}
	return a, nil
}

// Args expands one embedded argv template. Substitutions remain single arguments.
func Args(id string, values map[string]string) ([]string, error) {
	template, ok := data.Templates[id]
	if !ok {
		return nil, fmt.Errorf("unknown engine template %q", id)
	}
	out := append([]string(nil), template...)
	for i, arg := range out {
		if strings.HasPrefix(arg, "{") && strings.HasSuffix(arg, "}") {
			key := strings.TrimSuffix(strings.TrimPrefix(arg, "{"), "}")
			value, ok := values[key]
			if !ok {
				return nil, fmt.Errorf("unresolved engine argument %q", arg)
			}
			out[i] = value
		}
	}
	return out, nil
}

var buildVersion = regexp.MustCompile(`(?:\bb|version:\s*|build\s+)(\d{4,})`)

func CompatibleLlama(version string) bool {
	m := buildVersion.FindStringSubmatch(version)
	if m == nil {
		return false
	}
	n, _ := strconv.Atoi(m[1])
	return n >= data.MinimumBuild && n <= data.MaximumBuild
}

type Installer struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Source string `json:"source"`
}

func LMSInstaller() Installer { return data.LMSInstaller }
func OllamaAsset(goos, arch string) (Asset, error) {
	a, ok := data.OtherAssets["ollama/"+goos+"/"+arch]
	if !ok {
		return a, fmt.Errorf("no pinned Ollama asset for %s/%s", goos, arch)
	}
	return a, nil
}

func OllamaRelease() string { return data.OllamaRelease }
