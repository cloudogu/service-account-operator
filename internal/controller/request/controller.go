package request

import (
	"context"
	"errors"
	"fmt"
	"time"

	serviceaccountv2 "github.com/cloudogu/k8s-serviceaccount-lib/v2/api/v2"
	"github.com/cloudogu/service-account-operator/internal/config"
	"github.com/cloudogu/service-account-operator/internal/producer"
	sa "github.com/cloudogu/service-account-operator/internal/serviceaccount"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	finalizer = "k8s.cloudogu.com/service-account-request-finalizer"
)

// secretManager manages the Kubernetes Secret that holds a service account's credentials.
type secretManager interface {
	Exists(ctx context.Context, sare *serviceaccountv2.ServiceAccountRequest) (bool, error)
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

type StatusClient interface {
	client.SubResourceWriter
}

// Controller reconciles ServiceAccountRequest resources.
type Controller struct {
	client                k8sClient
	secretManager         secretManager
	producerClientFactory producerClientFactory
	operatorConfig        *config.OperatorConfig
}

func New(rtClient k8sClient, scheme *runtime.Scheme, operatorConfig *config.OperatorConfig) *Controller {
	return &Controller{
		client:                rtClient,
		secretManager:         sa.NewSecretManager(rtClient, scheme),
		producerClientFactory: producer.NewClientFactory(rtClient),
		operatorConfig:        operatorConfig,
	}
}

func (c *Controller) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)

	var sare serviceaccountv2.ServiceAccountRequest
	if err := c.client.Get(ctx, req.NamespacedName, &sare); client.IgnoreNotFound(err) != nil {
		logger.Error(err, "failed to get service account request")

		return ctrl.Result{}, err
	}

	if !sare.DeletionTimestamp.IsZero() {
		logger.Info("service account request needs to be deleted")

		return ctrl.Result{}, c.reconcileDelete(ctx, &sare)
	}

	if controllerutil.AddFinalizer(&sare, finalizer) {
		if err := c.client.Update(ctx, &sare); err != nil {
			logger.Error(err, "failed to add finalizer to service account request")

			return ctrl.Result{}, fmt.Errorf("failed to add finalizer to service account request %q: %w", sare.Name, err)
		}
		// Update refreshes sare's resourceVersion in place, so we continue reconciling in the same pass.
	}

	exists, err := c.secretManager.Exists(ctx, &sare)
	if err != nil {
		if errors.Is(err, sa.ErrSecretConflict) {
			return ctrl.Result{}, c.fail(ctx, &sare, err)
		}

		logger.Error(err, "failed to check if service account secret exists")

		return ctrl.Result{}, fmt.Errorf("failed to check if service account secret exists for %q: %w", sare.Name, err)
	}

	if exists {
		logger.Info("service account request needs to be updated")

		return c.reconcileUpdate(ctx, &sare)
	}

	logger.Info("service account request needs to be created")

	return c.reconcileCreate(ctx, &sare)
}

// TODO(open question): When an optional service account is created only after the consumer has
// already started (the consumer came up without it), the consumer may not pick up the new
// credentials until it is restarted. Decide whether this operator should trigger a consumer
// restart (e.g. annotate/roll the consumer's Deployment) or whether the consumer is expected to
// reload credentials on its own. Pending product decision before implementing.

func (c *Controller) reconcileCreate(ctx context.Context, sare *serviceaccountv2.ServiceAccountRequest) (ctrl.Result, error) {
	logger := logf.FromContext(ctx).WithValues("serviceAccountRequest", sare.Name)

	sapr, err := c.getProducer(ctx, sare.Namespace, sare.Spec.Producer)
	if err != nil {
		if apierrors.IsNotFound(err) {
			if sare.Spec.Optional {
				logger.Info("optional producer not found, skipping until producer is created", "producer", sare.Spec.Producer)
				return ctrl.Result{}, producerNotFound(ctx, c.client, sare, sare.Spec.Producer, err)
			}

			return ctrl.Result{}, fmt.Errorf("required producer %q not found: %w", sare.Spec.Producer, err)
		}

		return ctrl.Result{}, fmt.Errorf("failed to get producer %q: %w", sare.Spec.Producer, err)
	}

	saClient, err := c.getServiceAccountClient(ctx, sare, sapr)
	if err != nil {
		return ctrl.Result{}, c.fail(ctx, sare, fmt.Errorf("failed to build HTTP client for producer %q: %w", sapr.Name, err))
	}

	credentials, err := saClient.Create(ctx, qualifiedConsumer(sare), sare.Spec.Params)
	if err != nil {
		return ctrl.Result{}, c.fail(ctx, sare, fmt.Errorf("failed to create service account at producer %q: %w", sapr.Name, err))
	}

	secretName, err := c.secretManager.CreateOrUpdate(ctx, sare, credentials)
	if err != nil {
		return ctrl.Result{}, c.fail(ctx, sare, fmt.Errorf("failed to store credentials in Kubernetes secret: %w", err))
	}

	if err := serviceAccountReady(ctx, c.client, sare, secretName); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status after successful create for %q: %w", sare.Name, err)
	}

	return ctrl.Result{}, nil
}

func (c *Controller) getServiceAccountClient(ctx context.Context, sare *serviceaccountv2.ServiceAccountRequest, sapr *serviceaccountv2.ServiceAccountProducer) (serviceAccountClient, error) {
	saClient, err := c.producerClientFactory.NewForProducer(ctx, sare.Namespace, sapr)
	if err != nil {
		return nil, fmt.Errorf("failed to build service account client for producer %q: %w", sapr.Name, err)
	}

	return saClient, nil
}

