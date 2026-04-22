package config

import (
	"crypto/tls"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

func getControllerOptions(scheme *runtime.Scheme, namespace string) ctrl.Options {
	flagsConfig := parseFlags()

	tlsOpts := createTLSOptions(flagsConfig)
	webhookServer := createWebhookServer(flagsConfig, tlsOpts)
	metricsOptions := createMetricsServerOptions(flagsConfig, tlsOpts)

	return ctrl.Options{
		Scheme:        scheme,
		Metrics:       metricsOptions,
		WebhookServer: webhookServer,
		Cache: cache.Options{
			DefaultNamespaces: map[string]cache.Config{
				namespace: {},
			},
		},
		HealthProbeBindAddress: flagsConfig.ProbeAddr,
		LeaderElection:         flagsConfig.EnableLeaderElection,
		LeaderElectionID:       "service-account-operator.k8s.cloudogu.com",
	}
}

func createTLSOptions(flagsConfig Flags) []func(*tls.Config) {
	var tlsOpts []func(*tls.Config)

	disableHTTP2 := func(c *tls.Config) {
		log.Info("disabling HTTP/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !flagsConfig.EnableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	return tlsOpts
}

func createWebhookServer(flagsConfig Flags, tlsOpts []func(*tls.Config)) webhook.Server {
	webhookServerOptions := webhook.Options{
		TLSOpts: tlsOpts,
	}

	if len(flagsConfig.WebhookCertPath) > 0 {
		log.Info("initializing webhook certificate watcher using provided certificates",
			"webhook-cert-path", flagsConfig.WebhookCertPath,
			"webhook-cert-name", flagsConfig.WebhookCertName,
			"webhook-cert-key", flagsConfig.WebhookCertKey)

		webhookServerOptions.CertDir = flagsConfig.WebhookCertPath
		webhookServerOptions.CertName = flagsConfig.WebhookCertName
		webhookServerOptions.KeyName = flagsConfig.WebhookCertKey
	}

	return webhook.NewServer(webhookServerOptions)
}

func createMetricsServerOptions(flagsConfig Flags, tlsOpts []func(*tls.Config)) metricsserver.Options {
	metricsOptions := metricsserver.Options{
		BindAddress:   flagsConfig.MetricsAddr,
		SecureServing: flagsConfig.SecureMetrics,
		TLSOpts:       tlsOpts,
	}

	if flagsConfig.SecureMetrics {
		metricsOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	if len(flagsConfig.MetricsCertPath) > 0 {
		log.Info("initializing metrics certificate watcher using provided certificates",
			"metrics-cert-path", flagsConfig.MetricsCertPath,
			"metrics-cert-name", flagsConfig.MetricsCertName,
			"metrics-cert-key", flagsConfig.MetricsCertKey)

		metricsOptions.CertDir = flagsConfig.MetricsCertPath
		metricsOptions.CertName = flagsConfig.MetricsCertName
		metricsOptions.KeyName = flagsConfig.MetricsCertKey
	}

	return metricsOptions
}
