package setup

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/aeroknight786/trialm/internal/catalog"
	"github.com/aeroknight786/trialm/internal/setup/download"
)

func hfLocation(raw string) (string, string) {
	u, err := url.Parse(raw)
	if err != nil || u.Host != "huggingface.co" {
		return "", ""
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(parts) < 5 {
		return "", ""
	}
	return strings.Join(parts[:2], "/"), strings.Join(parts[4:], "/")
}

// Resolve reads file metadata only. Weight bytes are fetched after approval.
func Resolve(ctx context.Context, in Input, token string) (Input, error) {
	if in.LocalModel != "" || len(in.Assets) > 0 {
		return in, nil
	}
	repo := in.Model.HFGGUFRepo
	if in.Quant.Format == "mlx" {
		repo = in.Model.HFMLXRepo
	}
	if repo == "" {
		return in, errors.New("no Hugging Face repository is known for this model")
	}
	client := &http.Client{Timeout: 30 * time.Second}
	get := func(raw string) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
		if err != nil {
			return nil, err
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("Hugging Face metadata returned %s", resp.Status)
		}
		return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	}
	b, err := get("https://huggingface.co/api/models/" + repo + "/revision/main?blobs=true")
	if err != nil {
		return in, err
	}
	var info struct {
		SHA      string `json:"sha"`
		Siblings []struct {
			Name string `json:"rfilename"`
			Size int64  `json:"size"`
			LFS  *struct {
				SHA  string `json:"sha256"`
				Size int64  `json:"size"`
			} `json:"lfs"`
		} `json:"siblings"`
	}
	if err = json.Unmarshal(b, &info); err != nil {
		return in, err
	}
	if len(info.SHA) != 40 {
		return in, errors.New("missing immutable Hugging Face revision")
	}
	sort.Slice(info.Siblings, func(i, j int) bool { return info.Siblings[i].Name < info.Siblings[j].Name })
	for _, file := range info.Siblings {
		selected := strings.EqualFold(catalog.FileTag(file.Name), in.Quant.HFTag()) && strings.HasSuffix(strings.ToLower(file.Name), ".gguf")
		if in.Quant.Format == "mlx" {
			selected = slices.Contains([]string{".safetensors", ".json", ".model", ".txt", ".jinja"}, path.Ext(file.Name))
		}
		if !selected {
			continue
		}
		if path.IsAbs(file.Name) || path.Clean(file.Name) != file.Name || strings.HasPrefix(file.Name, "../") {
			return in, errors.New("unsafe model file name")
		}
		raw := (&url.URL{Scheme: "https", Host: "huggingface.co", Path: "/" + repo + "/resolve/" + info.SHA + "/" + file.Name}).String()
		digest := ""
		size := file.Size
		if file.LFS != nil {
			digest = file.LFS.SHA
			size = file.LFS.Size
		} else if size <= 2<<20 && in.Quant.Format == "mlx" && path.Ext(file.Name) != ".safetensors" {
			config, err := get(raw)
			if err != nil {
				return in, err
			}
			digest = fmt.Sprintf("%x", sha256.Sum256(config))
		}
		if len(digest) != 64 {
			return in, fmt.Errorf("no sha256 for %s", file.Name)
		}
		in.Assets = append(in.Assets, download.Request{URL: raw, SHA256: digest, Size: size, Dest: file.Name})
	}
	if len(in.Assets) == 0 {
		return in, fmt.Errorf("no pinned %s file in %s", in.Quant.HFTag(), repo)
	}
	return in, nil
}
