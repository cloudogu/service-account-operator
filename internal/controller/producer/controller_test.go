package producer

import (
	"context"
	"testing"

	serviceaccountv1 "github.com/cloudogu/k8s-serviceaccount-lib/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestControllerReconcileReturnsSuccessForExistingObject(t *testing.T) {
	scheme := newTestScheme(t)
	obj := &serviceaccountv1.ServiceAccountProducer{}
	obj.Namespace = "default"
	obj.Name = "example-producer"
	obj.Spec.Producer = "nexus"
	obj.Spec.HTTP = &serviceaccountv1.HTTPProducer{
		Endpoint: "https://nexus:8081/serviceaccounts",
		AuthSecret: serviceaccountv1.ServiceAccountProducerAuthSecret{
			LocalSecretRef: serviceaccountv1.LocalSecretRef{Name: "nexus-auth"},
			Key:            "token",
		},
		Return: map[serviceaccountv1.AttributeName]serviceaccountv1.ProducerReturnDefinition{
			"username": {Description: "Generated username"},
			"password": {Description: "Generated password"},
		},
		Params: &serviceaccountv1.ProducerParams{
			Attributes: map[serviceaccountv1.AttributeName]serviceaccountv1.AttributeDefinition{
				"permissions": {Description: "Granted permissions", Type: "string"},
			},
		},
	}

	rtClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(obj).
		Build()

	controller := New(rtClient)

	result, err := controller.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "example-producer"},
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
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "missing-producer"},
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
	metav1.AddToGroupVersion(scheme, serviceaccountv1.GroupVersion)
	if err := serviceaccountv1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() returned error: %v", err)
	}

	return scheme
}
