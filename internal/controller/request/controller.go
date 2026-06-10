package request

import (
	"context"
	"fmt"

	serviceaccountv1 "github.com/cloudogu/k8s-serviceaccount-lib/api/v1"
	httpclient "github.com/cloudogu/service-account-operator/internal/producer"
	sa "github.com/cloudogu/service-account-operator/internal/serviceaccount"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const finalizer = "k8s.cloudogu.com/service-account-request-finalizer"

// secretManager manages the Kubernetes Secret that holds a service account's credentials.
type secretManager interface {
	Exists(ctx context.Context, sare *serviceaccountv1.ServiceAccountRequest) (bool, error)
	CreateOrUpdate(ctx context.Context, sare *serviceaccountv1.ServiceAccountRequest, credentials map[string]string) (string, error)
}

// producerClientFactory builds an HTTPClient for a given ServiceAccountProducer,
// resolving the API key from the referenced Kubernetes Secret.
type producerClientFactory interface {
	NewForProducer(ctx context.Context, namespace string, sapr *serviceaccountv1.ServiceAccountProducer) (httpclient.HTTPClient, error)
}

// Controller reconciles ServiceAccountRequest resources.
type Controller struct {
	// TODO no need to export the client
	client.Client
	secretManager         secretManager
	producerClientFactory producerClientFactory
}

func New(rtClient client.Client, scheme *runtime.Scheme) *Controller {
	return &Controller{
		Client:                rtClient,
		secretManager:         sa.NewSecretManager(rtClient, scheme),
		producerClientFactory: &defaultProducerClientFactory{rtClient: rtClient},
	}
}

func (c *Controller) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var sare serviceaccountv1.ServiceAccountRequest
	if err := c.Get(ctx, req.NamespacedName, &sare); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !sare.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, c.reconcileDelete(ctx, &sare)
	}

	if controllerutil.AddFinalizer(&sare, finalizer) {
		if err := c.Update(ctx, &sare); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer to service account request %q: %w", sare.Name, err)
		}
		// TODO This cannot work. Either continue the reconciliation or return result with requeue.
		return ctrl.Result{}, nil
	}

	exists, err := c.secretManager.Exists(ctx, &sare)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to check if service account secret exists for %q: %w", sare.Name, err)
	}

	if exists {
		return c.reconcileUpdate(ctx, &sare)
	}

	return c.reconcileCreate(ctx, &sare)
	// TODO Do we need to restart the consumer if the serviceaccount is optional?
}

func (c *Controller) reconcileCreate(ctx context.Context, sare *serviceaccountv1.ServiceAccountRequest) (ctrl.Result, error) {
	logger := logf.FromContext(ctx).WithValues("serviceAccountRequest", sare.Name)
	status := newStatusWriter(c.Client, sare)

	sapr, err := c.getProducer(ctx, sare.Namespace, sare.Spec.Producer)
	if err != nil {
		if apierrors.IsNotFound(err) {
			if sare.Spec.Optional {
				logger.Info("optional producer not found, skipping until producer is created", "producer", sare.Spec.Producer)
				return ctrl.Result{}, status.producerNotFound(ctx, sare.Spec.Producer)
			}

			return ctrl.Result{}, fmt.Errorf("required producer %q not found: %w", sare.Spec.Producer, err)
		}

		return ctrl.Result{}, fmt.Errorf("failed to get producer %q: %w", sare.Spec.Producer, err)
	}

	// TODO rename to serviceAccountClient
	httpClient, err := c.producerClientFactory.NewForProducer(ctx, sare.Namespace, sapr)
	if err != nil {
		return ctrl.Result{}, c.fail(ctx, status, fmt.Errorf("failed to build HTTP client for producer %q: %w", sapr.Name, err))
	}

	credentials, err := httpClient.Create(ctx, sare.Spec.Consumer, httpclient.NewParamsFromSpec(sare.Spec.Params))
	if err != nil {
		return ctrl.Result{}, c.fail(ctx, status, fmt.Errorf("failed to create service account at producer %q: %w", sapr.Name, err))
	}

	secretName, err := c.secretManager.CreateOrUpdate(ctx, sare, credentials)
	if err != nil {
		return ctrl.Result{}, c.fail(ctx, status, fmt.Errorf("failed to store credentials in Kubernetes secret: %w", err))
	}

	sare.Status.SecretRef = &serviceaccountv1.LocalSecretRef{Name: secretName}
	if err := status.producerReady(ctx); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status after successful create for %q: %w", sare.Name, err)
	}

	if err := status.serviceAccountReady(ctx); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status after successful create for %q: %w", sare.Name, err)
	}

	return ctrl.Result{}, nil
}

