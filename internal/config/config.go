package config

import (
	"fmt"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	StageDevelopment = "development"
	StageProduction  = "production"
	StageEnvVar      = "STAGE"
	namespaceEnvVar  = "NAMESPACE"
	logLevelEnvVar   = "LOG_LEVEL"
)

var log = ctrl.Log.WithName("config")
var Stage = StageProduction

func IsStageDevelopment() bool {
	return Stage == StageDevelopment
}

type OperatorConfig struct {
	Namespace string

	ControllerOptions ctrl.Options
}

func NewOperatorConfig(scheme *runtime.Scheme) (*OperatorConfig, error) {
	configureStage()

	namespace, err := GetNamespace()
	if err != nil {
		return nil, fmt.Errorf("failed to read namespace: %w", err)
	}

	log.Info(fmt.Sprintf("deploying the service-account-operator in namespace %s", namespace))

	return &OperatorConfig{
		Namespace:         namespace,
		ControllerOptions: getControllerOptions(scheme, namespace),
	}, nil
}

func configureStage() {
	var err error
	Stage, err = getEnvVar(StageEnvVar)
	if err != nil {
		log.Error(err, "error reading stage environment variable, using production")
		Stage = StageProduction
	}

	if IsStageDevelopment() {
		log.Info("starting in development mode")
	}
}

func GetLogLevel() (string, error) {
	logLevel, err := getEnvVar(logLevelEnvVar)
	if err != nil {
		return "", fmt.Errorf("failed to get env var [%s]: %w", logLevelEnvVar, err)
	}

	return logLevel, nil
}

func GetNamespace() (string, error) {
	namespace, err := getEnvVar(namespaceEnvVar)
	if err != nil {
		return "", fmt.Errorf("failed to get env var [%s]: %w", namespaceEnvVar, err)
	}

	return namespace, nil
}

func getEnvVar(name string) (string, error) {
	env, found := os.LookupEnv(name)
	if !found {
		return "", fmt.Errorf("environment variable %s must be set", name)
	}

	return env, nil
}
