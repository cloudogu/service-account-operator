package config

import (
	"testing"

	"github.com/go-logr/logr"
	uberzap "go.uber.org/zap"
)

func TestConfigureLogger(t *testing.T) {
	t.Run("configures controller-runtime logger with computed zap options", func(t *testing.T) {
		t.Setenv(logLevelEnvVar, "debug")

		oldStage := Stage
		oldSetLogger := setLogger
		t.Cleanup(func() {
			Stage = oldStage
			setLogger = oldSetLogger
		})
		Stage = StageDevelopment

		called := false
		setLogger = func(logger logr.Logger) {
			called = true
			if logger.GetSink() == nil {
				t.Fatal("logger sink is nil")
			}
		}

		ConfigureLogger()

		if !called {
			t.Fatal("setLogger was not called")
		}
	})
}

func TestGetZapOptions(t *testing.T) {
	t.Run("returns debug level and development mode when configured", func(t *testing.T) {
		t.Setenv(logLevelEnvVar, "debug")

		oldStage := Stage
		t.Cleanup(func() {
			Stage = oldStage
		})
		Stage = StageDevelopment

		options := getZapOptions()

		if !options.Level.Enabled(uberzap.DebugLevel) {
			t.Fatalf("Level does not enable %v", uberzap.DebugLevel)
		}
		if !options.Level.Enabled(uberzap.InfoLevel) {
			t.Fatalf("Level does not enable %v", uberzap.InfoLevel)
		}
		if !options.Development {
			t.Fatal("Development = false, want true")
		}
	})

	t.Run("returns info level in production when LOG_LEVEL is not set", func(t *testing.T) {
		overrideLookupEnvForTest(t, func(string) (string, bool) {
			return "", false
		})

		oldStage := Stage
		t.Cleanup(func() {
			Stage = oldStage
		})
		Stage = StageProduction

		options := getZapOptions()

		if options.Level.Enabled(uberzap.DebugLevel) {
			t.Fatalf("Level unexpectedly enables %v", uberzap.DebugLevel)
		}
		if !options.Level.Enabled(uberzap.InfoLevel) {
			t.Fatalf("Level does not enable %v", uberzap.InfoLevel)
		}
		if options.Development {
			t.Fatal("Development = true, want false")
		}
	})

	t.Run("returns info level when configured log level is invalid", func(t *testing.T) {
		t.Setenv(logLevelEnvVar, "invalid")

		oldStage := Stage
		t.Cleanup(func() {
			Stage = oldStage
		})
		Stage = StageDevelopment

		options := getZapOptions()

		if options.Level.Enabled(uberzap.DebugLevel) {
			t.Fatalf("Level unexpectedly enables %v", uberzap.DebugLevel)
		}
		if !options.Level.Enabled(uberzap.InfoLevel) {
			t.Fatalf("Level does not enable %v", uberzap.InfoLevel)
		}
		if !options.Development {
			t.Fatal("Development = false, want true")
		}
	})
}