// fail records a failed ServiceAccountReady condition and returns the original
// error so the reconcile is retried with backoff.
func (c *Controller) fail(ctx context.Context, status *statusWriter, err error) error {
	if patchErr := status.serviceAccountFailed(ctx, err); patchErr != nil {
		logf.FromContext(ctx).Error(patchErr, "failed to update status conditions after reconcile error")
	}

	return err
}

func (c *Controller) reconcileDelete(ctx context.Context, sare *serviceaccountv1.ServiceAccountRequest) error {
	if !controllerutil.ContainsFinalizer(sare, finalizer) {
		return nil
	}

	// TODO Persistent error during service account deletion freezes the cr. This could be annoying during cleanups.
	if err := c.deleteServiceAccount(ctx, sare); err != nil {
		return err
	}
	controllerutil.RemoveFinalizer(sare, finalizer)
	if err := c.Update(ctx, sare); err != nil {
		return fmt.Errorf("failed to remove finalizer from service account request %q: %w", sare.Name, err)
	}

	return nil
}

func (c *Controller) deleteServiceAccount(ctx context.Context, sare *serviceaccountv1.ServiceAccountRequest) error {
	logf.FromContext(ctx).Info("delete not yet implemented, skipping", "serviceAccountRequest", sare.Name)
	return nil
}

func (c *Controller) reconcileUpdate(ctx context.Context, sare *serviceaccountv1.ServiceAccountRequest) (ctrl.Result, error) {
	logf.FromContext(ctx).Info("update not yet implemented, skipping", "serviceAccountRequest", sare.Name)
	return ctrl.Result{}, nil
}

func (c *Controller) getProducer(ctx context.Context, namespace, producerName string) (*serviceaccountv1.ServiceAccountProducer, error) {
	var sapr serviceaccountv1.ServiceAccountProducer
	if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: producerName}, &sapr); err != nil {
		return nil, err
	}

	return &sapr, nil
}

func (c *Controller) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&serviceaccountv1.ServiceAccountRequest{}).
		Watches(
			&serviceaccountv1.ServiceAccountProducer{},
			handler.EnqueueRequestsFromMapFunc(c.enqueueRequestsForProducer),
		).
		Named("serviceaccountrequest").
		Complete(c)
}

// enqueueRequestsForProducer maps a ServiceAccountProducer event to all SAREs in the same
// namespace that reference this producer, so optional SAREs are re-reconciled once their
// producer becomes available.
func (c *Controller) enqueueRequestsForProducer(ctx context.Context, obj client.Object) []reconcile.Request {
	logger := logf.FromContext(ctx).WithValues("producer", obj.GetName())

	var sareList serviceaccountv1.ServiceAccountRequestList
	if err := c.List(ctx, &sareList, client.InNamespace(obj.GetNamespace())); err != nil {
		logger.Error(err, "failed to list service account requests for producer event")
		return nil
	}

	var requests []reconcile.Request
	for _, sare := range sareList.Items {
		if sare.Spec.Producer == obj.GetName() {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: sare.Namespace, Name: sare.Name},
			})
		}
	}

	return requests
}
