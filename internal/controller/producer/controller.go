package producer

import (
	"context"

	serviceaccountv1 "github.com/cloudogu/k8s-serviceaccount-lib/api/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// Controller reconciles ServiceAccountProducer resources.
type Controller struct {
	client.Client
}

func New(rtClient client.Client) *Controller {
	return &Controller{Client: rtClient}
}

// Reconcile keeps the current implementation intentionally side-effect free.
func (c *Controller) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := logf.FromContext(ctx).WithValues("serviceAccountProducer", req.NamespacedName)

	var serviceAccountProducer serviceaccountv1.ServiceAccountProducer
	if err := c.Get(ctx, req.NamespacedName, &serviceAccountProducer); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	// TODO Check if producer is ready
	// TODO Check periodically if it is ready

	logger.Info("service account producer reconciled without business logic")

	return ctrl.Result{}, nil
}

func (c *Controller) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&serviceaccountv1.ServiceAccountProducer{}).
		Named("serviceaccountproducer").
		Complete(c)
}
