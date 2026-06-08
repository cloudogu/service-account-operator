package request

import (
	"context"
	"fmt"

	serviceaccountv1 "github.com/cloudogu/k8s-serviceaccount-lib/api/v1"
	httpclient "github.com/cloudogu/service-account-operator/internal/producer"
	sa "github.com/cloudogu/service-account-operator/internal/serviceaccount"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
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

const finalizer = "k8s.cloudogu.com/service-account-request-finalizer"

// secretManager writes service account credentials to a Kubernetes Secret.
type secretManager interface {
	CreateOrUpdate(ctx context.Context, sare *serviceaccountv1.ServiceAccountRequest, credentials map[string]string) (string, error)
}

// producerClientFactory builds an HTTPClient for a given ServiceAccountProducer,
// resolving the API key from the referenced Kubernetes Secret.
type producerClientFactory interface {
	NewForProducer(ctx context.Context, namespace string, sapr *serviceaccountv1.ServiceAccountProducer) (httpclient.HTTPClient, error)
}

// Controller reconciles ServiceAccountRequest resources.
type Controller struct {
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
		return ctrl.Result{}, nil
	}

	targetSecretName := sare.Name
	if sare.Spec.SecretRef != nil && sare.Spec.SecretRef.Name != "" {
		targetSecretName = sare.Spec.SecretRef.Name
	}

	exists, err := c.secretExists(ctx, sare.Namespace, targetSecretName)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to check for existing secret %q: %w", targetSecretName, err)
	}
	if exists {
		return c.reconcileUpdate(ctx, &sare)
	}
	return c.reconcileCreate(ctx, &sare)
}

func (c *Controller) reconcileCreate(ctx context.Context, sare *serviceaccountv1.ServiceAccountRequest) (ctrl.Result, error) {
	logger := logf.FromContext(ctx).WithValues("serviceAccountRequest", sare.Name)
	sareBefore := sare.DeepCopy()

	sapr, err := c.getProducer(ctx, sare.Namespace, sare.Spec.Producer)
	if err != nil {
		if apierrors.IsNotFound(err) {
			if sare.Spec.Optional {
				logger.Info("optional producer not found, skipping until producer is created", "producer", sare.Spec.Producer)
				apimeta.SetStatusCondition(&sare.Status.Conditions, metav1.Condition{
					Type:               serviceaccountv1.ConditionTypeProducerReady,
					Status:             metav1.ConditionFalse,
					Reason:             serviceaccountv1.ConditionReasonProducerReadyProducerNotFound,
					Message:            fmt.Sprintf("optional producer %q not found", sare.Spec.Producer),
					ObservedGeneration: sare.Generation,
				})
				return ctrl.Result{}, c.Status().Patch(ctx, sare, client.MergeFrom(sareBefore))
			}
			return ctrl.Result{}, fmt.Errorf("required producer %q not found: %w", sare.Spec.Producer, err)
		}
		return ctrl.Result{}, fmt.Errorf("failed to get producer %q: %w", sare.Spec.Producer, err)
	}

	httpClient, err := c.producerClientFactory.NewForProducer(ctx, sare.Namespace, sapr)
	if err != nil {
		return ctrl.Result{}, c.failWithCondition(ctx, sare, sareBefore, err)
	}

	credentials, err := httpClient.Create(ctx, sare.Spec.Consumer, toCreateParams(sare.Spec.Params))
	if err != nil {
		return ctrl.Result{}, c.failWithCondition(ctx, sare, sareBefore,
			fmt.Errorf("failed to create service account at producer %q: %w", sapr.Name, err))
	}

	secretName, err := c.secretManager.CreateOrUpdate(ctx, sare, credentials)
	if err != nil {
		return ctrl.Result{}, c.failWithCondition(ctx, sare, sareBefore, err)
	}

	apimeta.SetStatusCondition(&sare.Status.Conditions, metav1.Condition{
		Type:               serviceaccountv1.ConditionTypeProducerReady,
		Status:             metav1.ConditionTrue,
		Reason:             serviceaccountv1.ConditionReasonProducerReadyProducerFound,
		ObservedGeneration: sare.Generation,
	})
	apimeta.SetStatusCondition(&sare.Status.Conditions, metav1.Condition{
		Type:               serviceaccountv1.ConditionTypeServiceAccountReady,
		Status:             metav1.ConditionTrue,
		Reason:             serviceaccountv1.ConditionReasonServiceAccountReadyCreated,
		ObservedGeneration: sare.Generation,
	})
	sare.Status.SecretRef = &serviceaccountv1.LocalSecretRef{Name: secretName}
	return ctrl.Result{}, c.Status().Patch(ctx, sare, client.MergeFrom(sareBefore))
}

func (c *Controller) failWithCondition(ctx context.Context, sare *serviceaccountv1.ServiceAccountRequest, sareBefore *serviceaccountv1.ServiceAccountRequest, err error) error {
	apimeta.SetStatusCondition(&sare.Status.Conditions, metav1.Condition{
		Type:               serviceaccountv1.ConditionTypeServiceAccountReady,
		Status:             metav1.ConditionFalse,
		Reason:             serviceaccountv1.ConditionReasonServiceAccountReadyFailed,
		Message:            err.Error(),
		ObservedGeneration: sare.Generation,
	})
	if patchErr := c.Status().Patch(ctx, sare, client.MergeFrom(sareBefore)); patchErr != nil {
		logf.FromContext(ctx).Error(patchErr, "failed to update status conditions after reconcile error")
	}
	return err
}

func (c *Controller) reconcileDelete(ctx context.Context, sare *serviceaccountv1.ServiceAccountRequest) error {
	if !controllerutil.ContainsFinalizer(sare, finalizer) {
		return nil
	}
	if err := c.deleteServiceAccount(ctx, sare); err != nil {
		return err
	}
	controllerutil.RemoveFinalizer(sare, finalizer)
	return c.Update(ctx, sare)
}

func (c *Controller) deleteServiceAccount(_ context.Context, _ *serviceaccountv1.ServiceAccountRequest) error {
	return nil
}

func (c *Controller) reconcileUpdate(_ context.Context, _ *serviceaccountv1.ServiceAccountRequest) (ctrl.Result, error) {
	return ctrl.Result{}, nil
}

func (c *Controller) secretExists(ctx context.Context, namespace, name string) (bool, error) {
	var secret corev1.Secret
	err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &secret)
	if err == nil {
		return true, nil
	}
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	return false, err
}

func (c *Controller) getProducer(ctx context.Context, namespace, producerName string) (*serviceaccountv1.ServiceAccountProducer, error) {
	var sapr serviceaccountv1.ServiceAccountProducer
	if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: producerName}, &sapr); err != nil {
		return nil, err
	}
	return &sapr, nil
}

func toCreateParams(params *serviceaccountv1.ServiceAccountRequestParams) httpclient.CreateParams {
	if params == nil {
		return httpclient.CreateParams{}
	}
	return httpclient.CreateParams{
		Options: params.Options,
		Args:    params.Args,
	}
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
