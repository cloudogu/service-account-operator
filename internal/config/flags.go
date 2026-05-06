package config

import "flag"

type Flags struct {
	// MetricsAddr contains the bind address for the metrics endpoint, for example ":8443" for HTTPS
	// or ":8080" for HTTP. Optional. Defaults to "0" to disable the metrics service.
	MetricsAddr string
	// MetricsCertPath contains the directory of the metrics server certificate files, for example
	// "/var/run/metrics-certificates". Optional. Defaults to an empty string.
	MetricsCertPath string
	// MetricsCertName contains the metrics server certificate file name, for example "tls.crt".
	// Optional. Defaults to "tls.crt".
	MetricsCertName string
	// MetricsCertKey contains the metrics server private key file name, for example "tls.key".
	// Optional. Defaults to "tls.key".
	MetricsCertKey string
	// WebhookCertPath contains the directory of the webhook certificate files, for example
	// "/var/run/webhook-certificates". Optional. Defaults to an empty string.
	WebhookCertPath string
	// WebhookCertName contains the webhook certificate file name, for example "tls.crt".
	// Optional. Defaults to "tls.crt".
	WebhookCertName string
	// WebhookCertKey contains the webhook private key file name, for example "tls.key".
	// Optional. Defaults to "tls.key".
	WebhookCertKey string
	// EnableLeaderElection enables controller-runtime leader election. Optional. Defaults to false.
	EnableLeaderElection bool
	// ProbeAddr contains the bind address for the health and readiness probe endpoint, for example
	// ":8081". Optional. Defaults to ":8081".
	ProbeAddr string
	// SecureMetrics enables HTTPS for the metrics endpoint when set to true. Optional. Defaults to
	// true.
	SecureMetrics bool
	// EnableHTTP2 enables HTTP/2 for the metrics and webhook servers when set to true. Optional.
	// Defaults to false.
	EnableHTTP2 bool
}

func parseFlags() Flags {
	cfg := Flags{}

	flag.StringVar(&cfg.MetricsAddr, "metrics-bind-address", "0", "Bind address for the metrics endpoint.")
	flag.StringVar(&cfg.ProbeAddr, "health-probe-bind-address", ":8081", "Bind address for the probe endpoint.")
	flag.BoolVar(&cfg.EnableLeaderElection, "leader-elect", false,
		"Enable leader election for the controller manager.")
	flag.BoolVar(&cfg.SecureMetrics, "metrics-secure", true,
		"Serve the metrics endpoint via HTTPS.")
	flag.StringVar(&cfg.WebhookCertPath, "webhook-cert-path", "", "Directory containing the webhook certificate files.")
	flag.StringVar(&cfg.WebhookCertName, "webhook-cert-name", "tls.crt", "Webhook certificate file name.")
	flag.StringVar(&cfg.WebhookCertKey, "webhook-cert-key", "tls.key", "Webhook private key file name.")
	flag.StringVar(&cfg.MetricsCertPath, "metrics-cert-path", "",
		"Directory containing the metrics server certificate files.")
	flag.StringVar(&cfg.MetricsCertName, "metrics-cert-name", "tls.crt", "Metrics server certificate file name.")
	flag.StringVar(&cfg.MetricsCertKey, "metrics-cert-key", "tls.key", "Metrics server private key file name.")
	flag.BoolVar(&cfg.EnableHTTP2, "enable-http2", false,
		"Enable HTTP/2 for the metrics and webhook servers.")

	flag.Parse()

	return cfg
}
