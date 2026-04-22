package config

import (
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/mock"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestNewOperatorConfig(t *testing.T) {
	testScheme := runtime.NewScheme()

	t.Run("should use development stage and fail to get namespace", func(t *testing.T) {
		resetFlagStateForTest(t, nil)
		overrideLookupEnvForTest(t, func(key string) (string, bool) {
			switch key {
			case StageEnvVar:
				return StageDevelopment, true
			default:
				return "", false
			}
		})

		oldStage := Stage
		oldLog := log
		t.Cleanup(func() {
			Stage = oldStage
			log = oldLog
		})

		logMock := newMockLogSink(t)
		logMock.EXPECT().Init(mock.Anything).Return()
		logMock.EXPECT().Enabled(0).Return(true).Maybe()
		logMock.EXPECT().Info(0, "starting in development mode").Return()
		log = logr.New(logMock)

		actual, err := NewOperatorConfig(testScheme)

		if err == nil {
			t.Fatal("NewOperatorConfig() expected error")
		}
		if actual != nil {
			t.Fatalf("NewOperatorConfig() = %#v, want nil", actual)
		}
		if got, want := err.Error(), "failed to read namespace: failed to get env var [NAMESPACE]: environment variable NAMESPACE must be set"; got != want {
			t.Fatalf("NewOperatorConfig() error = %q, want %q", got, want)
		}
		if Stage != StageDevelopment {
			t.Fatalf("Stage = %q, want %q", Stage, StageDevelopment)
		}
	})

	t.Run("should use configured namespace and return controller options", func(t *testing.T) {
		resetFlagStateForTest(t, []string{
			"--metrics-bind-address=:9443",
			"--health-probe-bind-address=:18081",
			"--leader-elect=true",
			"--metrics-secure=false",
			"--enable-http2=true",
		})
		t.Setenv(StageEnvVar, StageDevelopment)
		t.Setenv(namespaceEnvVar, "ecosystem")

		oldStage := Stage
		oldLog := log
		t.Cleanup(func() {
			Stage = oldStage
			log = oldLog
		})

		logMock := newMockLogSink(t)
		logMock.EXPECT().Init(mock.Anything).Return()
		logMock.EXPECT().Enabled(0).Return(true).Maybe()
		logMock.EXPECT().Info(0, "starting in development mode").Return()
		logMock.EXPECT().Info(0, "deploying the service-account-operator in namespace ecosystem").Return()
		log = logr.New(logMock)

		actual, err := NewOperatorConfig(testScheme)
		if err != nil {
			t.Fatalf("NewOperatorConfig() returned error: %v", err)
		}
		if actual == nil {
			t.Fatal("NewOperatorConfig() returned nil config")
		}
		if actual.Namespace != "ecosystem" {
			t.Fatalf("Namespace = %q, want %q", actual.Namespace, "ecosystem")
		}
		if actual.ControllerOptions.HealthProbeBindAddress != ":18081" {
			t.Fatalf("HealthProbeBindAddress = %q, want %q", actual.ControllerOptions.HealthProbeBindAddress, ":18081")
		}
		if !actual.ControllerOptions.LeaderElection {
			t.Fatal("LeaderElection = false, want true")
		}
		if Stage != StageDevelopment {
			t.Fatalf("Stage = %q, want %q", Stage, StageDevelopment)
		}
	})
}

func TestIsStageDevelopment(t *testing.T) {
	oldStage := Stage
	t.Cleanup(func() {
		Stage = oldStage
	})

	Stage = StageDevelopment
	if !IsStageDevelopment() {
		t.Fatal("IsStageDevelopment() = false, want true")
	}

	Stage = StageProduction
	if IsStageDevelopment() {
		t.Fatal("IsStageDevelopment() = true, want false")
	}
}

func TestGetLogLevel(t *testing.T) {
	t.Run("returns error when LOG_LEVEL is not set", func(t *testing.T) {
		overrideLookupEnvForTest(t, func(string) (string, bool) {
			return "", false
		})

		got, err := GetLogLevel()
		if err == nil {
			t.Fatal("GetLogLevel() expected error")
		}
		if got != "" {
			t.Fatalf("GetLogLevel() = %q, want empty string", got)
		}
	})

	t.Run("returns configured value when LOG_LEVEL is set", func(t *testing.T) {
		t.Setenv(logLevelEnvVar, "debug")

		got, err := GetLogLevel()
		if err != nil {
			t.Fatalf("GetLogLevel() returned error: %v", err)
		}
		if got != "debug" {
			t.Fatalf("GetLogLevel() = %q, want %q", got, "debug")
		}
	})
}

