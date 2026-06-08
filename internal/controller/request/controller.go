package request

import (
	"context"
	"fmt"

	serviceaccountv1 "github.com/cloudogu/k8s-serviceaccount-lib/api/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
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

func (c *Controller) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := logf.FromContext(ctx).WithValues("serviceAccountRequest", req.NamespacedName)

	var sare serviceaccountv1.ServiceAccountRequest
	if err := c.Get(ctx, req.NamespacedName, &sare); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	producer, err := c.getProducer(ctx, sare.Namespace, sare.Spec.Producer)
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

	if producer.Spec.HTTP == nil {
		return ctrl.Result{}, fmt.Errorf("producer %q has no HTTP spec configured", producer.Name)
	}

	logger.Info("producer found", "producer", producer.Name, "endpoint", producer.Spec.HTTP.Endpoint)

	return ctrl.Result{}, nil
}

func (c *Controller) getProducer(ctx context.Context, namespace, producerName string) (*serviceaccountv1.ServiceAccountProducer, error) {
	var producer serviceaccountv1.ServiceAccountProducer
	if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: producerName}, &producer); err != nil {
		return nil, err
	}
	return &producer, nil
}

func (c *Controller) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&serviceaccountv1.ServiceAccountRequest{}).
		Named("serviceaccountrequest").
		Complete(c)
}
