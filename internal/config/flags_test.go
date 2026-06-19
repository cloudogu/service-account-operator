package config

import (
	"flag"
	"io"
	"os"
	"testing"
)

func TestParseFlags_DefaultValues(t *testing.T) {
	t.Run("returns default values when no command line flags are provided", func(t *testing.T) {
		resetFlagStateForTest(t, nil)

		cfg := parseFlags()

		if cfg.MetricsAddr != "0" {
			t.Fatalf("MetricsAddr = %q, want %q", cfg.MetricsAddr, "0")
		}
		if cfg.ProbeAddr != ":8081" {
			t.Fatalf("ProbeAddr = %q, want %q", cfg.ProbeAddr, ":8081")
		}
		if cfg.EnableLeaderElection {
			t.Fatal("EnableLeaderElection = true, want false")
		}
		if !cfg.SecureMetrics {
			t.Fatal("SecureMetrics = false, want true")
		}
		if cfg.WebhookCertPath != "" {
			t.Fatalf("WebhookCertPath = %q, want empty string", cfg.WebhookCertPath)
		}
		if cfg.WebhookCertName != "tls.crt" {
			t.Fatalf("WebhookCertName = %q, want %q", cfg.WebhookCertName, "tls.crt")
		}
		if cfg.WebhookCertKey != "tls.key" {
			t.Fatalf("WebhookCertKey = %q, want %q", cfg.WebhookCertKey, "tls.key")
		}
		if cfg.MetricsCertPath != "" {
			t.Fatalf("MetricsCertPath = %q, want empty string", cfg.MetricsCertPath)
		}
		if cfg.MetricsCertName != "tls.crt" {
			t.Fatalf("MetricsCertName = %q, want %q", cfg.MetricsCertName, "tls.crt")
		}
		if cfg.MetricsCertKey != "tls.key" {
			t.Fatalf("MetricsCertKey = %q, want %q", cfg.MetricsCertKey, "tls.key")
		}
		if cfg.EnableHTTP2 {
			t.Fatal("EnableHTTP2 = true, want false")
		}
	})
}

func TestParseFlags_UsesProvidedValues(t *testing.T) {
	t.Run("returns overridden values when command line flags are provided", func(t *testing.T) {
		resetFlagStateForTest(t, []string{
			"--metrics-bind-address=:8443",
			"--health-probe-bind-address=:18081",
			"--leader-elect=true",
			"--metrics-secure=false",
			"--webhook-cert-path=/tmp/webhook",
			"--webhook-cert-name=custom-webhook.crt",
			"--webhook-cert-key=custom-webhook.key",
			"--metrics-cert-path=/tmp/metrics",
			"--metrics-cert-name=custom-metrics.crt",
			"--metrics-cert-key=custom-metrics.key",
			"--enable-http2=true",
		})

		cfg := parseFlags()

		if cfg.MetricsAddr != ":8443" {
			t.Fatalf("MetricsAddr = %q, want %q", cfg.MetricsAddr, ":8443")
		}
		if cfg.ProbeAddr != ":18081" {
			t.Fatalf("ProbeAddr = %q, want %q", cfg.ProbeAddr, ":18081")
		}
		if !cfg.EnableLeaderElection {
			t.Fatal("EnableLeaderElection = false, want true")
		}
		if cfg.SecureMetrics {
			t.Fatal("SecureMetrics = true, want false")
		}
		if cfg.WebhookCertPath != "/tmp/webhook" {
			t.Fatalf("WebhookCertPath = %q, want %q", cfg.WebhookCertPath, "/tmp/webhook")
		}
		if cfg.WebhookCertName != "custom-webhook.crt" {
			t.Fatalf("WebhookCertName = %q, want %q", cfg.WebhookCertName, "custom-webhook.crt")
		}
		if cfg.WebhookCertKey != "custom-webhook.key" {
			t.Fatalf("WebhookCertKey = %q, want %q", cfg.WebhookCertKey, "custom-webhook.key")
		}
		if cfg.MetricsCertPath != "/tmp/metrics" {
			t.Fatalf("MetricsCertPath = %q, want %q", cfg.MetricsCertPath, "/tmp/metrics")
		}
		if cfg.MetricsCertName != "custom-metrics.crt" {
			t.Fatalf("MetricsCertName = %q, want %q", cfg.MetricsCertName, "custom-metrics.crt")
		}
		if cfg.MetricsCertKey != "custom-metrics.key" {
			t.Fatalf("MetricsCertKey = %q, want %q", cfg.MetricsCertKey, "custom-metrics.key")
		}
		if !cfg.EnableHTTP2 {
			t.Fatal("EnableHTTP2 = false, want true")
		}
	})
}

func resetFlagStateForTest(t *testing.T, args []string) {
	t.Helper()

	oldCommandLine := flag.CommandLine
	oldArgs := os.Args

	testFlagSet := flag.NewFlagSet("config-flags-test", flag.ContinueOnError)
	testFlagSet.SetOutput(io.Discard)
	flag.CommandLine = testFlagSet

	os.Args = append([]string{"config-flags-test"}, args...)

	t.Cleanup(func() {
		flag.CommandLine = oldCommandLine
		os.Args = oldArgs
	})
}
