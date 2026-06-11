package producer

import (
	"context"
	"errors"
	"time"

	serviceaccountv1 "github.com/cloudogu/k8s-serviceaccount-lib/api/v1"
	producerclient "github.com/cloudogu/service-account-operator/internal/producer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// readinessCheckInterval is how often a producer is re-checked for readiness.
const readinessCheckInterval = 5 * time.Minute

// producerClientFactory builds a client for a given ServiceAccountProducer, resolving the API key
// from the referenced Kubernetes Secret.
type producerClientFactory interface {
	NewForProducer(ctx context.Context, namespace string, sapr *serviceaccountv1.ServiceAccountProducer) (producerclient.ServiceAccountClient, error)
}

// Controller reconciles ServiceAccountProducer resources.
type Controller struct {
	client        client.Client
	clientFactory producerClientFactory
}

func New(rtClient client.Client) *Controller {
	return &Controller{
		client:        rtClient,
		clientFactory: producerclient.NewProducerClientFactory(rtClient),
	}
}

// Reconcile checks whether the producer is ready, reflects the result in the Ready condition and
// re-checks periodically so transient connectivity problems recover without an external trigger.
func (c *Controller) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := logf.FromContext(ctx).WithValues("serviceAccountProducer", req.NamespacedName)

	var sapr serviceaccountv1.ServiceAccountProducer
	if err := c.client.Get(ctx, req.NamespacedName, &sapr); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	status := newStatusWriter(c.client, &sapr)

	if reason, checkErr := c.checkReady(ctx, &sapr); checkErr != nil {
		logger.Info("producer not ready", "reason", reason, "error", checkErr.Error())
		if err := status.notReady(ctx, reason, checkErr.Error()); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{RequeueAfter: readinessCheckInterval}, nil
	}

	if err := status.ready(ctx); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: readinessCheckInterval}, nil
}

// checkReady validates the producer and probes its endpoint. On failure it returns the matching
// ServiceAccountProducer condition reason together with the underlying error.
func (c *Controller) checkReady(ctx context.Context, sapr *serviceaccountv1.ServiceAccountProducer) (string, error) {
	if sapr.Spec.HTTP == nil {
		return serviceaccountv1.ConditionReadyReasonInvalidConfiguration, errors.New("producer has no HTTP spec configured")
	}

	// HTTP spec is present, so the only thing NewForProducer can fail on is resolving the auth secret.
	saClient, err := c.clientFactory.NewForProducer(ctx, sapr.Namespace, sapr)
	if err != nil {
		return serviceaccountv1.ConditionReadyReasonAuthSecretNotFound, err
	}

	if err := saClient.Ready(ctx); err != nil {
		return serviceaccountv1.ConditionReadyReasonConnectionFailed, err
	}

	return "", nil
}

func (c *Controller) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&serviceaccountv1.ServiceAccountProducer{}).
		Named("serviceaccountproducer").
		Complete(c)
}
