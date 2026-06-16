package request

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	serviceaccountv1 "github.com/cloudogu/k8s-serviceaccount-lib/api/v1"
	"github.com/cloudogu/service-account-operator/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	errors2 "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

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

var testOperatorConfig = &config.OperatorConfig{}

func TestController_Reconcile(t *testing.T) {
	t.Run("should ignore not found SARE", func(t *testing.T) {
		scheme := newTestScheme(t)
		rtClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		controller := New(rtClient, scheme, testOperatorConfig)

		result, err := controller.Reconcile(context.Background(), reconcileRequest("missing", "ecosystem"))

		require.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)
	})

	t.Run("should add finalizer when missing and continue reconciling in the same pass", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSARE("grafana-to-prometheus", "ecosystem", "prometheus", false)
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare).Build()
		controller := New(rtClient, scheme, testOperatorConfig)

		// The required producer is missing, so reconcile proceeds past the finalizer step and errors out.
		// This proves the finalizer addition no longer short-circuits the pass.
		_, err := controller.Reconcile(context.Background(), reconcileRequest("grafana-to-prometheus", "ecosystem"))
		require.Error(t, err)

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
		controller := New(rtClient, scheme, testOperatorConfig)

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
		controller := New(rtClient, scheme, testOperatorConfig)
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
		controller := New(rtClient, scheme, testOperatorConfig)

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
		controller := New(rtClient, scheme, testOperatorConfig)
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
		controller := New(rtClient, scheme, testOperatorConfig)

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
		controller := New(rtClient, scheme, testOperatorConfig)

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
		controller := New(rtClient, scheme, testOperatorConfig)
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

		httpClientMock := newMockServiceAccountClient(t)
		httpClientMock.EXPECT().Create(mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("connection refused"))
		factoryMock := newMockProducerClientFactory(t)
		factoryMock.EXPECT().NewForProducer(mock.Anything, mock.Anything, mock.Anything).Return(httpClientMock, nil)
		controller := New(rtClient, scheme, testOperatorConfig)
		controller.producerClientFactory = factoryMock

		_, err := controller.Reconcile(context.Background(), reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.Error(t, err)
		var updated serviceaccountv1.ServiceAccountRequest
		require.NoError(t, rtClient.Get(context.Background(), types.NamespacedName{Name: "grafana-to-prometheus", Namespace: "ecosystem"}, &updated))
		cond := findCondition(updated.Status.Conditions, serviceaccountv1.ConditionTypeServiceAccountReady)
		require.NotNil(t, cond)
		assert.Equal(t, metav1.ConditionFalse, cond.Status)
	})

	t.Run("should return error when adding finalizer fails", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSARE("grafana-to-prometheus", "ecosystem", "prometheus", false)
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(sare).
			WithInterceptorFuncs(interceptor.Funcs{
				Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
					return errors.New("etcd unavailable")
				},
			}).
			Build()
		controller := New(rtClient, scheme, testOperatorConfig)

		_, err := controller.Reconcile(context.Background(), reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to add finalizer")
	})

	t.Run("should return error when getProducer fails with a non-not-found error", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSAREWithFinalizer("grafana-to-prometheus", "ecosystem", "prometheus", false)
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(sare).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if _, ok := obj.(*serviceaccountv1.ServiceAccountProducer); ok {
						return errors.New("etcd connection lost")
					}
					return c.Get(ctx, key, obj, opts...)
				},
			}).
			Build()
		controller := New(rtClient, scheme, testOperatorConfig)

		_, err := controller.Reconcile(context.Background(), reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get producer")
	})

	t.Run("should return error and set ServiceAccountReady=False when secret storage fails", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSAREWithFinalizer("grafana-to-prometheus", "ecosystem", "prometheus", false)
		sapr := newTestSAPR("prometheus", "ecosystem", "http://prometheus:9090/serviceaccounts")
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(sare, sapr).
			WithStatusSubresource(&serviceaccountv1.ServiceAccountRequest{}).
			Build()
		httpClientMock := newMockServiceAccountClient(t)
		httpClientMock.EXPECT().Create(mock.Anything, mock.Anything, mock.Anything).
			Return(map[string]string{"key": "val"}, nil)
		factoryMock := newMockProducerClientFactory(t)
		factoryMock.EXPECT().NewForProducer(mock.Anything, mock.Anything, mock.Anything).Return(httpClientMock, nil)
		secretMgrMock := newMockSecretManager(t)
		secretMgrMock.EXPECT().Exists(mock.Anything, mock.Anything).Return(false, nil)
		secretMgrMock.EXPECT().CreateOrUpdate(mock.Anything, mock.Anything, mock.Anything).
			Return("", errors.New("disk full"))
		controller := New(rtClient, scheme, testOperatorConfig)
		controller.producerClientFactory = factoryMock
		controller.secretManager = secretMgrMock

		_, err := controller.Reconcile(context.Background(), reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to store credentials")
		var updated serviceaccountv1.ServiceAccountRequest
		require.NoError(t, rtClient.Get(context.Background(), types.NamespacedName{Name: "grafana-to-prometheus", Namespace: "ecosystem"}, &updated))
		cond := findCondition(updated.Status.Conditions, serviceaccountv1.ConditionTypeServiceAccountReady)
		require.NotNil(t, cond)
		assert.Equal(t, metav1.ConditionFalse, cond.Status)
	})

	t.Run("should return original error when fail() cannot update the status condition", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSAREWithFinalizer("grafana-to-prometheus", "ecosystem", "prometheus", false)
		sapr := newTestSAPR("prometheus", "ecosystem", "http://prometheus:9090/serviceaccounts")
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(sare, sapr).
			WithStatusSubresource(&serviceaccountv1.ServiceAccountRequest{}).
			WithInterceptorFuncs(interceptor.Funcs{
				SubResourcePatch: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
					return errors.New("status patch failed")
				},
			}).
			Build()
		factoryMock := newMockProducerClientFactory(t)
		factoryMock.EXPECT().NewForProducer(mock.Anything, mock.Anything, mock.Anything).
			Return(nil, errors.New("auth secret not found"))
		controller := New(rtClient, scheme, testOperatorConfig)
		controller.producerClientFactory = factoryMock

		_, err := controller.Reconcile(context.Background(), reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.Error(t, err)
		assert.Contains(t, err.Error(), "auth secret not found")
	})

	t.Run("should return error when producerReady status update fails after successful create", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSAREWithFinalizer("grafana-to-prometheus", "ecosystem", "prometheus", false)
		sapr := newTestSAPR("prometheus", "ecosystem", "http://prometheus:9090/serviceaccounts")
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(sare, sapr).
			WithStatusSubresource(&serviceaccountv1.ServiceAccountRequest{}).
			WithInterceptorFuncs(interceptor.Funcs{
				SubResourcePatch: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
					return errors.New("status patch failed")
				},
			}).
			Build()
		httpClientMock := newMockServiceAccountClient(t)
		httpClientMock.EXPECT().Create(mock.Anything, mock.Anything, mock.Anything).
			Return(map[string]string{"key": "val"}, nil)
		factoryMock := newMockProducerClientFactory(t)
		factoryMock.EXPECT().NewForProducer(mock.Anything, mock.Anything, mock.Anything).Return(httpClientMock, nil)
		secretMgrMock := newMockSecretManager(t)
		secretMgrMock.EXPECT().Exists(mock.Anything, mock.Anything).Return(false, nil)
		secretMgrMock.EXPECT().CreateOrUpdate(mock.Anything, mock.Anything, mock.Anything).Return("grafana-to-prometheus", nil)
		controller := New(rtClient, scheme, testOperatorConfig)
		controller.producerClientFactory = factoryMock
		controller.secretManager = secretMgrMock

		_, err := controller.Reconcile(context.Background(), reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to update status after successful create")
	})

	t.Run("should return error when serviceAccountReady status update fails after successful create", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSAREWithFinalizer("grafana-to-prometheus", "ecosystem", "prometheus", false)
		sapr := newTestSAPR("prometheus", "ecosystem", "http://prometheus:9090/serviceaccounts")
		patchCount := 0
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(sare, sapr).
			WithStatusSubresource(&serviceaccountv1.ServiceAccountRequest{}).
			WithInterceptorFuncs(interceptor.Funcs{
				SubResourcePatch: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
					patchCount++
					if patchCount == 1 {
						return c.Status().Patch(ctx, obj, patch, opts...)
					}
					return errors.New("status patch failed on second call")
				},
			}).
			Build()
		httpClientMock := newMockServiceAccountClient(t)
		httpClientMock.EXPECT().Create(mock.Anything, mock.Anything, mock.Anything).
			Return(map[string]string{"key": "val"}, nil)
		factoryMock := newMockProducerClientFactory(t)
		factoryMock.EXPECT().NewForProducer(mock.Anything, mock.Anything, mock.Anything).Return(httpClientMock, nil)
		secretMgrMock := newMockSecretManager(t)
		secretMgrMock.EXPECT().Exists(mock.Anything, mock.Anything).Return(false, nil)
		secretMgrMock.EXPECT().CreateOrUpdate(mock.Anything, mock.Anything, mock.Anything).Return("grafana-to-prometheus", nil)
		controller := New(rtClient, scheme, testOperatorConfig)
		controller.producerClientFactory = factoryMock
		controller.secretManager = secretMgrMock

		_, err := controller.Reconcile(context.Background(), reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to update status after successful create")
	})

	t.Run("should return nil when SARE is being deleted but does not carry the correct finalizer", func(t *testing.T) {
		scheme := newTestScheme(t)
		now := metav1.NewTime(time.Now())
		sare := newTestSARE("grafana-to-prometheus", "ecosystem", "prometheus", false)
		sare.DeletionTimestamp = &now
		sare.Finalizers = []string{"some-other-controller/finalizer"} // fake client requires at least one finalizer when DeletionTimestamp is set
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare).Build()
		controller := New(rtClient, scheme, testOperatorConfig)

		result, err := controller.Reconcile(context.Background(), reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)
	})

	t.Run("should return error when removing finalizer fails", func(t *testing.T) {
		scheme := newTestScheme(t)
		now := metav1.NewTime(time.Now())
		sare := newTestSAREWithFinalizer("grafana-to-prometheus", "ecosystem", "prometheus", false)
		sare.DeletionTimestamp = &now
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(sare).
			WithInterceptorFuncs(interceptor.Funcs{
				Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
					return errors.New("update denied")
				},
			}).
			Build()
		controller := New(rtClient, scheme, testOperatorConfig)

		_, err := controller.Reconcile(context.Background(), reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to remove finalizer")
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

		httpClientMock := newMockServiceAccountClient(t)
		httpClientMock.EXPECT().Create(mock.Anything, mock.Anything, mock.Anything).Return(map[string]string{"username": "grafana-user", "password": "pass"}, nil)
		factoryMock := newMockProducerClientFactory(t)
		factoryMock.EXPECT().NewForProducer(mock.Anything, mock.Anything, mock.Anything).Return(httpClientMock, nil)

		secretMgrMock := newMockSecretManager(t)
		secretMgrMock.EXPECT().Exists(mock.Anything, mock.Anything).Return(false, nil)
		secretMgrMock.EXPECT().CreateOrUpdate(mock.Anything, mock.Anything, mock.Anything).Return("grafana-to-prometheus", nil)

		controller := New(rtClient, scheme, testOperatorConfig)
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
		controller := New(rtClient, scheme, testOperatorConfig)

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
		controller := New(rtClient, scheme, testOperatorConfig)

		sapr := newTestSAPR("prometheus", "ecosystem", "http://prometheus:9090/serviceaccounts")
		requests := controller.enqueueRequestsForProducer(context.Background(), sapr)

		assert.Empty(t, requests)
	})

	t.Run("should return empty list when client.List fails", func(t *testing.T) {
		scheme := newTestScheme(t)
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithInterceptorFuncs(interceptor.Funcs{
				List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
					return errors.New("etcd unavailable")
				},
			}).
			Build()
		controller := New(rtClient, scheme, testOperatorConfig)

		sapr := newTestSAPR("prometheus", "ecosystem", "http://prometheus:9090/serviceaccounts")
		requests := controller.enqueueRequestsForProducer(context.Background(), sapr)

		assert.Empty(t, requests)
	})

	t.Run("should not enqueue SAREs from a different namespace", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSARE("grafana-to-prometheus", "other-namespace", "prometheus", true)
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare).Build()
		controller := New(rtClient, scheme, testOperatorConfig)

		sapr := newTestSAPR("prometheus", "ecosystem", "http://prometheus:9090/serviceaccounts")
		requests := controller.enqueueRequestsForProducer(context.Background(), sapr)

		assert.Empty(t, requests)
	})
}

