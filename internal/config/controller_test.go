package config

import (
	"crypto/tls"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

func TestCreateTLSOptions(t *testing.T) {
	t.Run("should add HTTP/2 disable option when HTTP/2 is disabled", func(t *testing.T) {
		tlsOpts := createTLSOptions(Flags{EnableHTTP2: false})
		if len(tlsOpts) != 1 {
			t.Fatalf("len(TLSOpts) = %d, want 1", len(tlsOpts))
		}

		tlsConfig := &tls.Config{NextProtos: []string{"h2"}}
		tlsOpts[0](tlsConfig)

		if len(tlsConfig.NextProtos) != 1 || tlsConfig.NextProtos[0] != "http/1.1" {
			t.Fatalf("NextProtos = %#v, want %#v", tlsConfig.NextProtos, []string{"http/1.1"})
		}
	})

	t.Run("should not add TLS option when HTTP/2 is enabled", func(t *testing.T) {
		tlsOpts := createTLSOptions(Flags{EnableHTTP2: true})
		if len(tlsOpts) != 0 {
			t.Fatalf("len(TLSOpts) = %d, want 0", len(tlsOpts))
		}
	})
}

func TestCreateWebhookServer(t *testing.T) {
	t.Run("uses provided webhook certificate settings and tls options", func(t *testing.T) {
		tlsOpts := createTLSOptions(Flags{EnableHTTP2: false})
		flagsConfig := Flags{
			WebhookCertPath: "/tmp/webhook-certs",
			WebhookCertName: "webhook.crt",
			WebhookCertKey:  "webhook.key",
		}

		server := createWebhookServer(flagsConfig, tlsOpts)

		defaultServer, ok := server.(*webhook.DefaultServer)
		if !ok {
			t.Fatalf("expected webhook server to be *webhook.DefaultServer, got %T", server)
		}
		if defaultServer.Options.CertDir != "/tmp/webhook-certs" {
			t.Fatalf("CertDir = %q, want %q", defaultServer.Options.CertDir, "/tmp/webhook-certs")
		}
		if defaultServer.Options.CertName != "webhook.crt" {
			t.Fatalf("CertName = %q, want %q", defaultServer.Options.CertName, "webhook.crt")
		}
		if defaultServer.Options.KeyName != "webhook.key" {
			t.Fatalf("KeyName = %q, want %q", defaultServer.Options.KeyName, "webhook.key")
		}
		if len(defaultServer.Options.TLSOpts) != 1 {
			t.Fatalf("len(TLSOpts) = %d, want 1", len(defaultServer.Options.TLSOpts))
		}
	})
}

func TestCreateMetricsServerOptions(t *testing.T) {
	t.Run("should configure secure metrics including filter provider and certs", func(t *testing.T) {
		tlsOpts := createTLSOptions(Flags{EnableHTTP2: false})
		flagsConfig := Flags{
			MetricsAddr:     ":8443",
			SecureMetrics:   true,
			MetricsCertPath: "/tmp/metrics-certs",
			MetricsCertName: "metrics.crt",
			MetricsCertKey:  "metrics.key",
		}

		opts := createMetricsServerOptions(flagsConfig, tlsOpts)

		if opts.BindAddress != ":8443" {
			t.Fatalf("BindAddress = %q, want %q", opts.BindAddress, ":8443")
		}
		if !opts.SecureServing {
			t.Fatal("SecureServing = false, want true")
		}
		if opts.FilterProvider == nil {
			t.Fatal("FilterProvider = nil, want non-nil")
		}
		if opts.CertDir != "/tmp/metrics-certs" {
			t.Fatalf("CertDir = %q, want %q", opts.CertDir, "/tmp/metrics-certs")
		}
		if opts.CertName != "metrics.crt" {
			t.Fatalf("CertName = %q, want %q", opts.CertName, "metrics.crt")
		}
		if opts.KeyName != "metrics.key" {
			t.Fatalf("KeyName = %q, want %q", opts.KeyName, "metrics.key")
		}
		if len(opts.TLSOpts) != 1 {
			t.Fatalf("len(TLSOpts) = %d, want 1", len(opts.TLSOpts))
		}
	})

	t.Run("should not configure auth filter for insecure metrics", func(t *testing.T) {
		opts := createMetricsServerOptions(Flags{
			MetricsAddr:   ":8080",
			SecureMetrics: false,
		}, nil)

		if opts.BindAddress != ":8080" {
			t.Fatalf("BindAddress = %q, want %q", opts.BindAddress, ":8080")
		}
		if opts.SecureServing {
			t.Fatal("SecureServing = true, want false")
		}
		if opts.FilterProvider != nil {
			t.Fatal("FilterProvider != nil, want nil")
		}
		if opts.CertDir != "" || opts.CertName != "" || opts.KeyName != "" {
			t.Fatalf("expected empty cert config, got dir=%q name=%q key=%q", opts.CertDir, opts.CertName, opts.KeyName)
		}
	})
}

func TestGetControllerOptions(t *testing.T) {
	t.Run("maps parsed flags and namespace into controller-runtime options", func(t *testing.T) {
		resetFlagStateForTest(t, []string{
			"--metrics-bind-address=:9443",
			"--health-probe-bind-address=:18081",
			"--leader-elect=true",
			"--metrics-secure=false",
			"--webhook-cert-path=/tmp/webhook-certs",
			"--webhook-cert-name=webhook.crt",
			"--webhook-cert-key=webhook.key",
			"--metrics-cert-path=/tmp/metrics-certs",
			"--metrics-cert-name=metrics.crt",
			"--metrics-cert-key=metrics.key",
			"--enable-http2=true",
		})

		scheme := runtime.NewScheme()
		namespace := "ecosystem"

		options := getControllerOptions(scheme, namespace)

		if options.Scheme != scheme {
			t.Fatal("Scheme was not propagated into controller options")
		}
		if options.HealthProbeBindAddress != ":18081" {
			t.Fatalf("HealthProbeBindAddress = %q, want %q", options.HealthProbeBindAddress, ":18081")
		}
		if !options.LeaderElection {
			t.Fatal("LeaderElection = false, want true")
		}
		if options.LeaderElectionID != "service-account-operator.k8s.cloudogu.com" {
			t.Fatalf("LeaderElectionID = %q, want %q", options.LeaderElectionID, "service-account-operator.k8s.cloudogu.com")
		}
		if _, exists := options.Cache.DefaultNamespaces[namespace]; !exists {
			t.Fatalf("expected namespace %q to be configured in cache options", namespace)
		}

		if options.Metrics.BindAddress != ":9443" {
			t.Fatalf("Metrics.BindAddress = %q, want %q", options.Metrics.BindAddress, ":9443")
		}
		if options.Metrics.SecureServing {
			t.Fatal("Metrics.SecureServing = true, want false")
		}
		if options.Metrics.FilterProvider != nil {
			t.Fatal("Metrics.FilterProvider != nil, want nil")
		}
		if len(options.Metrics.TLSOpts) != 0 {
			t.Fatalf("len(Metrics.TLSOpts) = %d, want 0", len(options.Metrics.TLSOpts))
		}
		if options.Metrics.CertDir != "/tmp/metrics-certs" {
			t.Fatalf("Metrics.CertDir = %q, want %q", options.Metrics.CertDir, "/tmp/metrics-certs")
		}
		if options.Metrics.CertName != "metrics.crt" {
			t.Fatalf("Metrics.CertName = %q, want %q", options.Metrics.CertName, "metrics.crt")
		}
		if options.Metrics.KeyName != "metrics.key" {
			t.Fatalf("Metrics.KeyName = %q, want %q", options.Metrics.KeyName, "metrics.key")
		}

		webhookServer, ok := options.WebhookServer.(*webhook.DefaultServer)
		if !ok {
			t.Fatalf("expected WebhookServer to be *webhook.DefaultServer, got %T", options.WebhookServer)
		}
		if webhookServer.Options.CertDir != "/tmp/webhook-certs" {
			t.Fatalf("Webhook.CertDir = %q, want %q", webhookServer.Options.CertDir, "/tmp/webhook-certs")
		}
		if webhookServer.Options.CertName != "webhook.crt" {
			t.Fatalf("Webhook.CertName = %q, want %q", webhookServer.Options.CertName, "webhook.crt")
		}
		if webhookServer.Options.KeyName != "webhook.key" {
			t.Fatalf("Webhook.KeyName = %q, want %q", webhookServer.Options.KeyName, "webhook.key")
		}
		if len(webhookServer.Options.TLSOpts) != 0 {
			t.Fatalf("len(Webhook.TLSOpts) = %d, want 0", len(webhookServer.Options.TLSOpts))
		}
	})
}
