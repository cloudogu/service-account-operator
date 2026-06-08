package request

import (
	"context"
	"fmt"

	serviceaccountv1 "github.com/cloudogu/k8s-serviceaccount-lib/api/v1"
	httpclient "github.com/cloudogu/service-account-operator/internal/producer"
	sa "github.com/cloudogu/service-account-operator/internal/serviceaccount"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const finalizer = "k8s.cloudogu.com/service-account-request-finalizer"

// secretManager writes service account credentials to a Kubernetes Secret.
type secretManager interface {
	CreateOrUpdate(ctx context.Context, sare *serviceaccountv1.ServiceAccountRequest, credentials map[string]string) (string, error)
}

// httpClientFactory creates HTTPClient instances bound to a specific producer endpoint.
type httpClientFactory interface {
	New(endpoint, apiKey string) httpclient.HTTPClient
}

type defaultHTTPClientFactory struct{}

func (f *defaultHTTPClientFactory) New(endpoint, apiKey string) httpclient.HTTPClient {
	return httpclient.NewHTTPClient(endpoint, apiKey)
}

// Controller reconciles ServiceAccountRequest resources.
type Controller struct {
	client.Client
	secretManager     secretManager
	httpClientFactory httpClientFactory
}

func New(rtClient client.Client, scheme *runtime.Scheme) *Controller {
	return &Controller{
		Client:            rtClient,
		secretManager:     sa.NewSecretManager(rtClient, scheme),
		httpClientFactory: &defaultHTTPClientFactory{},
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

	sapr, err := c.getProducer(ctx, sare.Namespace, sare.Spec.Producer)
	if err != nil {
		if apierrors.IsNotFound(err) {
			if sare.Spec.Optional {
				logger.Info("optional producer not found, skipping until producer is created", "producer", sare.Spec.Producer)
				return ctrl.Result{}, nil
			}
			return ctrl.Result{}, fmt.Errorf("required producer %q not found: %w", sare.Spec.Producer, err)
		}
		return ctrl.Result{}, fmt.Errorf("failed to get producer %q: %w", sare.Spec.Producer, err)
	}

	httpClient, err := c.buildHTTPClient(ctx, sare.Namespace, sapr)
	if err != nil {
		return ctrl.Result{}, err
	}

	credentials, err := httpClient.Create(ctx, sare.Spec.Consumer, toCreateParams(sare.Spec.Params))
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create service account at producer %q: %w", sapr.Name, err)
	}

	secretName, err := c.secretManager.CreateOrUpdate(ctx, sare, credentials)
	if err != nil {
		return ctrl.Result{}, err
	}

	sareBefore := sare.DeepCopy()
	sare.Status.SecretRef = &serviceaccountv1.LocalSecretRef{Name: secretName}
	return ctrl.Result{}, c.Status().Patch(ctx, sare, client.MergeFrom(sareBefore))
}

func (c *Controller) buildHTTPClient(ctx context.Context, namespace string, sapr *serviceaccountv1.ServiceAccountProducer) (httpclient.HTTPClient, error) {
	if sapr.Spec.HTTP == nil {
		return nil, fmt.Errorf("producer %q has no HTTP spec configured", sapr.Name)
	}
	apiKey, err := c.getAPIKey(ctx, namespace, sapr.Spec.HTTP.AuthSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to get API key for producer %q: %w", sapr.Name, err)
	}
	return c.httpClientFactory.New(sapr.Spec.HTTP.Endpoint, apiKey), nil
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

func (c *Controller) getAPIKey(ctx context.Context, namespace string, authSecret serviceaccountv1.ServiceAccountProducerAuthSecret) (string, error) {
	var secret corev1.Secret
	if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: authSecret.Name}, &secret); err != nil {
		return "", fmt.Errorf("failed to get auth secret %q: %w", authSecret.Name, err)
	}
	apiKey, ok := secret.Data[authSecret.Key]
	if !ok {
		return "", fmt.Errorf("auth secret %q does not contain key %q", authSecret.Name, authSecret.Key)
	}
	return string(apiKey), nil
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
		Named("serviceaccountrequest").
		Complete(c)
}
