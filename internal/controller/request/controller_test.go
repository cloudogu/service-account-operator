package request

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

	t.Run("should return empty result for optional SARE when producer is not found", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSARE("grafana-to-prometheus", "ecosystem", "prometheus", true)
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
		sare := newTestSARE("grafana-to-prometheus", "ecosystem", "prometheus", false)
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare).Build()
		controller := New(rtClient)

		_, err := controller.Reconcile(context.Background(), reconcileRequest("grafana-to-prometheus", "ecosystem"))
		if err == nil {
			t.Fatal("Reconcile() expected error for missing required producer")
		}
	})

	t.Run("should return error when producer has no HTTP spec", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSARE("grafana-to-prometheus", "ecosystem", "prometheus", false)
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
		sare := newTestSARE("grafana-to-prometheus", "ecosystem", "prometheus", false)
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
