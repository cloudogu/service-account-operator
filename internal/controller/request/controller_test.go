package request

import (
	"context"
	"errors"
	"testing"
	"time"

	serviceaccountv1 "github.com/cloudogu/k8s-serviceaccount-lib/api/v1"
	httpclient "github.com/cloudogu/service-account-operator/internal/producer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// mockHTTPClient is a hand-written test double for httpclient.HTTPClient.
// A generated mock is not used here because the interface lives in a different package
// and mockery places in-package mocks in non-importable _test.go files.
type mockHTTPClient struct {
	credentials map[string]string
	err         error
}

func (m *mockHTTPClient) Create(_ context.Context, _ string, _ httpclient.CreateParams) (map[string]string, error) {
	return m.credentials, m.err
}

// --- helpers ---

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, serviceaccountv1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
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

func newAuthSecret(name, namespace, key, value string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Data:       map[string][]byte{key: []byte(value)},
	}
}

func reconcileRequest(name, namespace string) ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: name}}
}

// --- tests ---

func TestController_Reconcile(t *testing.T) {
	t.Run("should ignore not found SARE", func(t *testing.T) {
		scheme := newTestScheme(t)
		rtClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		controller := New(rtClient, scheme)

		result, err := controller.Reconcile(context.Background(), reconcileRequest("missing", "ecosystem"))

		require.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)
	})

	t.Run("should add finalizer when missing and return empty result", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSARE("grafana-to-prometheus", "ecosystem", "prometheus", false)
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare).Build()
		controller := New(rtClient, scheme)

		result, err := controller.Reconcile(context.Background(), reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)

		var updated serviceaccountv1.ServiceAccountRequest
		require.NoError(t, rtClient.Get(context.Background(), types.NamespacedName{Name: "grafana-to-prometheus", Namespace: "ecosystem"}, &updated))
		assert.Equal(t, []string{finalizer}, updated.Finalizers)
	})

	t.Run("should remove finalizer when SARE is being deleted", func(t *testing.T) {
		scheme := newTestScheme(t)
		now := metav1.NewTime(time.Now())
		sare := newTestSAREWithFinalizer("grafana-to-prometheus", "ecosystem", "prometheus", false)
		sare.DeletionTimestamp = &now
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare).Build()
		controller := New(rtClient, scheme)

		result, err := controller.Reconcile(context.Background(), reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)

		// The fake client removes the object once the last finalizer is gone and deletionTimestamp is set.
		var updated serviceaccountv1.ServiceAccountRequest
		err = rtClient.Get(context.Background(), types.NamespacedName{Name: "grafana-to-prometheus", Namespace: "ecosystem"}, &updated)
		if err == nil {
			assert.Empty(t, updated.Finalizers)
		}
	})

	t.Run("should skip reconcile when target secret already exists in cluster", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSAREWithFinalizer("grafana-to-prometheus", "ecosystem", "prometheus", false)
		existingSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "grafana-to-prometheus", Namespace: "ecosystem"},
		}
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare, existingSecret).Build()
		httpFactoryMock := newMockHttpClientFactory(t)
		controller := New(rtClient, scheme)
		controller.httpClientFactory = httpFactoryMock

		result, err := controller.Reconcile(context.Background(), reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)
		httpFactoryMock.AssertNotCalled(t, "New")
	})

	t.Run("should skip reconcile when custom secretRef target already exists in cluster", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSAREWithFinalizer("grafana-to-prometheus", "ecosystem", "prometheus", false)
		sare.Spec.SecretRef = &serviceaccountv1.LocalSecretRef{Name: "custom-secret"}
		existingSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "custom-secret", Namespace: "ecosystem"},
		}
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare, existingSecret).Build()
		controller := New(rtClient, scheme)

		result, err := controller.Reconcile(context.Background(), reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)
	})

	t.Run("should return empty result for optional SARE when producer is not found", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSAREWithFinalizer("grafana-to-prometheus", "ecosystem", "prometheus", true)
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare).Build()
		controller := New(rtClient, scheme)

		result, err := controller.Reconcile(context.Background(), reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)
	})

	t.Run("should return error for required SARE when producer is not found", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSAREWithFinalizer("grafana-to-prometheus", "ecosystem", "prometheus", false)
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare).Build()
		controller := New(rtClient, scheme)

		_, err := controller.Reconcile(context.Background(), reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.Error(t, err)
	})

	t.Run("should return error when producer has no HTTP spec", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSAREWithFinalizer("grafana-to-prometheus", "ecosystem", "prometheus", false)
		sapr := &serviceaccountv1.ServiceAccountProducer{
			ObjectMeta: metav1.ObjectMeta{Name: "prometheus", Namespace: "ecosystem"},
			Spec: serviceaccountv1.ServiceAccountProducerSpec{
				Producer: "prometheus",
				Exec:     &serviceaccountv1.ExecProducer{Command: "/create-sa.sh", Selector: metav1.LabelSelector{}},
			},
		}
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare, sapr).Build()
		controller := New(rtClient, scheme)

		_, err := controller.Reconcile(context.Background(), reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.Error(t, err)
	})

	t.Run("should return error when auth secret is not found", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSAREWithFinalizer("grafana-to-prometheus", "ecosystem", "prometheus", false)
		sapr := newTestSAPR("prometheus", "ecosystem", "http://prometheus:9090/serviceaccounts")
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare, sapr).Build()
		controller := New(rtClient, scheme)

		_, err := controller.Reconcile(context.Background(), reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.Error(t, err)
	})

	t.Run("should return error when auth secret is missing the expected key", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSAREWithFinalizer("grafana-to-prometheus", "ecosystem", "prometheus", false)
		sapr := newTestSAPR("prometheus", "ecosystem", "http://prometheus:9090/serviceaccounts")
		authSecret := newAuthSecret("prometheus-sa-secret", "ecosystem", "wrongKey", "token")
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare, sapr, authSecret).Build()
		controller := New(rtClient, scheme)

		_, err := controller.Reconcile(context.Background(), reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.Error(t, err)
	})

	t.Run("should return error when HTTP client fails", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSAREWithFinalizer("grafana-to-prometheus", "ecosystem", "prometheus", false)
		sapr := newTestSAPR("prometheus", "ecosystem", "http://prometheus:9090/serviceaccounts")
		authSecret := newAuthSecret("prometheus-sa-secret", "ecosystem", "apiKey", "secret-token")
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare, sapr, authSecret).Build()

		httpClientMock := &mockHTTPClient{err: errors.New("connection refused")}
		httpFactoryMock := newMockHttpClientFactory(t)
		httpFactoryMock.EXPECT().New(mock.Anything, mock.Anything).Return(httpClientMock)
		controller := New(rtClient, scheme)
		controller.httpClientFactory = httpFactoryMock

		_, err := controller.Reconcile(context.Background(), reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.Error(t, err)
	})

	t.Run("should create secret and update status on success", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSAREWithFinalizer("grafana-to-prometheus", "ecosystem", "prometheus", false)
		sapr := newTestSAPR("prometheus", "ecosystem", "http://prometheus:9090/serviceaccounts")
		authSecret := newAuthSecret("prometheus-sa-secret", "ecosystem", "apiKey", "secret-token")
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(sare, sapr, authSecret).
			WithStatusSubresource(&serviceaccountv1.ServiceAccountRequest{}).
			Build()

		httpClientMock := &mockHTTPClient{credentials: map[string]string{"username": "grafana-user", "password": "pass"}}
		httpFactoryMock := newMockHttpClientFactory(t)
		httpFactoryMock.EXPECT().New(mock.Anything, mock.Anything).Return(httpClientMock)

		secretMgrMock := newMockSecretManager(t)
		secretMgrMock.EXPECT().CreateOrUpdate(mock.Anything, mock.Anything, mock.Anything).Return("grafana-to-prometheus", nil)

		controller := New(rtClient, scheme)
		controller.httpClientFactory = httpFactoryMock
		controller.secretManager = secretMgrMock

		result, err := controller.Reconcile(context.Background(), reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)

		var updated serviceaccountv1.ServiceAccountRequest
		require.NoError(t, rtClient.Get(context.Background(), types.NamespacedName{Name: "grafana-to-prometheus", Namespace: "ecosystem"}, &updated))
		require.NotNil(t, updated.Status.SecretRef)
		assert.Equal(t, "grafana-to-prometheus", updated.Status.SecretRef.Name)
	})
}