const (
	testNamespace         = "ecosystem"
	testReqName           = "grafana"
	testPrName            = "prometheus"
	testConsumer          = "grafana"
	testQualifiedConsumer = "grafana-ecosystem"
)

func TestController_reconcileDelete(t *testing.T) {
	type fields struct {
		client                func(t *testing.T) (client.Client, *serviceaccountv1.ServiceAccountProducer)
		secretManager         func(t *testing.T) secretManager
		producerClientFactory func(t *testing.T, sapr *serviceaccountv1.ServiceAccountProducer) producerClientFactory
		operatorConfig        *config.OperatorConfig
	}
	type args struct {
		sare *serviceaccountv1.ServiceAccountRequest
	}

	testSare := &serviceaccountv1.ServiceAccountRequest{ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: testReqName, DeletionTimestamp: new(metav1.NewTime(time.Now())), Finalizers: []string{"k8s.cloudogu.com/service-account-request-finalizer"}}, Spec: serviceaccountv1.ServiceAccountRequestSpec{Producer: testPrName, Consumer: testConsumer}}

	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name: "should skip if no finalizer is present",
			args: args{
				sare: &serviceaccountv1.ServiceAccountRequest{},
			},
			wantErr: assert.NoError,
		},
		{
			name: "should return nil on successful deletion",
			args: args{
				sare: testSare,
			},
			fields: fields{
				secretManager: func(t *testing.T) secretManager {
					return mockSecretManagerDelete(t, testSare, nil)
				},
				client: func(t *testing.T) (client.Client, *serviceaccountv1.ServiceAccountProducer) {
					sClient := newMockK8sClient(t)

					testSapr := &serviceaccountv1.ServiceAccountProducer{ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: testPrName}}
					expectClientGetProducer(sClient, testSapr, nil)
					expectClientEmptyFinalizerSare(t, sClient, nil)
					expectClientPatchStatus(t, sClient, nil)

					return sClient, testSapr
				},
				producerClientFactory: func(t *testing.T, sapr *serviceaccountv1.ServiceAccountProducer) producerClientFactory {
					return mockProducerFactory(t, sapr, true, nil, nil, nil)
				},
			},
			wantErr: assert.NoError,
		},
		{
			name: "should return error on error deleting service account secret",
			args: args{
				sare: testSare,
			},
			fields: fields{
				client: func(t *testing.T) (client.Client, *serviceaccountv1.ServiceAccountProducer) {
					sClient := newMockK8sClient(t)
					expectClientErrorStatusSare(t, sClient, "failed to delete secret for service account request \"grafana\"")
					return sClient, nil
				},
				secretManager: func(t *testing.T) secretManager {
					return mockSecretManagerDelete(t, testSare, assert.AnError)
				},
			},
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool {
				return assert.ErrorContains(t, err, "failed to delete secret for service account request \"grafana\":")
			},
		},
		{
			name: "should return nil if producer is not found",
			args: args{
				sare: testSare,
			},
			fields: fields{
				secretManager: func(t *testing.T) secretManager {
					return mockSecretManagerDelete(t, testSare, nil)
				},
				client: func(t *testing.T) (client.Client, *serviceaccountv1.ServiceAccountProducer) {
					sClient := newMockK8sClient(t)

					expectClientGetProducer(sClient, nil, errors2.NewNotFound(schema.GroupResource{}, "producer"))
					expectClientEmptyFinalizerSare(t, sClient, nil)

					return sClient, nil
				},
			},
			wantErr: assert.NoError,
		},
		{
			name: "should return nil if the request does not exists anymore while deleting the finalizer",
			args: args{
				sare: testSare,
			},
			fields: fields{
				secretManager: func(t *testing.T) secretManager {
					return mockSecretManagerDelete(t, testSare, nil)
				},
				client: func(t *testing.T) (client.Client, *serviceaccountv1.ServiceAccountProducer) {
					sClient := newMockK8sClient(t)

					expectClientGetProducer(sClient, nil, errors2.NewNotFound(schema.GroupResource{}, "producer"))
					expectClientEmptyFinalizerSare(t, sClient, errors2.NewNotFound(schema.GroupResource{Group: "", Resource: ""}, "sare"))

					return sClient, nil
				},
			},
			wantErr: assert.NoError,
		},
		{
			name: "should return error on error deleting the finalizer",
			args: args{
				sare: testSare,
			},
			fields: fields{
				secretManager: func(t *testing.T) secretManager {
					return mockSecretManagerDelete(t, testSare, nil)
				},
				client: func(t *testing.T) (client.Client, *serviceaccountv1.ServiceAccountProducer) {
					sClient := newMockK8sClient(t)

					expectClientGetProducer(sClient, nil, errors2.NewNotFound(schema.GroupResource{}, "producer"))
					expectClientEmptyFinalizerSare(t, sClient, assert.AnError)

					return sClient, nil
				},
			},
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool {
				return assert.ErrorContains(t, err, "failed to remove finalizer from service account request \"grafana\":")
			},
		},
		{
			name: "should return error on error getting producer",
			args: args{
				sare: testSare,
			},
			fields: fields{
				secretManager: func(t *testing.T) secretManager {
					return mockSecretManagerDelete(t, testSare, nil)
				},
				client: func(t *testing.T) (client.Client, *serviceaccountv1.ServiceAccountProducer) {
					sClient := newMockK8sClient(t)
					expectClientGetProducer(sClient, nil, assert.AnError)
					expectClientErrorStatusSare(t, sClient, "failed to get producer \"prometheus\"")
					return sClient, nil
				},
			},
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool {
				return assert.ErrorContains(t, err, "failed to get producer \"prometheus\":")
			},
		},
		{
			name: "should return error on error getting service account client",
			args: args{
				sare: testSare,
			},
			fields: fields{
				secretManager: func(t *testing.T) secretManager {
					return mockSecretManagerDelete(t, testSare, nil)
				},
				client: func(t *testing.T) (client.Client, *serviceaccountv1.ServiceAccountProducer) {
					sClient := newMockK8sClient(t)
					testSapr := &serviceaccountv1.ServiceAccountProducer{ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: testPrName}}
					expectClientGetProducer(sClient, testSapr, nil)
					expectClientErrorStatusSare(t, sClient, "failed to build service account client for producer \"prometheus\"")
					return sClient, testSapr
				},
				producerClientFactory: func(t *testing.T, sapr *serviceaccountv1.ServiceAccountProducer) producerClientFactory {
					return mockProducerFactory(t, sapr, false, nil, nil, assert.AnError)
				},
			},
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool {
				return assert.ErrorContains(t, err, "failed to build service account client for producer \"prometheus\":")
			},
		},
		{
			name: "should return error on error checking if service account exists",
			args: args{
				sare: testSare,
			},
			fields: fields{
				secretManager: func(t *testing.T) secretManager {
					return mockSecretManagerDelete(t, testSare, nil)
				},
				client: func(t *testing.T) (client.Client, *serviceaccountv1.ServiceAccountProducer) {
					sClient := newMockK8sClient(t)
					testSapr := &serviceaccountv1.ServiceAccountProducer{ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: testPrName}}
					expectClientGetProducer(sClient, testSapr, nil)
					expectClientErrorStatusSare(t, sClient, "failed to check if service account \"grafana\" exists at producer \"prometheus\"")
					return sClient, testSapr
				},
				producerClientFactory: func(t *testing.T, sapr *serviceaccountv1.ServiceAccountProducer) producerClientFactory {
					return mockProducerFactory(t, sapr, false, assert.AnError, nil, nil)
				},
			},
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool {
				return assert.ErrorContains(t, err, "failed to check if service account \"grafana\" exists at producer \"prometheus\":")
			},
		},
		{
			name: "should return nil if the service account does not exist in the producer",
			args: args{
				sare: testSare,
			},
			fields: fields{
				secretManager: func(t *testing.T) secretManager {
					return mockSecretManagerDelete(t, testSare, nil)
				},
				client: func(t *testing.T) (client.Client, *serviceaccountv1.ServiceAccountProducer) {
					sClient := newMockK8sClient(t)
					testSapr := &serviceaccountv1.ServiceAccountProducer{ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: testPrName}}

					expectClientGetProducer(sClient, testSapr, nil)
					expectClientEmptyFinalizerSare(t, sClient, nil)

					return sClient, testSapr
				},
				producerClientFactory: func(t *testing.T, sapr *serviceaccountv1.ServiceAccountProducer) producerClientFactory {
					return mockProducerFactory(t, sapr, false, nil, nil, nil)
				},
			},
			wantErr: assert.NoError,
		},
		{
			name: "should return error on error deleting the service account at the producer",
			args: args{
				sare: testSare,
			},
			fields: fields{
				secretManager: func(t *testing.T) secretManager {
					return mockSecretManagerDelete(t, testSare, nil)
				},
				client: func(t *testing.T) (client.Client, *serviceaccountv1.ServiceAccountProducer) {
					sClient := newMockK8sClient(t)
					testSapr := &serviceaccountv1.ServiceAccountProducer{ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: testPrName}}
					expectClientGetProducer(sClient, testSapr, nil)
					expectClientErrorStatusSare(t, sClient, "failed to delete service account \"grafana\" at producer \"prometheus\":")

					return sClient, testSapr
				},
				producerClientFactory: func(t *testing.T, sapr *serviceaccountv1.ServiceAccountProducer) producerClientFactory {
					return mockProducerFactory(t, sapr, true, nil, assert.AnError, nil)
				},
			},
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool {
				return assert.ErrorContains(t, err, "failed to delete service account \"grafana\" at producer \"prometheus\":")
			},
		},
		{
			name: "should return nil if the update status of the producer fails",
			args: args{
				sare: testSare,
			},
			fields: fields{
				secretManager: func(t *testing.T) secretManager {
					return mockSecretManagerDelete(t, testSare, nil)
				},
				client: func(t *testing.T) (client.Client, *serviceaccountv1.ServiceAccountProducer) {
					sClient := newMockK8sClient(t)
					testSapr := &serviceaccountv1.ServiceAccountProducer{ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: testPrName}}
					expectClientGetProducer(sClient, testSapr, nil)
					expectClientPatchStatus(t, sClient, assert.AnError)
					expectClientEmptyFinalizerSare(t, sClient, nil)

					return sClient, testSapr
				},
				producerClientFactory: func(t *testing.T, sapr *serviceaccountv1.ServiceAccountProducer) producerClientFactory {
					return mockProducerFactory(t, sapr, true, nil, nil, nil)
				},
			},
			wantErr: assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Controller{}
			sapr := &serviceaccountv1.ServiceAccountProducer{}
			ctx := context.Background()
			if tt.fields.client != nil {
				c.client, sapr = tt.fields.client(t)
			}
			if tt.fields.secretManager != nil {
				c.secretManager = tt.fields.secretManager(t)
			}
			if tt.fields.producerClientFactory != nil {
				c.producerClientFactory = tt.fields.producerClientFactory(t, sapr)
			}
			if tt.fields.operatorConfig != nil {
				c.operatorConfig = tt.fields.operatorConfig
			} else {
				c.operatorConfig = &config.OperatorConfig{DeletionTimeout: time.Minute}
			}

			sare := &serviceaccountv1.ServiceAccountRequest{}
			if tt.args.sare != nil {
				sare = tt.args.sare.DeepCopy()
			}

			tt.wantErr(t, c.reconcileDelete(ctx, sare), fmt.Sprintf("reconcileDelete(%v, %v)", ctx, tt.args.sare))
		})
	}
}

