package request

import (
	"context"
	"errors"
	"fmt"
	"time"

	serviceaccountv2 "github.com/cloudogu/k8s-serviceaccount-lib/v2/api/v2"
	"github.com/cloudogu/service-account-operator/internal/config"
	"github.com/cloudogu/service-account-operator/internal/controller/request/cron"
	"github.com/cloudogu/service-account-operator/internal/producer"
	sa "github.com/cloudogu/service-account-operator/internal/serviceaccount"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	finalizer = "k8s.cloudogu.com/service-account-request-finalizer"
)

// Controller reconciles ServiceAccountRequest resources.
type Controller struct {
	client                k8sClient
	secretManager         secretManager
	producerClientFactory producerClientFactory
	operatorConfig        *config.OperatorConfig
	eventRecorder         eventRecorder
	cronTaskFactory       taskRunnerFactory
	rotateCronWatcher     map[string]cron.TaskRunner
}

type defaultCronTaskFactory struct{}

func (d *defaultCronTaskFactory) New(ctx context.Context, expr string, jobClosure cron.JobFunc) (cron.TaskRunner, error) {
	return cron.New(ctx, expr, jobClosure)
}

func New(rtClient k8sClient, scheme *runtime.Scheme, operatorConfig *config.OperatorConfig, eventRecorder eventRecorder) *Controller {
	return &Controller{
		client:                rtClient,
		secretManager:         sa.NewSecretManager(rtClient, scheme),
		producerClientFactory: producer.NewClientFactory(rtClient),
		operatorConfig:        operatorConfig,
		eventRecorder:         eventRecorder,
		cronTaskFactory:       new(defaultCronTaskFactory),
		rotateCronWatcher:     make(map[string]cron.TaskRunner),
	}
}

func (c *Controller) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := logf.FromContext(ctx).WithValues("serviceAccountRequest", req.Name)

	var sare serviceaccountv2.ServiceAccountRequest
	if err := c.client.Get(ctx, req.NamespacedName, &sare); apierrors.IsNotFound(err) {
		logger.Info("service account request not found, skipping reconcile")

		return ctrl.Result{}, nil
	} else if err != nil {
		logger.Error(err, "failed to get service account request")

		return ctrl.Result{}, fmt.Errorf("failed to get service account request %q: %w", req.Name, err)
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

	return c.reconcileCreateOrUpdate(ctx, &sare)
}

// TODO: When an optional service account is created only after the consumer has
// already started (the consumer came up without it), the consumer may not pick up the new
// credentials until it is restarted. Decide whether this operator should trigger a consumer
// restart (e.g. annotate/roll the consumer's Deployment) or whether the consumer is expected to
// reload credentials on its own. Pending product decision before implementing.
//
// See cloudogu/service-account-operator#8

func (c *Controller) reconcileCreateOrUpdate(ctx context.Context, sare *serviceaccountv2.ServiceAccountRequest) (ctrl.Result, error) {
	logger := logf.FromContext(ctx).WithValues("serviceAccountRequest", sare.Name)

	secretExists, secretName, err := c.secretManager.Exists(ctx, sare)
	if err != nil {
		if errors.Is(err, sa.ErrSecretConflict) {
			return ctrl.Result{}, c.fail(ctx, sare, err)
		}

		logger.Error(err, "failed to check if service account secret exists")

		return ctrl.Result{}, fmt.Errorf("failed to check if service account secret exists for %q: %w", sare.Name, err)
	}

	createOrUpdateString := "created"
	if secretExists {
		createOrUpdateString = "updated"
	}
	logger.Info("service account request needs to be " + createOrUpdateString)

	if sare.Spec.Rotation.Enabled {
		err := c.setSaRotationWatcher(ctx, sare)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to replace service account rotation expression: %w", err)
		}
	} else {
		c.deleteSaRotationWatcher(sare)
	}

	sapr, err := c.getProducer(ctx, sare.Namespace, sare.Spec.Producer)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("producer not found, deleting any secrets that might have been created for this request", "producer", sare.Spec.Producer)
			deleteErr := c.secretManager.Delete(ctx, sare)
			if deleteErr != nil {
				return ctrl.Result{}, fmt.Errorf("failed to delete service account secret of deleted producer %q: %w", sapr.Name, deleteErr)
			}

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

	behaviorParams := producer.BehaviorParams{}
	if !secretExists {
		behaviorParams.RotateServiceAccountNow = true
	}

	credentials, err := saClient.CreateOrUpdate(ctx, qualifiedConsumer(sare), sare.Spec.Params, behaviorParams)
	if err != nil {
		return ctrl.Result{}, c.fail(ctx, sare, fmt.Errorf("failed to create/update service account at producer %q: %w", sapr.Name, err))
	}

	if credentials == nil {
		logger.Info("The producer did not return credentials upon update indicating no change. Skipping the secret update.", "producer", sare.Spec.Producer)
	} else {
		secretName, err = c.secretManager.CreateOrUpdate(ctx, sare, credentials)
		if err != nil {
			return ctrl.Result{}, c.fail(ctx, sare, fmt.Errorf("failed to store credentials in Kubernetes secret: %w", err))
		}

		createUpdateTitleString := cases.Title(language.English).String(createOrUpdateString)
		c.eventRecorder.Eventf(sapr, sare, corev1.EventTypeNormal, "ServiceAccountRequest", "ServiceAccount"+createUpdateTitleString, "%s service account %q", createUpdateTitleString, sare.Spec.Consumer)
	}

	if err := serviceAccountReady(ctx, c.client, sare, secretName); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status after successful create/update for %q: %w", sare.Name, err)
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
	c.deleteSaRotationWatcher(sare)

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

