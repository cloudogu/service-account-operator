package producer

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testCtx = context.Background()

func TestHTTPClient_Create(t *testing.T) {
	t.Run("should send POST with consumer and params in body and return credentials", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "/", r.URL.Path)
			assert.Equal(t, "test-api-key", r.Header.Get(apiKeyHeader))
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

			body, _ := io.ReadAll(r.Body)
			var req createRequestBody
			require.NoError(t, json.Unmarshal(body, &req))
			assert.Equal(t, "grafana", req.Consumer)
			assert.Equal(t, Params{"--verbose"}, req.Params)

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"username": "grafana-user", "password": "secret"})
		}))
		defer server.Close()

		client := NewHTTPClient(server.URL, "test-api-key")
		creds, err := client.Create(testCtx, "grafana", Params{"--verbose"})

		require.NoError(t, err)
		assert.Equal(t, "grafana-user", creds["username"])
		assert.Equal(t, "secret", creds["password"])
	})

	t.Run("should send empty params when Params is nil", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			var req createRequestBody
			_ = json.Unmarshal(body, &req)
			assert.Empty(t, req.Params)

			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"apiKey": "abc"})
		}))
		defer server.Close()

		client := NewHTTPClient(server.URL, "key")
		creds, err := client.Create(testCtx, "consumer", nil)

		require.NoError(t, err)
		assert.Equal(t, "abc", creds["apiKey"])
	})

	t.Run("should return error when producer returns non-201 status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "bad request", http.StatusBadRequest)
		}))
		defer server.Close()

		client := NewHTTPClient(server.URL, "key")
		_, err := client.Create(testCtx, "consumer", nil)

		require.Error(t, err)
		assert.ErrorContains(t, err, "producer returned unexpected status 400")
	})

	t.Run("should return actionable error on 401 Unauthorized", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer server.Close()

		client := NewHTTPClient(server.URL, "wrong-key")
		_, err := client.Create(testCtx, "consumer", nil)

		require.Error(t, err)
		assert.ErrorContains(t, err, "rejected the request with 401 — check the API key in the auth secret")
	})

	t.Run("should return error when server is unreachable", func(t *testing.T) {
		client := NewHTTPClient("http://127.0.0.1:1", "key")
		_, err := client.Create(testCtx, "consumer", nil)

		require.Error(t, err)
		assert.ErrorContains(t, err, "HTTP create-serviceaccount request to producer \"http://127.0.0.1:1\" failed:")
	})

	t.Run("should return error for invalid endpoint URL", func(t *testing.T) {
		client := NewHTTPClient("://invalid", "key")
		_, err := client.Create(testCtx, "consumer", nil)

		require.Error(t, err)
		assert.ErrorContains(t, err, "failed to create HTTP request: parse \"://invalid\": missing protocol scheme")
	})

	t.Run("should return error when producer response body is not valid JSON", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte("not-json"))
		}))
		defer server.Close()

		client := NewHTTPClient(server.URL, "key")
		_, err := client.Create(testCtx, "consumer", nil)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to decode producer response")
	})
}

func TestHTTPClient_Update(t *testing.T) {
	t.Run("should panic because Update is not yet implemented", func(t *testing.T) {
		client := NewHTTPClient("http://example.com", "key")
		assert.Panics(t, func() {
			_, _ = client.Update(testCtx, "consumer", nil)
		})
	})
}

func TestHTTPClient_Ready(t *testing.T) {
	t.Run("should send HEAD with api key and succeed on any non-5xx HTTP response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodHead, r.Method)
			assert.Equal(t, "test-api-key", r.Header.Get(apiKeyHeader))
			w.WriteHeader(http.StatusMethodNotAllowed) // any non-5xx response counts as reachable
		}))
		defer server.Close()

		client := NewHTTPClient(server.URL, "test-api-key")
		require.NoError(t, client.Ready(testCtx))
	})

	t.Run("should return error when endpoint is unreachable", func(t *testing.T) {
		// Port 1 on loopback is not listening, so the connection is refused immediately.
		client := NewHTTPClient("http://127.0.0.1:1", "key")
		actualErr := client.Ready(testCtx)

		require.Error(t, actualErr)
		assert.ErrorContains(t, actualErr, `endpoint "http://127.0.0.1:1" is not ready because it is unreachable`)
	})

	t.Run("should return error on 5xx response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer server.Close()

		client := NewHTTPClient(server.URL, "key")
		actualErr := client.Ready(testCtx)

		require.Error(t, actualErr)
		assert.ErrorContains(t, actualErr, "503")
	})

	t.Run("should return actionable error on 401 Unauthorized", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer server.Close()

		client := NewHTTPClient(server.URL, "wrong-key")
		actualErr := client.Ready(testCtx)

		require.Error(t, actualErr)
		assert.ErrorContains(t, actualErr, "401")
		assert.ErrorContains(t, actualErr, "API key")
	})

	t.Run("should return error for invalid endpoint URL", func(t *testing.T) {
		client := NewHTTPClient("://invalid", "key")
		actualErr := client.Ready(testCtx)

		require.Error(t, actualErr)
		assert.ErrorContains(t, actualErr, `failed to build request to check producer readiness for "://invalid"`)
	})
}

func TestHTTPClient_Delete(t *testing.T) {
	tests := []struct {
		name        string
		endpoint    string // overrides server URL — no server started when set
		statusCode  int
		wantErr     bool
		errContains []string
	}{
		{name: "200 OK", statusCode: http.StatusOK},
		{name: "204 No Content", statusCode: http.StatusNoContent},
		{name: "404 Not Found (account already gone)", statusCode: http.StatusNotFound},
		{name: "500 Internal Server Error", statusCode: http.StatusInternalServerError, wantErr: true, errContains: []string{"producer returned unexpected status 500"}},
		{name: "401 Unauthorized", statusCode: http.StatusUnauthorized, wantErr: true, errContains: []string{"rejected the request with 401", "API key"}},
		{name: "unreachable endpoint", endpoint: "http://127.0.0.1:1", wantErr: true, errContains: []string{"HTTP delete-serviceaccount request to producer"}},
		{name: "invalid endpoint URL", endpoint: "http://[invalid", wantErr: true, errContains: []string{"failed to build URL for producer endpoint"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			endpoint := tt.endpoint
			if endpoint == "" {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(tt.statusCode)
				}))
				defer server.Close()
				endpoint = server.URL
			}

			err := NewHTTPClient(endpoint, "key").Delete(testCtx, "consumer")

			if tt.wantErr {
				require.Error(t, err)
				for _, s := range tt.errContains {
					assert.ErrorContains(t, err, s)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}

	t.Run("should send DELETE to /<consumer> with api key header", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodDelete, r.Method)
			assert.Equal(t, "/grafana", r.URL.Path)
			assert.Equal(t, "test-api-key", r.Header.Get(apiKeyHeader))
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		require.NoError(t, NewHTTPClient(server.URL, "test-api-key").Delete(testCtx, "grafana"))
	})
}