func mockSecretManagerDelete(t *testing.T, sare *serviceaccountv1.ServiceAccountRequest, err error) secretManager {
	manager := newMockSecretManager(t)
	manager.EXPECT().Delete(mock.Anything, sare).Return(err)
	return manager
}

func expectClientGetProducer(c *mockK8sClient, sapr *serviceaccountv1.ServiceAccountProducer, err error) {
	c.EXPECT().Get(mock.Anything, types.NamespacedName{Namespace: testNamespace, Name: testPrName}, mock.IsType(&serviceaccountv1.ServiceAccountProducer{})).
		Run(func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) {
			if err == nil && sapr != nil {
				*obj.(*serviceaccountv1.ServiceAccountProducer) = *sapr
			}
		}).Return(err)
}

func expectClientEmptyFinalizerSare(t *testing.T, c *mockK8sClient, err error) {
	c.EXPECT().Update(mock.Anything, mock.IsType(&serviceaccountv1.ServiceAccountRequest{})).
		Run(func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) {
			if err == nil {
				updatedSaReq := obj.(*serviceaccountv1.ServiceAccountRequest)
				assert.Empty(t, updatedSaReq.Finalizers)
			}
		}).Return(err)
}

func expectClientErrorStatusSare(t *testing.T, c *mockK8sClient, expectedError string) {
	statusClient := NewMockStatusClient(t)
	statusClient.EXPECT().Patch(mock.Anything, mock.IsType(&serviceaccountv1.ServiceAccountRequest{}), mock.Anything).
		Run(func(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) {
			data, patchErr := patch.Data(obj)
			require.NoError(t, patchErr)
			s := string(data)
			assert.Contains(t, s, `{"status":{"conditions"`)
		}).Return(nil)
	c.EXPECT().Status().Return(statusClient)
}

