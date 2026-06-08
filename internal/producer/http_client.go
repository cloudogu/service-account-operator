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

// Params contains the parameters forwarded to the producer when creating or updating a service account.
type Params struct {
	Options map[string][]string
	Args    []string
}

// HTTPClient manages service accounts on a specific producer via HTTP.
// The producer endpoint implements PUT (create), POST (update) and DELETE (delete)
// on {endpoint}/{consumer}, see ADR-0015.
type HTTPClient interface {
	// Create provisions a new service account and returns its credentials.
	Create(ctx context.Context, consumer string, params Params) (map[string]string, error)
	// Update re-provisions an existing service account and returns the refreshed credentials.
	Update(ctx context.Context, consumer string, params Params) (map[string]string, error)
	// Delete removes a service account at the producer.
	Delete(ctx context.Context, consumer string) error
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

type credentialRequestBody struct {
	Params credentialParamsBody `json:"params,omitempty"`
}

type credentialParamsBody struct {
	Options map[string][]string `json:"options,omitempty"`
	Args    []string            `json:"args,omitempty"`
}

type credentialResponseBody struct {
	Credentials map[string]string `json:"credentials"`
}

// Create calls PUT {endpoint}/{consumer} to create a service account and returns the credentials.
func (c *httpClient) Create(ctx context.Context, consumer string, params Params) (map[string]string, error) {
	return c.credentialRequest(ctx, http.MethodPut, consumer, params, http.StatusCreated)
}

// Update calls POST {endpoint}/{consumer} to re-provision a service account and returns the refreshed credentials.
func (c *httpClient) Update(ctx context.Context, consumer string, params Params) (map[string]string, error) {
	return c.credentialRequest(ctx, http.MethodPost, consumer, params, http.StatusOK)
}

// Delete calls DELETE {endpoint}/{consumer} to remove a service account.
func (c *httpClient) Delete(ctx context.Context, consumer string) error {
	targetURL, err := c.consumerURL(consumer)
	if err != nil {
		return err
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
	defer resp.Body.Close()

	// A missing service account is an acceptable outcome for a delete.
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("producer returned unexpected status %d for %q: %s", resp.StatusCode, targetURL, string(respBody))
	}

	return nil
}

// credentialRequest performs a create/update request that sends params and returns credentials.
func (c *httpClient) credentialRequest(ctx context.Context, method, consumer string, params Params, expectedStatus int) (map[string]string, error) {
	targetURL, err := c.consumerURL(consumer)
	if err != nil {
		return nil, err
	}

	body, err := json.Marshal(credentialRequestBody{
		Params: credentialParamsBody{Options: params.Options, Args: params.Args},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, targetURL, bytes.NewReader(body))
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

	if resp.StatusCode != expectedStatus {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("producer returned unexpected status %d for %q: %s", resp.StatusCode, targetURL, string(respBody))
	}

	var result credentialResponseBody
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode producer response: %w", err)
	}

	return result.Credentials, nil
}

func (c *httpClient) consumerURL(consumer string) (string, error) {
	targetURL, err := url.JoinPath(c.endpoint, consumer)
	if err != nil {
		return "", fmt.Errorf("failed to build URL for producer endpoint %q and consumer %q: %w", c.endpoint, consumer, err)
	}
	return targetURL, nil
}
