package config

import (
	"fmt"

	"github.com/go-logr/logr"
	uberzap "go.uber.org/zap"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// logSink mirrors logr.LogSink so tests can generate a mock for the controller-runtime logger sink
// abstraction without depending on the concrete zap-backed implementation.
//
//nolint:unused
type logSink interface {
	logr.LogSink
}

var setLogger = ctrl.SetLogger

func ConfigureLogger() {
	setLogger(zap.New(zap.UseFlagOptions(new(getZapOptions()))))
}

func getZapOptions() zap.Options {
	var logLevel uberzap.AtomicLevel

	envLogLevel, err := getLogLevel()
	if err != nil {
		fmt.Printf("unable to get configured log level, using info level instead\n  %s\n", err.Error())
		logLevel = uberzap.NewAtomicLevelAt(uberzap.InfoLevel)
	} else {
		logLevel, err = uberzap.ParseAtomicLevel(envLogLevel)
		if err != nil {
			fmt.Printf("error parsing configured log level, using info level instead\n  %s\n", err.Error())
			logLevel = uberzap.NewAtomicLevelAt(uberzap.InfoLevel)
		}
	}

	return zap.Options{
		Development: isStageDevelopment(),
		Level:       logLevel,
	}
}