func expectClientPatchStatus(t *testing.T, c *mockK8sClient, err error) {
	statusClient := NewMockStatusClient(t)
	statusClient.EXPECT().Patch(mock.Anything, mock.IsType(&serviceaccountv1.ServiceAccountProducer{}), mock.Anything).
		Run(func(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) {
			if err == nil {
				data, patchErr := patch.Data(obj)
				require.NoError(t, patchErr)
				assert.Contains(t, string(data), `{"status":{"lastExecution"`)
			}
		}).Return(err)
	c.EXPECT().Status().Return(statusClient)
}

func mockProducerFactory(t *testing.T, sapr *serviceaccountv1.ServiceAccountProducer, exists bool, existsErr, deleteErr, factoryErr error) producerClientFactory {
	factory := newMockProducerClientFactory(t)
	if factoryErr != nil {
		factory.EXPECT().NewForProducer(mock.Anything, testNamespace, sapr).Return(nil, factoryErr)
		return factory
	}

	saClient := newMockServiceAccountClient(t)
	saClient.EXPECT().Exists(mock.Anything, testQualifiedConsumer).Return(exists, existsErr)
	if existsErr == nil && exists {
		saClient.EXPECT().Delete(mock.Anything, testQualifiedConsumer).Return(deleteErr)
	}

	factory.EXPECT().NewForProducer(mock.Anything, testNamespace, sapr).Return(saClient, nil)
	return factory
}