// fail records a failed ServiceAccountReady condition and returns the original
// error so the reconcile is retried with backoff.
func (c *Controller) fail(ctx context.Context, sare *serviceaccountv2.ServiceAccountRequest, err error) error {
	if patchErr := serviceAccountFailed(ctx, c.client, sare, err); patchErr != nil {
		logf.FromContext(ctx).Error(patchErr, "failed to update status conditions after reconcile error")
	}

	return err
}

func (c *Controller) reconcileDelete(ctx context.Context, sare *serviceaccountv2.ServiceAccountRequest) error {
	if !controllerutil.ContainsFinalizer(sare, finalizer) {
		return nil
	}

	if time.Since(sare.DeletionTimestamp.Time) > c.operatorConfig.DeletionTimeout {
		logf.FromContext(ctx).Info("deletion timeout reached, dropping finalizer to avoid hanging resource", "serviceAccountRequest", sare.Name)
		return c.removeFinalizer(ctx, sare)
	}

	if err := c.deleteServiceAccount(ctx, sare); err != nil {
		wrapErr := fmt.Errorf("failed to delete service account for %q: %w", sare.Name, err)
		return c.fail(ctx, sare, wrapErr)
	}

	return c.removeFinalizer(ctx, sare)
}

func (c *Controller) removeFinalizer(ctx context.Context, sare *serviceaccountv2.ServiceAccountRequest) error {
	if !controllerutil.ContainsFinalizer(sare, finalizer) {
		return nil
	}

	controllerutil.RemoveFinalizer(sare, finalizer)
	if err := c.client.Update(ctx, sare); err != nil {
		// Prevent resource not found error produced by informer cache lag.
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to remove finalizer from service account request %q: %w", sare.Name, err)
	}

	return nil
}

func (c *Controller) deleteServiceAccount(ctx context.Context, sare *serviceaccountv2.ServiceAccountRequest) error {
	logger := logf.FromContext(ctx)
	logger.Info("deleting service account", "serviceAccount", sare.Spec.Consumer)

	err := c.secretManager.Delete(ctx, sare)
	if err != nil {
		return fmt.Errorf("failed to delete secret for service account request %q: %w", sare.Name, err)
	}

	var sapr *serviceaccountv2.ServiceAccountProducer
	sapr, err = c.getProducer(ctx, sare.Namespace, sare.Spec.Producer)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("producer not found, skipping deletion of service account at producer", "producer", sare.Spec.Producer, "request", sare.Name)
			return nil
		}

		return fmt.Errorf("failed to get producer %q: %w", sare.Spec.Producer, err)
	}

	saClient, err := c.getServiceAccountClient(ctx, sare, sapr)
	if err != nil {
		return err
	}

	consumer := qualifiedConsumer(sare)
	exists, err := saClient.Exists(ctx, consumer)
	if err != nil {
		return fmt.Errorf("failed to check if service account %q exists at producer %q: %w", sare.Spec.Consumer, sapr.Name, err)
	}

	if !exists {
		logger.Info("service account not found at producer, skipping deletion", "serviceAccount", sare.Spec.Consumer, "producer", sapr.Name)
		return nil
	}

	err = saClient.Delete(ctx, consumer)
	if err != nil {
		return fmt.Errorf("failed to delete service account %q at producer %q: %w", sare.Spec.Consumer, sapr.Name, err)
	}
	logger.Info("deleted service account", "serviceAccount", sare.Spec.Consumer, "producer", sapr.Name)

	original := sapr.DeepCopy()
	sapr.Status.LastExecution = metav1.NewTime(time.Now())
	err = c.client.Status().Patch(ctx, sapr, client.MergeFrom(original))
	if err != nil {
		logger.Error(err, "failed to patch lastExecution status after successful delete", "serviceAccount", sare.Spec.Consumer, "producer", sapr.Name)
	}

	return nil
}

func (c *Controller) reconcileUpdate(ctx context.Context, sare *serviceaccountv2.ServiceAccountRequest) (ctrl.Result, error) {
	logf.FromContext(ctx).Info("update not yet implemented, skipping", "serviceAccountRequest", sare.Name)
	return ctrl.Result{}, nil
}

func (c *Controller) getProducer(ctx context.Context, namespace, producerName string) (*serviceaccountv2.ServiceAccountProducer, error) {
	var sapr serviceaccountv2.ServiceAccountProducer
	if err := c.client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: producerName}, &sapr); err != nil {
		return nil, err
	}

	return &sapr, nil
}

func (c *Controller) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&serviceaccountv2.ServiceAccountRequest{}).
		Watches(
			&serviceaccountv2.ServiceAccountProducer{},
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

	var sareList serviceaccountv2.ServiceAccountRequestList
	if err := c.client.List(ctx, &sareList, client.InNamespace(obj.GetNamespace())); err != nil {
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

// qualifiedConsumer returns a namespace-qualified consumer name to ensure uniqueness across namespaces.
// For example, a consumer "grafana" in namespace "ecosystem" becomes "grafana-ecosystem".
func qualifiedConsumer(sare *serviceaccountv2.ServiceAccountRequest) string {
	return sare.Spec.Consumer + "-" + sare.Namespace
}
