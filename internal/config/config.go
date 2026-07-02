package config

import (
	"fmt"
	"os"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	StageDevelopment                = "development"
	StageProduction                 = "production"
	StageEnvVar                     = "STAGE"
	namespaceEnvVar                 = "NAMESPACE"
	logLevelEnvVar                  = "LOG_LEVEL"
	deletionTimeoutEnvVar           = "DELETION_TIMEOUT"
	producerReconcileIntervalEnvVar = "PRODUCER_RECONCILE_INTERVAL"
)

var log = ctrl.Log.WithName("config")
var Stage = StageProduction

func isStageDevelopment() bool {
	return Stage == StageDevelopment
}

// OperatorConfig contains the runtime configuration required to start the operator.
type OperatorConfig struct {
	// Namespace contains the Kubernetes namespace watched by the operator cache.
	Namespace string

	// ControllerOptions contains the controller-runtime manager configuration.
	ControllerOptions ctrl.Options

	// DeletionTimeout is the time to wait for a resource to be deleted before giving up.
	// The default value is supplied by the values.yaml.
	DeletionTimeout time.Duration

	// ProducerReconcileInterval is the interval for periodic producer reconciliation.
	// Setting this to 0 disables periodic reconciliation.
	ProducerReconcileInterval time.Duration
}

// NewOperatorConfig builds the operator runtime configuration from environment and flags.
func NewOperatorConfig(scheme *runtime.Scheme) (*OperatorConfig, error) {
	configureStage()

	namespace, err := getNamespace()
	if err != nil {
		return nil, fmt.Errorf("failed to read namespace: %w", err)
	}

	log.Info(fmt.Sprintf("deploying the service-account-operator in namespace %s", namespace))

	deletionTimeout, err := getDeletionTimeout()
	if err != nil {
		return nil, fmt.Errorf("failed to read deletion timeout: %w", err)
	}
	log.Info(fmt.Sprintf("using deletion timeout %s to avoid hanging resources", deletionTimeout))

	producerReconcileInterval, err := getProducerReconcileInterval()
	if err != nil {
		return nil, fmt.Errorf("failed to read producer reconcile interval: %w", err)
	}
	log.Info(fmt.Sprintf("using producer reconcile interval %s to periodically reconcile producers", producerReconcileInterval))

	return &OperatorConfig{
		Namespace:                 namespace,
		ControllerOptions:         getControllerOptions(scheme, namespace),
		DeletionTimeout:           deletionTimeout,
		ProducerReconcileInterval: producerReconcileInterval,
	}, nil
}

func getDeletionTimeout() (time.Duration, error) {
	deletionTimeout, err := getEnvVar(deletionTimeoutEnvVar)
	if err != nil {
		return 0, fmt.Errorf("failed to get env var [%s]: %w", deletionTimeoutEnvVar, err)
	}

	deletionTimeoutDuration, err := time.ParseDuration(deletionTimeout)
	if err != nil {
		return 0, fmt.Errorf("failed to parse env var [%s] with value [%s]: %w", deletionTimeoutEnvVar, deletionTimeout, err)
	}

	return deletionTimeoutDuration, nil
}

func getProducerReconcileInterval() (time.Duration, error) {
	reconcileInterval, err := getEnvVar(producerReconcileIntervalEnvVar)
	if err != nil {
		return 0, fmt.Errorf("failed to get env var [%s]: %w", producerReconcileIntervalEnvVar, err)
	}

	reconcileIntervalDuration, err := time.ParseDuration(reconcileInterval)
	if err != nil {
		return 0, fmt.Errorf("failed to parse env var [%s] with value [%s]: %w", producerReconcileIntervalEnvVar, reconcileInterval, err)
	}

	return reconcileIntervalDuration, nil
}

func configureStage() {
	var err error
	Stage, err = getEnvVar(StageEnvVar)
	if err != nil {
		log.Error(err, "error reading stage environment variable, using production")
		Stage = StageProduction
	}

	if isStageDevelopment() {
		log.Info("starting in development mode")
	}
}

func getLogLevel() (string, error) {
	logLevel, err := getEnvVar(logLevelEnvVar)
	if err != nil {
		return "", fmt.Errorf("failed to get env var [%s]: %w", logLevelEnvVar, err)
	}

	return logLevel, nil
}

func getNamespace() (string, error) {
	namespace, err := getEnvVar(namespaceEnvVar)
	if err != nil {
		return "", fmt.Errorf("failed to get env var [%s]: %w", namespaceEnvVar, err)
	}

	return namespace, nil
}

func getEnvVar(name string) (string, error) {
	env, found := lookupEnv(name)
	if !found {
		return "", fmt.Errorf("environment variable %s must be set", name)
	}

	return env, nil
}

var lookupEnv = os.LookupEnv
