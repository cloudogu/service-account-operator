package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	serviceaccountv1 "github.com/cloudogu/k8s-serviceaccount-lib/api/v1"
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
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(serviceaccountv1.AddToScheme(scheme))
}

func main() {
	config.ConfigureLogger()

	cfg, err := config.NewOperatorConfig(scheme)
	if err != nil {
		setupLog.Error(err, "failed to create operator config")
		os.Exit(1)
	}

	if err := startManager(cfg); err != nil {
		setupLog.Error(err, "failed to start manager")
		os.Exit(1)
	}
}

func startManager(cfg *config.OperatorConfig) error {
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), cfg.ControllerOptions)
	if err != nil {
		return fmt.Errorf("failed to create manager: %w", err)
	}

	serviceAccountRequestController := request.New(mgr.GetClient())
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

	managerCtx, cancelManager := context.WithCancel(context.Background())
	defer cancelManager()

	managerErrCh := make(chan error, 1)
	go func() {
		managerErrCh <- mgr.Start(managerCtx)
	}()

	setupLog.Info("starting manager")

	select {
	case err := <-managerErrCh:
		if err != nil {
			return fmt.Errorf("failed to run manager: %w", err)
		}
		return nil
	case <-signalCtx.Done():
		setupLog.Info("shutdown signal received, stopping manager gracefully")
		cancelManager()
	}

	shutdownTimer := time.NewTimer(gracefulShutdownTimeout)
	defer shutdownTimer.Stop()

	select {
	case err := <-managerErrCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			return fmt.Errorf("manager stopped with an error during shutdown: %w", err)
		}
		setupLog.Info("manager stopped gracefully")
		return nil
	case <-shutdownTimer.C:
		return fmt.Errorf("graceful shutdown timed out after %s", gracefulShutdownTimeout)
	}
}
