/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package drift

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"
)

type Sample struct {
	Labels map[string]string
	Value  float64
}

type PromClient interface {
	Query(ctx context.Context, query string, ts time.Time) ([]Sample, error)
}

type HTTPPromOption func(*HTTPPromClient)

func WithHTTPClient(c *http.Client) HTTPPromOption {
	return func(h *HTTPPromClient) { h.client = c }
}

func WithBearerToken(token string) HTTPPromOption {
	return func(h *HTTPPromClient) { h.token = token }
}

func WithBearerTokenFile(path string) HTTPPromOption {
	return func(h *HTTPPromClient) { h.tokenFile = path }
}

type HTTPPromClient struct {
	baseURL   string
	client    *http.Client
	token     string
	tokenFile string
}

func NewHTTPPromClient(baseURL string, opts ...HTTPPromOption) *HTTPPromClient {
	c := &HTTPPromClient{
		baseURL:   baseURL,
		tokenFile: "/var/run/secrets/kubernetes.io/serviceaccount/token",
	}
	for _, o := range opts {
		o(c)
	}
	if c.client == nil {
		// In-cluster Thanos/Prometheus uses self-signed serving certs;
		// the connection is already inside the cluster network.
		c.client = &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
			},
		}
	}
	return c
}

func (c *HTTPPromClient) Query(ctx context.Context, query string, ts time.Time) ([]Sample, error) {
	u := c.baseURL + "/api/v1/query?query=" + url.QueryEscape(query) +
		"&time=" + strconv.FormatFloat(float64(ts.Unix()), 'f', -1, 64)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("prom query: build request: %w", err)
	}

	tok, err := c.bearerToken()
	if err != nil {
		return nil, fmt.Errorf("prom query: read token: %w", err)
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("prom query: %w", err)
	}
	defer resp.Body.Close()

	var body struct {
		Status    string `json:"status"`
		Error     string `json:"error"`
		ErrorType string `json:"errorType"`
		Data      struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string  `json:"metric"`
				Value  [2]json.RawMessage `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("prom query: decode response: %w", err)
	}
	if body.Status != "success" {
		return nil, fmt.Errorf("prom query: %s: %s", body.ErrorType, body.Error)
	}

	samples := make([]Sample, 0, len(body.Data.Result))
	for _, r := range body.Data.Result {
		var valStr string
		if err := json.Unmarshal(r.Value[1], &valStr); err != nil {
			return nil, fmt.Errorf("prom query: unmarshal value: %w", err)
		}
		v, err := strconv.ParseFloat(valStr, 64)
		if err != nil {
			return nil, fmt.Errorf("prom query: parse float %q: %w", valStr, err)
		}
		samples = append(samples, Sample{Labels: r.Metric, Value: v})
	}
	return samples, nil
}

// bearerToken returns the explicit token if set, otherwise reads the token file.
func (c *HTTPPromClient) bearerToken() (string, error) {
	if c.token != "" {
		return c.token, nil
	}
	b, err := os.ReadFile(c.tokenFile)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(b), nil
}