func TestGetNamespace(t *testing.T) {
	t.Run("returns error when NAMESPACE is not set", func(t *testing.T) {
		overrideLookupEnvForTest(t, func(string) (string, bool) {
			return "", false
		})

		got, err := GetNamespace()
		if err == nil {
			t.Fatal("GetNamespace() expected error")
		}
		if got != "" {
			t.Fatalf("GetNamespace() = %q, want empty string", got)
		}
	})

	t.Run("returns configured value when NAMESPACE is set", func(t *testing.T) {
		t.Setenv(namespaceEnvVar, "cloudogu")

		got, err := GetNamespace()
		if err != nil {
			t.Fatalf("GetNamespace() returned error: %v", err)
		}
		if got != "cloudogu" {
			t.Fatalf("GetNamespace() = %q, want %q", got, "cloudogu")
		}
	})
}

func TestConfigureStage(t *testing.T) {
	t.Run("should set stage to development", func(t *testing.T) {
		t.Setenv(StageEnvVar, StageDevelopment)

		oldStage := Stage
		oldLog := log
		t.Cleanup(func() {
			Stage = oldStage
			log = oldLog
		})

		logMock := newMockLogSink(t)
		logMock.EXPECT().Init(mock.Anything).Return()
		logMock.EXPECT().Enabled(0).Return(true).Maybe()
		logMock.EXPECT().Info(0, "starting in development mode").Return()
		log = logr.New(logMock)

		configureStage()

		if Stage != StageDevelopment {
			t.Fatalf("Stage = %q, want %q", Stage, StageDevelopment)
		}
	})

	t.Run("should set stage to production when configured as production", func(t *testing.T) {
		t.Setenv(StageEnvVar, StageProduction)

		oldStage := Stage
		oldLog := log
		t.Cleanup(func() {
			Stage = oldStage
			log = oldLog
		})

		logMock := newMockLogSink(t)
		logMock.EXPECT().Init(mock.Anything).Return()
		logMock.EXPECT().Enabled(0).Return(true).Maybe()
		log = logr.New(logMock)

		configureStage()

		if Stage != StageProduction {
			t.Fatalf("Stage = %q, want %q", Stage, StageProduction)
		}
	})

	t.Run("should fall back to production when stage env is missing", func(t *testing.T) {
		overrideLookupEnvForTest(t, func(key string) (string, bool) {
			if key == StageEnvVar {
				return "", false
			}
			return lookupEnv(key)
		})

		oldStage := Stage
		oldLog := log
		t.Cleanup(func() {
			Stage = oldStage
			log = oldLog
		})

		logMock := newMockLogSink(t)
		logMock.EXPECT().Init(mock.Anything).Return()
		logMock.EXPECT().Enabled(0).Return(true).Maybe()
		logMock.EXPECT().Error(mock.Anything, "error reading stage environment variable, using production").Return()
		log = logr.New(logMock)

		Stage = StageDevelopment
		configureStage()

		if Stage != StageProduction {
			t.Fatalf("Stage = %q, want %q", Stage, StageProduction)
		}
	})
}

func TestGetEnvVar(t *testing.T) {
	t.Run("returns error when env var is missing", func(t *testing.T) {
		overrideLookupEnvForTest(t, func(string) (string, bool) {
			return "", false
		})

		got, err := getEnvVar("MISSING_ENV")
		if err == nil {
			t.Fatal("getEnvVar() expected error")
		}
		if got != "" {
			t.Fatalf("getEnvVar() = %q, want empty string", got)
		}
	})

	t.Run("returns value when env var is set", func(t *testing.T) {
		overrideLookupEnvForTest(t, func(name string) (string, bool) {
			if name == "EXAMPLE_ENV" {
				return "example", true
			}
			return "", false
		})

		got, err := getEnvVar("EXAMPLE_ENV")
		if err != nil {
			t.Fatalf("getEnvVar() returned error: %v", err)
		}
		if got != "example" {
			t.Fatalf("getEnvVar() = %q, want %q", got, "example")
		}
	})
}

func overrideLookupEnvForTest(t *testing.T, fn func(string) (string, bool)) {
	t.Helper()

	oldLookupEnv := lookupEnv
	lookupEnv = fn
	t.Cleanup(func() {
		lookupEnv = oldLookupEnv
	})
}
