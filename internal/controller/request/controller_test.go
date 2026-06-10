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
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// mockHTTPClient is a hand-written test double for httpclient.HTTPClient.
// A generated mock is not used here because the interface lives in a different package
// and mockery places in-package mocks in non-importable _test.go files.
type mockHTTPClient struct {
	credentials map[string]string
	err         error
}

func (m *mockHTTPClient) Create(_ context.Context, _ string, _ httpclient.Params) (map[string]string, error) {
	return m.credentials, m.err
}

func (m *mockHTTPClient) Update(_ context.Context, _ string, _ httpclient.Params) (map[string]string, error) {
	return m.credentials, m.err
}

func (m *mockHTTPClient) Delete(_ context.Context, _ string) error {
	return m.err
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

func reconcileRequest(name, namespace string) ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: name}}
}

func newOwnedSecret(name, namespace string, owner *serviceaccountv1.ServiceAccountRequest, scheme *runtime.Scheme, t *testing.T) *corev1.Secret {
	t.Helper()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}
	if err := controllerutil.SetControllerReference(owner, secret, scheme); err != nil {
		t.Fatalf("SetControllerReference() error: %v", err)
	}
	return secret
}

func findCondition(conditions []metav1.Condition, condType string) *metav1.Condition {
	return apimeta.FindStatusCondition(conditions, condType)
}

// newMockedController creates a Controller with mocked dependencies for unit testing.
// Tests that exercise the full producer-client path (auth secret lookup etc.) should
// use newTestSAPR + a real client instead — see producer_client_factory_test.go.
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
		existingSecret := newOwnedSecret("grafana-to-prometheus", "ecosystem", sare, scheme, t)
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare, existingSecret).Build()
		factoryMock := newMockProducerClientFactory(t)
		controller := New(rtClient, scheme)
		controller.producerClientFactory = factoryMock

		result, err := controller.Reconcile(context.Background(), reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)
		factoryMock.AssertNotCalled(t, "NewForProducer")
	})

	t.Run("should skip reconcile when custom secretRef target already exists in cluster", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSAREWithFinalizer("grafana-to-prometheus", "ecosystem", "prometheus", false)
		sare.Spec.SecretRef = &serviceaccountv1.LocalSecretRef{Name: "custom-secret"}
		existingSecret := newOwnedSecret("custom-secret", "ecosystem", sare, scheme, t)
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare, existingSecret).Build()
		controller := New(rtClient, scheme)

		result, err := controller.Reconcile(context.Background(), reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)
	})

	t.Run("should return wrapped error when secretManager.Exists fails", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSAREWithFinalizer("grafana-to-prometheus", "ecosystem", "prometheus", false)
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare).Build()
		secretMgrMock := newMockSecretManager(t)
		secretMgrMock.EXPECT().Exists(mock.Anything, mock.Anything).Return(false, errors.New("storage error"))
		controller := New(rtClient, scheme)
		controller.secretManager = secretMgrMock

		_, err := controller.Reconcile(context.Background(), reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to check if service account secret exists for")
		assert.Contains(t, err.Error(), "grafana-to-prometheus")
	})

	t.Run("should return empty result and set ProducerReady=False for optional SARE when producer is not found", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSAREWithFinalizer("grafana-to-prometheus", "ecosystem", "prometheus", true)
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(sare).
			WithStatusSubresource(&serviceaccountv1.ServiceAccountRequest{}).
			Build()
		controller := New(rtClient, scheme)

		result, err := controller.Reconcile(context.Background(), reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)

		var updated serviceaccountv1.ServiceAccountRequest
		require.NoError(t, rtClient.Get(context.Background(), types.NamespacedName{Name: "grafana-to-prometheus", Namespace: "ecosystem"}, &updated))
		cond := findCondition(updated.Status.Conditions, serviceaccountv1.ConditionTypeProducerReady)
		require.NotNil(t, cond)
		assert.Equal(t, metav1.ConditionFalse, cond.Status)
		assert.Equal(t, serviceaccountv1.ConditionReasonProducerReadyProducerNotFound, cond.Reason)
	})

	t.Run("should return error for required SARE when producer is not found", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSAREWithFinalizer("grafana-to-prometheus", "ecosystem", "prometheus", false)
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare).Build()
		controller := New(rtClient, scheme)

		_, err := controller.Reconcile(context.Background(), reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.Error(t, err)
	})

	t.Run("should return error and set ServiceAccountReady=False when factory fails to build client", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSAREWithFinalizer("grafana-to-prometheus", "ecosystem", "prometheus", false)
		sapr := newTestSAPR("prometheus", "ecosystem", "http://prometheus:9090/serviceaccounts")
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(sare, sapr).
			WithStatusSubresource(&serviceaccountv1.ServiceAccountRequest{}).
			Build()

		factoryMock := newMockProducerClientFactory(t)
		factoryMock.EXPECT().NewForProducer(mock.Anything, mock.Anything, mock.Anything).
			Return(nil, errors.New("auth secret not found"))
		controller := New(rtClient, scheme)
		controller.producerClientFactory = factoryMock

		_, err := controller.Reconcile(context.Background(), reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.Error(t, err)
		var updated serviceaccountv1.ServiceAccountRequest
		require.NoError(t, rtClient.Get(context.Background(), types.NamespacedName{Name: "grafana-to-prometheus", Namespace: "ecosystem"}, &updated))
		cond := findCondition(updated.Status.Conditions, serviceaccountv1.ConditionTypeServiceAccountReady)
		require.NotNil(t, cond)
		assert.Equal(t, metav1.ConditionFalse, cond.Status)
		assert.Equal(t, serviceaccountv1.ConditionReasonServiceAccountReadyFailed, cond.Reason)
	})

	t.Run("should return error and set ServiceAccountReady=False when HTTP client fails", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSAREWithFinalizer("grafana-to-prometheus", "ecosystem", "prometheus", false)
		sapr := newTestSAPR("prometheus", "ecosystem", "http://prometheus:9090/serviceaccounts")
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(sare, sapr).
			WithStatusSubresource(&serviceaccountv1.ServiceAccountRequest{}).
			Build()

		httpClientMock := &mockHTTPClient{err: errors.New("connection refused")}
		factoryMock := newMockProducerClientFactory(t)
		factoryMock.EXPECT().NewForProducer(mock.Anything, mock.Anything, mock.Anything).Return(httpClientMock, nil)
		controller := New(rtClient, scheme)
		controller.producerClientFactory = factoryMock

		_, err := controller.Reconcile(context.Background(), reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.Error(t, err)
		var updated serviceaccountv1.ServiceAccountRequest
		require.NoError(t, rtClient.Get(context.Background(), types.NamespacedName{Name: "grafana-to-prometheus", Namespace: "ecosystem"}, &updated))
		cond := findCondition(updated.Status.Conditions, serviceaccountv1.ConditionTypeServiceAccountReady)
		require.NotNil(t, cond)
		assert.Equal(t, metav1.ConditionFalse, cond.Status)
	})

	t.Run("should create secret and update status with Ready conditions on success", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSAREWithFinalizer("grafana-to-prometheus", "ecosystem", "prometheus", false)
		sapr := newTestSAPR("prometheus", "ecosystem", "http://prometheus:9090/serviceaccounts")
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(sare, sapr).
			WithStatusSubresource(&serviceaccountv1.ServiceAccountRequest{}).
			Build()

		httpClientMock := &mockHTTPClient{credentials: map[string]string{"username": "grafana-user", "password": "pass"}}
		factoryMock := newMockProducerClientFactory(t)
		factoryMock.EXPECT().NewForProducer(mock.Anything, mock.Anything, mock.Anything).Return(httpClientMock, nil)

		secretMgrMock := newMockSecretManager(t)
		secretMgrMock.EXPECT().Exists(mock.Anything, mock.Anything).Return(false, nil)
		secretMgrMock.EXPECT().CreateOrUpdate(mock.Anything, mock.Anything, mock.Anything).Return("grafana-to-prometheus", nil)

		controller := New(rtClient, scheme)
		controller.producerClientFactory = factoryMock
		controller.secretManager = secretMgrMock

		result, err := controller.Reconcile(context.Background(), reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)

		var updated serviceaccountv1.ServiceAccountRequest
		require.NoError(t, rtClient.Get(context.Background(), types.NamespacedName{Name: "grafana-to-prometheus", Namespace: "ecosystem"}, &updated))
		require.NotNil(t, updated.Status.SecretRef)
		assert.Equal(t, "grafana-to-prometheus", updated.Status.SecretRef.Name)

		saCond := findCondition(updated.Status.Conditions, serviceaccountv1.ConditionTypeServiceAccountReady)
		require.NotNil(t, saCond)
		assert.Equal(t, metav1.ConditionTrue, saCond.Status)
		assert.Equal(t, serviceaccountv1.ConditionReasonServiceAccountReadyCreated, saCond.Reason)

		producerCond := findCondition(updated.Status.Conditions, serviceaccountv1.ConditionTypeProducerReady)
		require.NotNil(t, producerCond)
		assert.Equal(t, metav1.ConditionTrue, producerCond.Status)
		assert.Equal(t, serviceaccountv1.ConditionReasonProducerReadyProducerFound, producerCond.Reason)
	})
}

