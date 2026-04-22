package request

import (
	"context"

	serviceaccountv1 "github.com/cloudogu/k8s-serviceaccount-lib/api/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// Controller reconciles ServiceAccountRequest resources.
type Controller struct {
	client.Client
}

func New(rtClient client.Client) *Controller {
	return &Controller{Client: rtClient}
}

// +kubebuilder:rbac:groups=k8s.cloudogu.com,resources=serviceaccountrequests,verbs=get;list;watch
// +kubebuilder:rbac:groups=k8s.cloudogu.com,resources=serviceaccountrequests/status,verbs=get

// Reconcile keeps the current implementation intentionally side-effect free.
func (c *Controller) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := logf.FromContext(ctx).WithValues("serviceAccountRequest", req.NamespacedName)

	var serviceAccountRequest serviceaccountv1.ServiceAccountRequest
	if err := c.Get(ctx, req.NamespacedName, &serviceAccountRequest); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	logger.Info("service account request reconciled without business logic")

	return ctrl.Result{}, nil
}

func (c *Controller) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&serviceaccountv1.ServiceAccountRequest{}).
		Named("serviceaccountrequest").
		Complete(c)
}
