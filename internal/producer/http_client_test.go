package producer

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPClient_Create(t *testing.T) {
	t.Run("should send PUT request and return credentials on success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPut {
				t.Errorf("method = %q, want PUT", r.Method)
			}
			if r.URL.Path != "/grafana" {
				t.Errorf("path = %q, want /grafana", r.URL.Path)
			}
			if got := r.Header.Get(apiKeyHeader); got != "test-api-key" {
				t.Errorf("%s header = %q, want %q", apiKeyHeader, got, "test-api-key")
			}
			if got := r.Header.Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", got)
			}

			body, _ := io.ReadAll(r.Body)
			var req credentialRequestBody
			if err := json.Unmarshal(body, &req); err != nil {
				t.Errorf("failed to decode request body: %v", err)
			}
			if req.Params.Options["role"][0] != "admin" {
				t.Errorf("params not passed correctly: %+v", req.Params)
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(credentialResponseBody{
				Credentials: map[string]string{"username": "grafana-user", "password": "secret"},
			})
		}))
		defer server.Close()

		client := NewHTTPClient(server.URL, "test-api-key")
		params := Params{
			Options: map[string][]string{"role": {"admin"}},
		}

		creds, err := client.Create(context.Background(), "grafana", params)
		if err != nil {
			t.Fatalf("Create() returned error: %v", err)
		}
		if creds["username"] != "grafana-user" {
			t.Errorf("username = %q, want %q", creds["username"], "grafana-user")
		}
		if creds["password"] != "secret" {
			t.Errorf("password = %q, want %q", creds["password"], "secret")
		}
	})

	t.Run("should send empty params when Params is zero value", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			var req credentialRequestBody
			_ = json.Unmarshal(body, &req)
			if len(req.Params.Options) != 0 || len(req.Params.Args) != 0 {
				t.Errorf("expected empty params in request body, got %+v", req.Params)
			}

			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(credentialResponseBody{Credentials: map[string]string{"apiKey": "abc"}})
		}))
		defer server.Close()

		client := NewHTTPClient(server.URL, "key")
		creds, err := client.Create(context.Background(), "consumer", Params{})
		if err != nil {
			t.Fatalf("Create() returned error: %v", err)
		}
		if creds["apiKey"] != "abc" {
			t.Errorf("apiKey = %q, want %q", creds["apiKey"], "abc")
		}
	})

	t.Run("should return error when producer returns non-201 status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "bad request", http.StatusBadRequest)
		}))
		defer server.Close()

		client := NewHTTPClient(server.URL, "key")
		_, err := client.Create(context.Background(), "consumer", Params{})
		if err == nil {
			t.Fatal("Create() expected error for non-201 response")
		}
	})

	t.Run("should return error when server is unreachable", func(t *testing.T) {
		client := NewHTTPClient("http://127.0.0.1:1", "key")
		_, err := client.Create(context.Background(), "consumer", Params{})
		if err == nil {
			t.Fatal("Create() expected error for unreachable server")
		}
	})

	t.Run("should return error for invalid endpoint URL", func(t *testing.T) {
		client := NewHTTPClient("://invalid", "key")
		_, err := client.Create(context.Background(), "consumer", Params{})
		if err == nil {
			t.Fatal("Create() expected error for invalid URL")
		}
	})
}

func TestHTTPClient_Update(t *testing.T) {
	t.Run("should send POST request and return refreshed credentials on success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("method = %q, want POST", r.Method)
			}
			if r.URL.Path != "/grafana" {
				t.Errorf("path = %q, want /grafana", r.URL.Path)
			}
			if got := r.Header.Get(apiKeyHeader); got != "test-api-key" {
				t.Errorf("%s header = %q, want %q", apiKeyHeader, got, "test-api-key")
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(credentialResponseBody{
				Credentials: map[string]string{"username": "grafana-user", "password": "rotated"},
			})
		}))
		defer server.Close()

		client := NewHTTPClient(server.URL, "test-api-key")
		creds, err := client.Update(context.Background(), "grafana", Params{})
		if err != nil {
			t.Fatalf("Update() returned error: %v", err)
		}
		if creds["password"] != "rotated" {
			t.Errorf("password = %q, want %q", creds["password"], "rotated")
		}
	})

	t.Run("should return error when producer returns non-200 status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "conflict", http.StatusConflict)
		}))
		defer server.Close()

		client := NewHTTPClient(server.URL, "key")
		_, err := client.Update(context.Background(), "consumer", Params{})
		if err == nil {
			t.Fatal("Update() expected error for non-200 response")
		}
	})
}

func TestHTTPClient_Delete(t *testing.T) {
	t.Run("should send DELETE request and succeed on 200", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodDelete {
				t.Errorf("method = %q, want DELETE", r.Method)
			}
			if r.URL.Path != "/grafana" {
				t.Errorf("path = %q, want /grafana", r.URL.Path)
			}
			if got := r.Header.Get(apiKeyHeader); got != "test-api-key" {
				t.Errorf("%s header = %q, want %q", apiKeyHeader, got, "test-api-key")
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		client := NewHTTPClient(server.URL, "test-api-key")
		if err := client.Delete(context.Background(), "grafana"); err != nil {
			t.Fatalf("Delete() returned error: %v", err)
		}
	})

	t.Run("should succeed on 204 No Content", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
		defer server.Close()

		client := NewHTTPClient(server.URL, "key")
		if err := client.Delete(context.Background(), "consumer"); err != nil {
			t.Fatalf("Delete() returned error: %v", err)
		}
	})

	t.Run("should treat 404 as success since the account is already gone", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		client := NewHTTPClient(server.URL, "key")
		if err := client.Delete(context.Background(), "consumer"); err != nil {
			t.Fatalf("Delete() returned error: %v", err)
		}
	})

	t.Run("should return error on unexpected status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}))
		defer server.Close()

		client := NewHTTPClient(server.URL, "key")
		if err := client.Delete(context.Background(), "consumer"); err == nil {
			t.Fatal("Delete() expected error for 500 response")
		}
	})
}
