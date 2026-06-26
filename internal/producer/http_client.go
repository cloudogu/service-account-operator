package producer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"time"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	apiKeyHeader         = "X-CES-SA-API-KEY"
	defaultTimeout30secs = 30 * time.Second
)

// Params is the list of parameters forwarded to the producer when creating or updating a service account. These
// parameters are usually optional, anyhow developers of Service Account consumers are strongly asked to check the
// producers requirements.
type Params map[string]string

const (
	RotateSaNotNow BehaviorParamsRotation = iota
	RotateSaImmediately
)

// BehaviorParamsRotation indicates ways of how a Service Account's credential should be rotated.
type BehaviorParamsRotation int

// BehaviorParams may be used to by a consumer (via their SARE) to trigger actions towards the Service Account producer.
type BehaviorParams struct {
	// RotateServiceAccount indicates if a Service Account's credential should be rotated. This field must be ignored
	// during the first creation of a Service Account (because there is nothing to rotate).
	// This field is optional and defaults to [RotateSaNotNow].
	RotateServiceAccount BehaviorParamsRotation `json:"rotateServiceAccount,omitempty"`
}

// ServiceAccountClient manages service accounts on a specific producer.
type ServiceAccountClient interface {
	// Create provisions a new service account and returns its credentials.
	// TODO Delete Create after merging Jelemux'/Niklas' deletion code
	// Deprecated: use CreateOrUpdate instead
	Create(ctx context.Context, consumer string, params Params) (map[string]string, error)
	// CreateOrUpdate creates a service account or re-provisions (aka rotating) an existing service account and returns the refreshed credentials. The credential map may be nil if no change occurred.
	CreateOrUpdate(ctx context.Context, consumer string, params Params, behaviorParams BehaviorParams) (map[string]string, error)
	// Delete removes a service account at the producer.
	Delete(ctx context.Context, consumer string) error
	// Ready returns nil when the producer endpoint is reachable
	Ready(ctx context.Context) error
	// Exists returns true if the service account exists at the producer.
	Exists(ctx context.Context, consumer string) (bool, error)
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
		client:   &http.Client{Timeout: defaultTimeout30secs},
		endpoint: endpoint,
		apiKey:   apiKey,
	}
}

type createOrUpdateRequestBody struct {
	// Consumer contains the identifier of the Service Account Consumer. This field is required.
	Consumer string `json:"consumer"`
	// Params contains key/value parameters upon the producer modifies the service account creation/update. These
	// parameters are usually optional, anyhow developers of Service Account consumers are strongly asked to check the
	// producers requirements.
	Params Params `json:"params,omitempty"`
	// BehaviorParams contain information in which the Service Account producer may be triggered for an action. This field is strictly optional.
	BehaviorParams BehaviorParams `json:"behaviorParams,omitzero"`
}

// Exists checks if any Service Account exists for the consumer and returns true if the Producer API indicates so.
func (c *HttpClient) Exists(ctx context.Context, consumer string) (bool, error) {
	targetURL, err := url.JoinPath(c.endpoint, consumer)
	if err != nil {
		return false, fmt.Errorf("failed to build URL for producer endpoint %q and consumer %q: %w", c.endpoint, consumer, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, targetURL, http.NoBody)
	if err != nil {
		return false, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	req.Header.Set(apiKeyHeader, c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("HTTP request to producer %q failed: %w", c.endpoint, err)
	}
	defer func() {
		closeErr := resp.Body.Close()
		if closeErr != nil {
			logf.FromContext(ctx).Error(closeErr, "failed to close producer response body", "consumer", consumer, "endpoint", c.endpoint)
		}
	}()

	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("producer returned unexpected status %d for %q", resp.StatusCode, c.endpoint)
	}
}

// TODO Delete after merging but check changes to Create beforehand with CreateOrUpdate

// Create calls a service account producer's API to create a service account for the given consumer and returns the credentials.
// Deprecated: use CreateOrUpdate instead
func (c *HttpClient) Create(ctx context.Context, consumer string, params Params) (map[string]string, error) {
	body, err := json.Marshal(createOrUpdateRequestBody{Consumer: consumer, Params: params})
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
		return nil, fmt.Errorf("HTTP create-serviceaccount request to producer %q failed: %w", c.endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("producer %q rejected the request with 401; please check the API key in the auth secret", c.endpoint)
	}

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("producer returned unexpected status %s for %q: %s", resp.Status, c.endpoint, string(respBody))
	}

	var credentials map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&credentials); err != nil {
		return nil, fmt.Errorf("failed to decode producer response: %w", err)
	}

	return credentials, nil
}

// CreateOrUpdate calls a service account producer's API to idempotently modify a service account for the given consumer
// and returns the credentials. The credential map may be nil if no change occurred.
func (c *HttpClient) CreateOrUpdate(ctx context.Context, consumer string, params Params, behaviorParams BehaviorParams) (map[string]string, error) {
	// use a pointer to behaviorParams so the json marshaller will properly omit it
	bodyObject := createOrUpdateRequestBody{Consumer: consumer, Params: params, BehaviorParams: behaviorParams}
	body, err := json.Marshal(bodyObject)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal service account request body for consumer %s: %w", consumer, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP put request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(apiKeyHeader, c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http serviceaccount request to producer %q failed: %w", c.endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("producer %q rejected the request with %d; please check the API key in the SARE auth secret", c.endpoint, resp.StatusCode)
	}

	okayishStatusCodes := []int{http.StatusOK, http.StatusCreated, http.StatusNoContent}
	if !slices.Contains(okayishStatusCodes, resp.StatusCode) {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("producer returned unexpected status %s on update for %q: %s", resp.Status, c.endpoint, string(respBody))
	}

	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}

	var credentials map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&credentials); err != nil {
		return nil, fmt.Errorf("failed to decode producer response: %w", err)
	}

	return credentials, nil
}

// Ready checks the producer endpoint for basic readiness and returns an error if the endpoint is unreachable or returns a 5xx status.
// Transport errors (DNS failure, connection refused, missing netpols) as well as HTTP 401 and 5xx responses are treated as not-ready.
func (c *HttpClient) Ready(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, c.endpoint, nil)
	if err != nil {
		return fmt.Errorf("failed to build request to check producer readiness for %q: %w", c.endpoint, err)
	}
	req.Header.Set(apiKeyHeader, c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("endpoint %q is not ready because it is unreachable: %w", c.endpoint, err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("producer %q rejected the request with 401 — check the API key in the auth secret", c.endpoint)
	}

	if resp.StatusCode >= 500 {
		return fmt.Errorf("endpoint %q is not ready (status %s)", c.endpoint, resp.Status)
	}

	return nil
}

// Delete removes a consumer's service account from the service account producer.
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
		return fmt.Errorf("HTTP delete-serviceaccount request to producer %q failed: %w", targetURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("producer %q rejected the request with 401 — check the API key in the auth secret", c.endpoint)
	}

	// A missing service account is an acceptable outcome for a delete.
	if (resp.StatusCode < 200 || resp.StatusCode >= 300) && resp.StatusCode != http.StatusNotFound {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("producer returned unexpected status %s for %q: %s", resp.Status, targetURL, string(respBody))
	}

	return nil
}
