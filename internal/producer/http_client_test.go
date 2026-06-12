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
		creds, err := client.Create(context.Background(), "grafana", Params{"--verbose"})

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
		creds, err := client.Create(context.Background(), "consumer", nil)

		require.NoError(t, err)
		assert.Equal(t, "abc", creds["apiKey"])
	})

	t.Run("should return error when producer returns non-201 status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "bad request", http.StatusBadRequest)
		}))
		defer server.Close()

		client := NewHTTPClient(server.URL, "key")
		_, err := client.Create(context.Background(), "consumer", nil)

		require.Error(t, err)
	})

	t.Run("should return error when server is unreachable", func(t *testing.T) {
		client := NewHTTPClient("http://127.0.0.1:1", "key")
		_, err := client.Create(context.Background(), "consumer", nil)

		require.Error(t, err)
	})

	t.Run("should return error for invalid endpoint URL", func(t *testing.T) {
		client := NewHTTPClient("://invalid", "key")
		_, err := client.Create(context.Background(), "consumer", nil)

		require.Error(t, err)
	})

	t.Run("should return error when producer response body is not valid JSON", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte("not-json"))
		}))
		defer server.Close()

		client := NewHTTPClient(server.URL, "key")
		_, err := client.Create(context.Background(), "consumer", nil)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to decode producer response")
	})
}

func TestHTTPClient_Update(t *testing.T) {
	t.Run("should panic because Update is not yet implemented", func(t *testing.T) {
		client := NewHTTPClient("http://example.com", "key")
		assert.Panics(t, func() {
			_, _ = client.Update(context.Background(), "consumer", nil)
		})
	})
}

func TestHTTPClient_Ready(t *testing.T) {
	t.Run("should send HEAD with api key and succeed on any HTTP response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodHead, r.Method)
			assert.Equal(t, "test-api-key", r.Header.Get(apiKeyHeader))
			w.WriteHeader(http.StatusMethodNotAllowed) // any HTTP response counts as reachable
		}))
		defer server.Close()

		client := NewHTTPClient(server.URL, "test-api-key")
		require.NoError(t, client.Ready(context.Background()))
	})

	t.Run("should return error when endpoint is unreachable", func(t *testing.T) {
		// Port 1 on loopback is not listening, so the connection is refused immediately.
		client := NewHTTPClient("http://127.0.0.1:1", "key")
		require.Error(t, client.Ready(context.Background()))
	})

	t.Run("should return error for invalid endpoint URL", func(t *testing.T) {
		client := NewHTTPClient("://invalid", "key")
		require.Error(t, client.Ready(context.Background()))
	})
}

func TestHTTPClient_Delete(t *testing.T) {
	t.Run("should send DELETE request and succeed on 200", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodDelete, r.Method)
			assert.Equal(t, "/grafana", r.URL.Path)
			assert.Equal(t, "test-api-key", r.Header.Get(apiKeyHeader))
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		client := NewHTTPClient(server.URL, "test-api-key")
		require.NoError(t, client.Delete(context.Background(), "grafana"))
	})

	t.Run("should succeed on 204 No Content", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
		defer server.Close()

		client := NewHTTPClient(server.URL, "key")
		require.NoError(t, client.Delete(context.Background(), "consumer"))
	})

	t.Run("should treat 404 as success since the account is already gone", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		client := NewHTTPClient(server.URL, "key")
		require.NoError(t, client.Delete(context.Background(), "consumer"))
	})

	t.Run("should return error on unexpected status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}))
		defer server.Close()

		client := NewHTTPClient(server.URL, "key")
		require.Error(t, client.Delete(context.Background(), "consumer"))
	})

	t.Run("should return error when endpoint is unreachable", func(t *testing.T) {
		client := NewHTTPClient("http://127.0.0.1:1", "key")
		require.Error(t, client.Delete(context.Background(), "consumer"))
	})

	t.Run("should return error for invalid endpoint URL", func(t *testing.T) {
		client := NewHTTPClient("http://[invalid", "key")
		require.Error(t, client.Delete(context.Background(), "consumer"))
	})
}
