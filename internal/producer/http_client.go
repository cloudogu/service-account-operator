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

// CreateParams contains the parameters forwarded to the producer when creating a service account.
type CreateParams struct {
	Options map[string][]string
	Args    []string
}

// HTTPClient creates service accounts on a specific producer via HTTP.
type HTTPClient interface {
	Create(ctx context.Context, consumer string, params CreateParams) (map[string]string, error)
}

type httpClient struct {
	client   *http.Client
	endpoint string
	apiKey   string
}

// NewHTTPClient creates an HTTPClient bound to a specific producer endpoint and API key.
func NewHTTPClient(endpoint, apiKey string) HTTPClient {
	return &httpClient{
		client:   &http.Client{Timeout: defaultTimeout},
		endpoint: endpoint,
		apiKey:   apiKey,
	}
}

type createRequestBody struct {
	Params createParamsBody `json:"params,omitempty"`
}

type createParamsBody struct {
	Options map[string][]string `json:"options,omitempty"`
	Args    []string            `json:"args,omitempty"`
}

type createResponseBody struct {
	Credentials map[string]string `json:"credentials"`
}

// Create calls PUT {endpoint}/{consumer} to create a service account and returns the credentials.
func (c *httpClient) Create(ctx context.Context, consumer string, params CreateParams) (map[string]string, error) {
	targetURL, err := url.JoinPath(c.endpoint, consumer)
	if err != nil {
		return nil, fmt.Errorf("failed to build URL for producer endpoint %q and consumer %q: %w", c.endpoint, consumer, err)
	}

	body, err := json.Marshal(createRequestBody{
		Params: createParamsBody{Options: params.Options, Args: params.Args},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(apiKeyHeader, c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request to producer %q failed: %w", targetURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("producer returned unexpected status %d for %q: %s", resp.StatusCode, targetURL, string(respBody))
	}

	var result createResponseBody
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode producer response: %w", err)
	}

	return result.Credentials, nil
}