func TestController_EnqueueRequestsForProducer(t *testing.T) {
	t.Run("should enqueue SAREs that reference the given producer", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare1 := newTestSARE("grafana-to-prometheus", "ecosystem", "prometheus", true)
		sare2 := newTestSARE("loki-to-prometheus", "ecosystem", "prometheus", true)
		sare3 := newTestSARE("grafana-to-alertmanager", "ecosystem", "alertmanager", false)
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare1, sare2, sare3).Build()
		controller := New(rtClient, scheme)

		sapr := newTestSAPR("prometheus", "ecosystem", "http://prometheus:9090/serviceaccounts")
		requests := controller.enqueueRequestsForProducer(context.Background(), sapr)

		require.Len(t, requests, 2)
		names := []string{requests[0].Name, requests[1].Name}
		assert.Contains(t, names, "grafana-to-prometheus")
		assert.Contains(t, names, "loki-to-prometheus")
	})

	t.Run("should return empty list when no SAREs reference the producer", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSARE("grafana-to-alertmanager", "ecosystem", "alertmanager", false)
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare).Build()
		controller := New(rtClient, scheme)

		sapr := newTestSAPR("prometheus", "ecosystem", "http://prometheus:9090/serviceaccounts")
		requests := controller.enqueueRequestsForProducer(context.Background(), sapr)

		assert.Empty(t, requests)
	})

	t.Run("should not enqueue SAREs from a different namespace", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSARE("grafana-to-prometheus", "other-namespace", "prometheus", true)
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare).Build()
		controller := New(rtClient, scheme)

		sapr := newTestSAPR("prometheus", "ecosystem", "http://prometheus:9090/serviceaccounts")
		requests := controller.enqueueRequestsForProducer(context.Background(), sapr)

		assert.Empty(t, requests)
	})
}
