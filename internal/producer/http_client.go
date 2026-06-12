package producer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const (
	apiKeyHeader   = "X-CES-SA-API-KEY"
	defaultTimeout = 30 * time.Second
)

// ServiceAccountClient manages service accounts on a specific producer.
type ServiceAccountClient interface {
	// Create provisions a new service account and returns its credentials.
	Create(ctx context.Context, consumer string, params Params) (map[string]string, error)
	// Update re-provisions an existing service account and returns the refreshed credentials.
	// Not yet implemented — requires a PUT endpoint on the producer side.
	Update(ctx context.Context, consumer string, params Params) (map[string]string, error)
	// Delete removes a service account at the producer.
	Delete(ctx context.Context, consumer string) error
	// Ready returns nil when the producer endpoint is reachable
	Ready(ctx context.Context) error
}

// HttpClient is an HTTP client bound to a specific producer endpoint and API key.
type HttpClient struct {
	client   *http.Client
	endpoint string
	apiKey   string
}

// NewHTTPClient creates an HttpClient bound to a specific producer endpoint and API key.
func NewHTTPClient(endpoint, apiKey string) *HttpClient {
	return &HttpClient{
		client:   &http.Client{Timeout: defaultTimeout},
		endpoint: endpoint,
		apiKey:   apiKey,
	}
}

type createRequestBody struct {
	Consumer string `json:"consumer"`
	Params   Params `json:"params,omitempty"`
}

// Create calls POST {endpoint} to create a service account and returns the credentials.
func (c *HttpClient) Create(ctx context.Context, consumer string, params Params) (map[string]string, error) {
	body, err := json.Marshal(createRequestBody{Consumer: consumer, Params: params})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(apiKeyHeader, c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request to producer %q failed: %w", c.endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("producer returned unexpected status %d for %q: %s", resp.StatusCode, c.endpoint, string(respBody))
	}

	var credentials map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&credentials); err != nil {
		return nil, fmt.Errorf("failed to decode producer response: %w", err)
	}

	return credentials, nil
}

// Update is not yet implemented. The producer API requires a PUT endpoint that does not exist yet.
func (c *HttpClient) Update(_ context.Context, _ string, _ Params) (map[string]string, error) {
	panic("Update is not yet implemented — requires PUT endpoint on the producer side")
}

// Ready probes the producer endpoint with a HEAD request. Any HTTP response — even 4xx/5xx —
// counts as reachable; only transport errors (DNS failure, connection refused, missing netpols)
// mean the endpoint is unreachable.
func (c *HttpClient) Ready(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, c.endpoint, nil)
	if err != nil {
		return fmt.Errorf("failed to build readiness probe request for %q: %w", c.endpoint, err)
	}
	req.Header.Set(apiKeyHeader, c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("endpoint %q is not reachable: %w", c.endpoint, err)
	}
	_ = resp.Body.Close()

	return nil
}

// Delete calls DELETE {endpoint}/{consumer} to remove a service account.
func (c *HttpClient) Delete(ctx context.Context, consumer string) error {
	targetURL, err := url.JoinPath(c.endpoint, consumer)
	if err != nil {
		return fmt.Errorf("failed to build URL for producer endpoint %q and consumer %q: %w", c.endpoint, consumer, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, targetURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}
	req.Header.Set(apiKeyHeader, c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request to producer %q failed: %w", targetURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// A missing service account is an acceptable outcome for a delete.
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("producer returned unexpected status %d for %q: %s", resp.StatusCode, targetURL, string(respBody))
	}

	return nil
}
