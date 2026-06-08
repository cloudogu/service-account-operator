package request

import (
	"context"
	"testing"
	"time"

	serviceaccountv1 "github.com/cloudogu/k8s-serviceaccount-lib/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := serviceaccountv1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() returned error: %v", err)
	}
	return scheme
}

func newTestSARE(name, namespace, producer string, optional bool) *serviceaccountv1.ServiceAccountRequest {
	return &serviceaccountv1.ServiceAccountRequest{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: serviceaccountv1.ServiceAccountRequestSpec{
			Consumer:     "grafana",
			ConsumerType: serviceaccountv1.DoguConsumerType,
			Producer:     producer,
			Optional:     optional,
		},
	}
}

// newTestSAREWithFinalizer creates a SARE that has already passed the finalizer step.
func newTestSAREWithFinalizer(name, namespace, producer string, optional bool) *serviceaccountv1.ServiceAccountRequest {
	sare := newTestSARE(name, namespace, producer, optional)
	sare.Finalizers = []string{finalizer}
	return sare
}

func newTestSAPR(name, namespace, endpoint string) *serviceaccountv1.ServiceAccountProducer {
	return &serviceaccountv1.ServiceAccountProducer{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: serviceaccountv1.ServiceAccountProducerSpec{
			Producer: name,
			HTTP: &serviceaccountv1.HTTPProducer{
				Endpoint: endpoint,
				AuthSecret: serviceaccountv1.ServiceAccountProducerAuthSecret{
					LocalSecretRef: serviceaccountv1.LocalSecretRef{Name: "prometheus-sa-secret"},
					Key:            "apiKey",
				},
			},
		},
	}
}

func reconcileRequest(name, namespace string) ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: name}}
}

func TestController_Reconcile(t *testing.T) {
	t.Run("should ignore not found SARE", func(t *testing.T) {
		scheme := newTestScheme(t)
		rtClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		controller := New(rtClient)

		result, err := controller.Reconcile(context.Background(), reconcileRequest("missing", "ecosystem"))
		if err != nil {
			t.Fatalf("Reconcile() returned error: %v", err)
		}
		if result != (ctrl.Result{}) {
			t.Fatalf("Reconcile() = %#v, want empty result", result)
		}
	})

	t.Run("should add finalizer when missing and return empty result", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSARE("grafana-to-prometheus", "ecosystem", "prometheus", false)
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare).Build()
		controller := New(rtClient)

		result, err := controller.Reconcile(context.Background(), reconcileRequest("grafana-to-prometheus", "ecosystem"))
		if err != nil {
			t.Fatalf("Reconcile() returned error: %v", err)
		}
		if result != (ctrl.Result{}) {
			t.Fatalf("Reconcile() = %#v, want empty result", result)
		}

		var updated serviceaccountv1.ServiceAccountRequest
		if err := rtClient.Get(context.Background(), types.NamespacedName{Name: "grafana-to-prometheus", Namespace: "ecosystem"}, &updated); err != nil {
			t.Fatalf("Get() returned error: %v", err)
		}
		if len(updated.Finalizers) != 1 || updated.Finalizers[0] != finalizer {
			t.Errorf("Finalizers = %v, want [%q]", updated.Finalizers, finalizer)
		}
	})

	t.Run("should remove finalizer when SARE is being deleted", func(t *testing.T) {
		scheme := newTestScheme(t)
		now := metav1.NewTime(time.Now())
		sare := newTestSAREWithFinalizer("grafana-to-prometheus", "ecosystem", "prometheus", false)
		sare.DeletionTimestamp = &now
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare).Build()
		controller := New(rtClient)

		result, err := controller.Reconcile(context.Background(), reconcileRequest("grafana-to-prometheus", "ecosystem"))
		if err != nil {
			t.Fatalf("Reconcile() returned error: %v", err)
		}
		if result != (ctrl.Result{}) {
			t.Fatalf("Reconcile() = %#v, want empty result", result)
		}

		// The fake client removes the object once the last finalizer is gone and deletionTimestamp is set.
		var updated serviceaccountv1.ServiceAccountRequest
		err = rtClient.Get(context.Background(), types.NamespacedName{Name: "grafana-to-prometheus", Namespace: "ecosystem"}, &updated)
		if err == nil && len(updated.Finalizers) != 0 {
			t.Errorf("Finalizers = %v, want empty", updated.Finalizers)
		}
	})

	t.Run("should return empty result for optional SARE when producer is not found", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSAREWithFinalizer("grafana-to-prometheus", "ecosystem", "prometheus", true)
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare).Build()
		controller := New(rtClient)

		result, err := controller.Reconcile(context.Background(), reconcileRequest("grafana-to-prometheus", "ecosystem"))
		if err != nil {
			t.Fatalf("Reconcile() returned error: %v", err)
		}
		if result != (ctrl.Result{}) {
			t.Fatalf("Reconcile() = %#v, want empty result", result)
		}
	})

	t.Run("should return error for required SARE when producer is not found", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSAREWithFinalizer("grafana-to-prometheus", "ecosystem", "prometheus", false)
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare).Build()
		controller := New(rtClient)

		_, err := controller.Reconcile(context.Background(), reconcileRequest("grafana-to-prometheus", "ecosystem"))
		if err == nil {
			t.Fatal("Reconcile() expected error for missing required producer")
		}
	})

	t.Run("should return error when producer has no HTTP spec", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSAREWithFinalizer("grafana-to-prometheus", "ecosystem", "prometheus", false)
		sapr := &serviceaccountv1.ServiceAccountProducer{
			ObjectMeta: metav1.ObjectMeta{Name: "prometheus", Namespace: "ecosystem"},
			Spec: serviceaccountv1.ServiceAccountProducerSpec{
				Producer: "prometheus",
				Exec: &serviceaccountv1.ExecProducer{
					Command:  "/create-sa.sh",
					Selector: metav1.LabelSelector{},
				},
			},
		}
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare, sapr).Build()
		controller := New(rtClient)

		_, err := controller.Reconcile(context.Background(), reconcileRequest("grafana-to-prometheus", "ecosystem"))
		if err == nil {
			t.Fatal("Reconcile() expected error when producer has no HTTP spec")
		}
	})

	t.Run("should succeed when producer with HTTP spec is found", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSAREWithFinalizer("grafana-to-prometheus", "ecosystem", "prometheus", false)
		sapr := newTestSAPR("prometheus", "ecosystem", "http://prometheus:9090/serviceaccounts")
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare, sapr).Build()
		controller := New(rtClient)

		result, err := controller.Reconcile(context.Background(), reconcileRequest("grafana-to-prometheus", "ecosystem"))
		if err != nil {
			t.Fatalf("Reconcile() returned error: %v", err)
		}
		if result != (ctrl.Result{}) {
			t.Fatalf("Reconcile() = %#v, want empty result", result)
		}
	})
}
