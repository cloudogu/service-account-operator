package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	serviceaccountv2 "github.com/cloudogu/k8s-serviceaccount-lib/v2/api/v2"
	"github.com/cloudogu/service-account-operator/internal/config"
	"github.com/cloudogu/service-account-operator/internal/controller/producer"
	"github.com/cloudogu/service-account-operator/internal/controller/request"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure the manager can use them when loading kubeconfig entries.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
)

const gracefulShutdownTimeout = 15 * time.Second

var (
	scheme  = runtime.NewScheme()
	mainLog = ctrl.Log.WithName("main")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(serviceaccountv2.AddToScheme(scheme))
}

func main() {
	config.ConfigureLogger()

	cfg, err := config.NewOperatorConfig(scheme)
	if err != nil {
		mainLog.Error(err, "failed to create operator config")
		os.Exit(1)
	}

	if err := startManager(cfg); err != nil {
		mainLog.Error(err, "failed to start manager")
		os.Exit(1)
	}
}

func startManager(cfg *config.OperatorConfig) error {
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), cfg.ControllerOptions)
	if err != nil {
		return fmt.Errorf("failed to create manager: %w", err)
	}

	serviceAccountRequestController := request.New(mgr.GetClient(), mgr.GetScheme())
	if err := serviceAccountRequestController.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("failed to create service account request controller: %w", err)
	}

	serviceAccountProducerController := producer.New(mgr.GetClient())
	if err := serviceAccountProducerController.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("failed to create service account producer controller: %w", err)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return fmt.Errorf("failed to set up health check: %w", err)
	}

	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return fmt.Errorf("failed to set up ready check: %w", err)
	}

	return startManagerWithGracefulShutdown(mgr)
}

func startManagerWithGracefulShutdown(mgr ctrl.Manager) error {
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	managerErrCh := make(chan error, 1)
	go func() {
		managerErrCh <- mgr.Start(signalCtx)
	}()

	mainLog.Info("starting manager")

	return waitForGracefulShutdown(signalCtx, gracefulShutdownTimeout, managerErrCh)
}

func waitForGracefulShutdown(ctx context.Context, timeout time.Duration, managerErrCh <-chan error) error {
	select {
	case err := <-managerErrCh:
		if err != nil {
			return fmt.Errorf("failed to run manager: %w", err)
		}
		return nil
	case <-ctx.Done():
		mainLog.Info("shutdown signal received, stopping manager gracefully")
	}

	// After shutdown was requested, wait for the manager to exit on its own and only
	// fail if that graceful shutdown exceeds the configured timeout.
	shutdownTimer := time.NewTimer(timeout)
	defer shutdownTimer.Stop()

	select {
	case err := <-managerErrCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			return fmt.Errorf("manager stopped with an error during shutdown: %w", err)
		}
		mainLog.Info("manager stopped gracefully")
		return nil
	case <-shutdownTimer.C:
		return fmt.Errorf("graceful shutdown timed out after %s", timeout)
	}
}
