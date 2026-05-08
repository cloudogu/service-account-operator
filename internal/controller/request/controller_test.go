package request

import (
	"context"
	"testing"

	serviceaccountv1 "github.com/cloudogu/k8s-serviceaccount-lib/api/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestControllerReconcileReturnsSuccessForExistingObject(t *testing.T) {
	scheme := newTestScheme(t)
	obj := &serviceaccountv1.ServiceAccountRequest{}
	obj.Namespace = "default"
	obj.Name = "example-request"
	obj.Spec.Consumer = "grafana"
	obj.Spec.ConsumerType = serviceaccountv1.DoguConsumerType
	obj.Spec.Producer = "nexus"

	rtClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(obj).
		Build()

	controller := New(rtClient)

	result, err := controller.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "example-request"},
	})
	if err != nil {
		t.Fatalf("Reconcile() returned error: %v", err)
	}

	if result != (ctrl.Result{}) {
		t.Fatalf("Reconcile() = %#v, want empty result", result)
	}
}

func TestControllerReconcileIgnoresNotFound(t *testing.T) {
	scheme := newTestScheme(t)
	rtClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	controller := New(rtClient)

	result, err := controller.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "missing-request"},
	})
	if err != nil {
		t.Fatalf("Reconcile() returned error: %v", err)
	}

	if result != (ctrl.Result{}) {
		t.Fatalf("Reconcile() = %#v, want empty result", result)
	}
}

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := serviceaccountv1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() returned error: %v", err)
	}

	return scheme
}
