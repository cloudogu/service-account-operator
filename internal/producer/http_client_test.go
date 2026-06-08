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
			var req createRequestBody
			if err := json.Unmarshal(body, &req); err != nil {
				t.Errorf("failed to decode request body: %v", err)
			}
			if req.Params.Options["role"][0] != "admin" {
				t.Errorf("params not passed correctly: %+v", req.Params)
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(createResponseBody{
				Credentials: map[string]string{"username": "grafana-user", "password": "secret"},
			})
		}))
		defer server.Close()

		client := NewHTTPClient(server.URL, "test-api-key")
		params := CreateParams{
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

	t.Run("should send empty params when CreateParams is zero value", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			var req createRequestBody
			_ = json.Unmarshal(body, &req)
			if len(req.Params.Options) != 0 || len(req.Params.Args) != 0 {
				t.Errorf("expected empty params in request body, got %+v", req.Params)
			}

			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(createResponseBody{Credentials: map[string]string{"apiKey": "abc"}})
		}))
		defer server.Close()

		client := NewHTTPClient(server.URL, "key")
		creds, err := client.Create(context.Background(), "consumer", CreateParams{})
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
		_, err := client.Create(context.Background(), "consumer", CreateParams{})
		if err == nil {
			t.Fatal("Create() expected error for non-201 response")
		}
	})

	t.Run("should return error when server is unreachable", func(t *testing.T) {
		client := NewHTTPClient("http://127.0.0.1:1", "key")
		_, err := client.Create(context.Background(), "consumer", CreateParams{})
		if err == nil {
			t.Fatal("Create() expected error for unreachable server")
		}
	})

	t.Run("should return error for invalid endpoint URL", func(t *testing.T) {
		client := NewHTTPClient("://invalid", "key")
		_, err := client.Create(context.Background(), "consumer", CreateParams{})
		if err == nil {
			t.Fatal("Create() expected error for invalid URL")
		}
	})
}
