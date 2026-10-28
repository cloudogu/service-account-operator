package producer

import (
	"context"
	"errors"
	"fmt"
	"time"

	serviceaccountv2 "github.com/cloudogu/k8s-serviceaccount-lib/v2/api/v2"
	"github.com/cloudogu/service-account-operator/internal/config"
	producerclient "github.com/cloudogu/service-account-operator/internal/producer"
	networkingv1 "k8s.io/api/networking/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// serviceAccountClient manages service accounts on a specific producer.
// Defined here for mock generation.
type serviceAccountClient interface { //nolint:unused
	producerclient.ServiceAccountClient
}

type networkPolicyUpserter interface {
	UpsertNetworkPolicy(ctx context.Context, producer *serviceaccountv2.ServiceAccountProducer) error
}

// clientFactory builds a client for a given ServiceAccountProducer, resolving the API key
// from the referenced Kubernetes Secret.
type clientFactory interface {
	NewForProducer(ctx context.Context, namespace string, sapr *serviceaccountv2.ServiceAccountProducer) (producerclient.ServiceAccountClient, error)
}

// Controller reconciles ServiceAccountProducer resources.
type Controller struct {
	client            client.Client
	clientFactory     clientFactory
	reconcileInterval time.Duration
	netpolUpserter    networkPolicyUpserter
}

func New(rtClient client.Client, operatorConfig *config.OperatorConfig) *Controller {
	return &Controller{
		client:            rtClient,
		clientFactory:     producerclient.NewClientFactory(rtClient),
		reconcileInterval: operatorConfig.ProducerReconcileInterval,
		netpolUpserter: &defaultNetworkPolicyUpserter{
			netpolsEnabled: operatorConfig.NetworkPoliciesEnabled,
			k8sClient:      rtClient,
			selectorLabels: operatorConfig.SelectorLabels,
		},
	}
}

// Reconcile checks whether the producer is ready, reflects the result in the Ready condition and
// re-checks periodically so transient connectivity problems recover without an external trigger.
func (c *Controller) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := logf.FromContext(ctx).WithValues("serviceAccountProducer", req.NamespacedName)

	var sapr serviceaccountv2.ServiceAccountProducer
	if err := c.client.Get(ctx, req.NamespacedName, &sapr); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if upsertErr := c.netpolUpserter.UpsertNetworkPolicy(ctx, &sapr); upsertErr != nil {
		logger.Error(upsertErr, "failed to upsert network policy")
		if err := markNetpolFailed(ctx, c.client, &sapr, upsertErr.Error()); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to mark network policy as failed: %w", err)
		}

		return ctrl.Result{RequeueAfter: c.reconcileInterval}, nil
	}

	if err := markNetpolUpserted(ctx, c.client, &sapr); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to mark network policy as upserted: %w", err)
	}

	if reason, checkErr := c.checkReady(ctx, &sapr); checkErr != nil {
		logger.Info("producer not ready", "reason", reason, "error", checkErr.Error())
		if err := markNotReady(ctx, c.client, &sapr, reason, checkErr.Error()); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{RequeueAfter: c.reconcileInterval}, nil
	}

	if err := markReady(ctx, c.client, &sapr); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: c.reconcileInterval}, nil
}

// checkReady validates the producer and probes its endpoint. On failure it returns the matching
// ServiceAccountProducer condition reason together with the underlying error.
func (c *Controller) checkReady(ctx context.Context, sapr *serviceaccountv2.ServiceAccountProducer) (string, error) {
	if sapr.Spec.HTTP == nil {
		return serviceaccountv2.ConditionReadyReasonInvalidConfiguration, errors.New("producer has no HTTP spec configured")
	}

	// HTTP spec is present, so the only thing NewForProducer can fail on is resolving the auth secret.
	saClient, err := c.clientFactory.NewForProducer(ctx, sapr.Namespace, sapr)
	if err != nil {
		return serviceaccountv2.ConditionReadyReasonAuthSecretNotFound, err
	}

	if err := saClient.Ready(ctx); err != nil {
		return serviceaccountv2.ConditionReadyReasonConnectionFailed, err
	}

	return "", nil
}

func (c *Controller) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&serviceaccountv2.ServiceAccountProducer{}).
		Owns(&networkingv1.NetworkPolicy{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("serviceaccountproducer").
		Complete(c)
}
