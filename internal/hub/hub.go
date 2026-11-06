// Package hub reads Hugging Face model metadata needed to pick a trial
// stand-in: the base model a fine-tuned, abliterated or quantized repo was
// derived from.
package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// API is the Hugging Face API base (overridable in tests).
var API = "https://huggingface.co"

var client = &http.Client{Timeout: 15 * time.Second}

// RepoFromURL extracts "owner/name" from a huggingface.co file or repo URL.
func RepoFromURL(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || !strings.HasSuffix(u.Host, "huggingface.co") {
		return "", false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", false
	}
	if parts[0] == "api" || parts[0] == "datasets" || parts[0] == "spaces" {
		return "", false
	}
	return parts[0] + "/" + parts[1], true
}

type modelInfo struct {
	CardData struct {
		BaseModel json.RawMessage `json:"base_model"`
	} `json:"cardData"`
	Tags []string `json:"tags"`
}

// BaseModels returns the base models a repo declares (model card
// base_model, else base_model:* tags), nearest first.
func BaseModels(ctx context.Context, repo string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, API+"/api/models/"+repo, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "trialm")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("hugging face: %s: HTTP %d", repo, resp.StatusCode)
	}
	var mi modelInfo
	if err := json.NewDecoder(resp.Body).Decode(&mi); err != nil {
		return nil, err
	}
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || strings.EqualFold(s, repo) {
			return
		}
		for _, o := range out {
			if strings.EqualFold(o, s) {
				return
			}
		}
		out = append(out, s)
	}
	if len(mi.CardData.BaseModel) > 0 {
		var one string
		var many []string
		if json.Unmarshal(mi.CardData.BaseModel, &one) == nil {
			add(one)
		} else if json.Unmarshal(mi.CardData.BaseModel, &many) == nil {
			for _, m := range many {
				add(m)
			}
		}
	}
	if len(out) == 0 {
		for _, t := range mi.Tags {
			if !strings.HasPrefix(t, "base_model:") {
				continue
			}
			v := strings.TrimPrefix(t, "base_model:")
			for _, rel := range []string{"quantized:", "finetune:", "merge:", "adapter:"} {
				v = strings.TrimPrefix(v, rel)
			}
			add(v)
		}
	}
	return out, nil
}

// BaseChain follows the first declared base up to maxHops times and returns
// the ancestors, nearest first (the repo itself is not included).
func BaseChain(ctx context.Context, repo string, maxHops int) []string {
	var chain []string
	cur := repo
	for i := 0; i < maxHops; i++ {
		bases, err := BaseModels(ctx, cur)
		if err != nil || len(bases) == 0 {
			break
		}
		next := bases[0]
		for _, c := range chain {
			if strings.EqualFold(c, next) {
				return chain
			}
		}
		chain = append(chain, next)
		cur = next
	}
	return chain
}