func (c *Controller) deleteSaRotationWatcher(sare *serviceaccountv2.ServiceAccountRequest) {
	sareName := namespacedName(sare)
	if cronWatcher, ok := c.rotateCronWatcher[sareName]; ok {
		cronWatcher.Stop()
		delete(c.rotateCronWatcher, sareName)
	}
}

func (c *Controller) setSaRotationWatcher(ctx context.Context, sare *serviceaccountv2.ServiceAccountRequest) (err error) {
	sareName := namespacedName(sare)
	if cronWatcher, ok := c.rotateCronWatcher[sareName]; ok {
		cronWatcher.Stop()
	}

	// deleteSaSecretFunc relies on deleting the secret to a consumer because we watch the secret for deletion.
	// if the deletion is detected, an update to the SA is issued against the producer.
	deleteSaSecretFunc := func(ctx context.Context) (int, error) {
		err := c.secretManager.Delete(ctx, sare)
		if err != nil {
			// currently, the gronx tasker uses the return code for debugging the gronx task.
			return 1, fmt.Errorf("failed to delete service account secret %q for service account rotation: %w", sare.Name, err)
		}

		return 0, nil
	}

	cronWatcher, err := c.cronTaskFactory.New(ctx, sare.Spec.Rotation.Rotation, deleteSaSecretFunc)
	if err != nil {
		return fmt.Errorf("failed to set cron watcher for SARE %q: %w", sareName, err)
	}

	go func() {
		cronWatcher.Run()
	}()
	go func() {
		select {
		case <-ctx.Done():
			cronWatcher.Stop()
		case <-cronWatcher.Stopped():
		}
	}()
	c.rotateCronWatcher[sareName] = cronWatcher

	return nil
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

	// explicitly deleting the secret would cause unnecessary reconciliation
	// the secret is deleted via the controller reference anyway

	sapr, err := c.getProducer(ctx, sare.Namespace, sare.Spec.Producer)
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

	c.eventRecorder.Eventf(sapr, sare, corev1.EventTypeNormal, "ServiceAccountRequest", "ServiceAccountDeleted", "Deleted service account %q", sare.Spec.Consumer)
	return nil
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
		For(&serviceaccountv2.ServiceAccountRequest{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Owns(&corev1.Secret{}, builder.WithPredicates(wasDeletedPredicate())).
		Watches(
			&serviceaccountv2.ServiceAccountProducer{},
			handler.EnqueueRequestsFromMapFunc(c.enqueueRequestsForProducer),
			builder.WithPredicates(predicate.Or(predicate.GenerationChangedPredicate{}, producerGotReadyPredicate())),
		).
		Named("serviceaccountrequest").
		Complete(c)
}

func producerGotReadyPredicate() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.TypedUpdateEvent[client.Object]) bool {
			producerOld, ok := e.ObjectOld.(*serviceaccountv2.ServiceAccountProducer)
			if !ok || producerOld == nil {
				return false
			}

			producerNew, ok := e.ObjectNew.(*serviceaccountv2.ServiceAccountProducer)
			if !ok || producerNew == nil {
				return false
			}

			oldConditionNotReady := meta.IsStatusConditionFalse(producerOld.Status.Conditions, serviceaccountv2.ConditionTypeReady)
			newConditionReady := meta.IsStatusConditionTrue(producerNew.Status.Conditions, serviceaccountv2.ConditionTypeReady)
			return oldConditionNotReady && newConditionReady
		},
	}
}

func wasDeletedPredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(event.TypedCreateEvent[client.Object]) bool {
			return false
		},
		DeleteFunc: func(event.TypedDeleteEvent[client.Object]) bool {
			return true
		},
		UpdateFunc: func(event.TypedUpdateEvent[client.Object]) bool {
			return false
		},
		GenericFunc: func(event.TypedGenericEvent[client.Object]) bool {
			return false
		},
	}
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

// namespacedName returns a namespace-qualified SARE name to ensure uniqueness across namespaces. The resource name's
// uniqueness is itself enforced by Helm, so overwriting other consumer's SAREs are avoided this way.
//
// For example, a consumer "grafana-prometheus-sa" in namespace "ecosystem" becomes "grafana-prometheus-sa-ecosystem".
func namespacedName(sare *serviceaccountv2.ServiceAccountRequest) string {
	return sare.Name + "-" + sare.Namespace
}
