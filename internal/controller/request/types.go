package request

import (
	"context"

	serviceaccountv2 "github.com/cloudogu/k8s-serviceaccount-lib/v2/api/v2"
	"github.com/cloudogu/service-account-operator/internal/controller/request/cron"
	"github.com/cloudogu/service-account-operator/internal/producer"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// secretManager manages the Kubernetes Secret that holds a service account's credentials.
type secretManager interface {
	Exists(ctx context.Context, sare *serviceaccountv2.ServiceAccountRequest) (exists bool, secretName string, err error)
	CreateOrUpdate(ctx context.Context, sare *serviceaccountv2.ServiceAccountRequest, credentials map[string]string) (string, error)
	Delete(ctx context.Context, sare *serviceaccountv2.ServiceAccountRequest) error
}

// producerClientFactory builds an HTTPClient for a given ServiceAccountProducer,
// resolving the API key from the referenced Kubernetes Secret.
type producerClientFactory interface {
	NewForProducer(ctx context.Context, namespace string, sapr *serviceaccountv2.ServiceAccountProducer) (producer.ServiceAccountClient, error)
}

type k8sClient interface {
	client.Client
}

// serviceAccountClient manages service accounts on a specific producer.
// Defined here for mock generation
type serviceAccountClient interface { //nolint:unused
	producer.ServiceAccountClient
}

//nolint:unused
type statusClient interface {
	client.SubResourceWriter
}

type eventRecorder interface {
	events.EventRecorder
}

type taskRunnerFactory interface {
	// New creates a new instance of a
	New(ctx context.Context, expr string, jobClosure cron.JobFunc) (cron.TaskRunner, error)
}

//nolint:unused
type taskRunner interface {
	// Run runs the provided task
	Run()
	// Stop interrupts the provided task.
	Stop()
	// Stopped returns a channel indicating the runner was stopped.
	Stopped() chan struct{}
}
