// Package openrouter implements authentication, credits and endpoint discovery.
package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const API = "https://openrouter.ai/api/v1"

type Client struct {
	BaseURL, Key string
	HTTP         *http.Client
}

func (c Client) Do(ctx context.Context, method, path string, body, out any) error {
	base := c.BaseURL
	if base == "" {
		base = API
	}
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base, "/")+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Title", "triaLM")
	req.Header.Set("HTTP-Referer", "https://github.com/aeroknight786/trialm")
	if c.Key != "" {
		req.Header.Set("Authorization", "Bearer "+c.Key)
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// Never echo a remote body: an auth service may reflect a secret.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("OpenRouter %s: HTTP %d", path, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
}
func (c Client) Credits(ctx context.Context) (float64, error) {
	var r struct {
		Data struct {
			TotalCredits float64 `json:"total_credits"`
			TotalUsage   float64 `json:"total_usage"`
		} `json:"data"`
	}
	err := c.Do(ctx, "GET", "/credits", nil, &r)
	return r.Data.TotalCredits - r.Data.TotalUsage, err
}
func (c Client) Exchange(ctx context.Context, code, verifier string) (string, error) {
	var r struct {
		Key string `json:"key"`
	}
	err := c.Do(ctx, "POST", "/auth/keys", map[string]string{"code": code, "code_verifier": verifier, "code_challenge_method": "S256"}, &r)
	if err == nil && r.Key == "" {
		err = fmt.Errorf("OpenRouter returned no key")
	}
	return r.Key, err
}

// Price is USD per token, as supplied by the endpoints API.
type Price struct {
	Prompt     string `json:"prompt"`
	Completion string `json:"completion"`
	CacheRead  string `json:"input_cache_read"`
	Request    string `json:"request"`
}
type Endpoint struct {
	ModelID             string   `json:"model_id"`
	SafetyRetention     *bool    `json:"safety_retention"`
	Name                string   `json:"name"`
	ProviderName        string   `json:"provider_name"`
	Tag                 string   `json:"tag"`
	Quantization        string   `json:"quantization"`
	ContextLength       int      `json:"context_length"`
	SupportedParameters []string `json:"supported_parameters"`
	DataPolicy          struct {
		DataCollection string `json:"data_collection"`
		ZDR            bool   `json:"zdr"`
	} `json:"data_policy"`
	// Some cached endpoint records expose these at the top level.
	DataCollection string   `json:"data_collection"`
	ZDR            bool     `json:"zdr"`
	Pricing        Price    `json:"pricing"`
	Uptime         *float64 `json:"uptime_last_30m"`
}

func (c Client) Endpoints(ctx context.Context, id string) ([]Endpoint, error) {
	var r struct {
		Data struct {
			Endpoints []Endpoint `json:"endpoints"`
		} `json:"data"`
	}
	// Preserve the owner/model slash, while preventing query/path injection.
	parts := strings.Split(id, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	err := c.Do(ctx, "GET", "/models/"+strings.Join(parts, "/")+"/endpoints", nil, &r)
	return r.Data.Endpoints, err
}

// PrivateEndpoints joins unlabelled model records to the published ZDR list.
// The normal endpoint schema does not promise data_policy fields. Only an
// exact model/tag/precision match with explicit safety_retention:false can
// establish eligibility; a contradictory allow policy is never overwritten.
// https://openrouter.ai/docs/api/api-reference/endpoints/preview-the-impact-of-zdr-on-the-available-endpoints
func (c Client) PrivateEndpoints(ctx context.Context, id string, zdr bool) ([]Endpoint, error) {
	eps, err := c.Endpoints(ctx, id)
	if err != nil {
		return nil, err
	}
	need := false
	for _, e := range eps {
		if e.DataCollection == "" && e.DataPolicy.DataCollection == "" {
			need = true
		}
		if zdr && !e.ZDR && !e.DataPolicy.ZDR {
			need = true
		}
	}
	if !need {
		return eps, nil
	}
	var list struct {
		Data []Endpoint `json:"data"`
	}
	if err = c.Do(ctx, "GET", "/endpoints/zdr", nil, &list); err != nil {
		return nil, err
	}
	for i, e := range eps {
		policy := e.DataCollection
		if policy == "" {
			policy = e.DataPolicy.DataCollection
		}
		if policy != "" && policy != "deny" {
			continue
		}
		for _, known := range list.Data {
			if known.ModelID == id && known.Tag == e.Tag && known.Quantization == e.Quantization && known.SafetyRetention != nil && !*known.SafetyRetention {
				eps[i].DataCollection = "deny"
				eps[i].ZDR = true
				safe := false
				eps[i].SafetyRetention = &safe
				break
			}
		}
	}
	return eps, nil
}
