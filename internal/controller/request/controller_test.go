package request

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	serviceaccountv1 "github.com/cloudogu/k8s-serviceaccount-lib/api/v1"
	producerclient "github.com/cloudogu/service-account-operator/internal/producer"
	sa "github.com/cloudogu/service-account-operator/internal/serviceaccount"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

var testCtx = context.Background()

var testSare = serviceaccountv1.ServiceAccountRequest{
	ObjectMeta: metav1.ObjectMeta{Name: "grafana-to-prometheus", Namespace: "ecosystem"},
	Spec: serviceaccountv1.ServiceAccountRequestSpec{
		Consumer:     "grafana",
		ConsumerType: serviceaccountv1.DoguConsumerType,
		Producer:     "prometheus",
	},
}

var testSapr = serviceaccountv1.ServiceAccountProducer{
	ObjectMeta: metav1.ObjectMeta{Name: "prometheus", Namespace: "ecosystem"},
	Spec: serviceaccountv1.ServiceAccountProducerSpec{
		Producer: "prometheus",
		HTTP: &serviceaccountv1.HTTPProducer{
			Endpoint: "http://prometheus:9090/serviceaccounts",
			AuthSecret: serviceaccountv1.ServiceAccountProducerAuthSecret{
				LocalSecretRef: serviceaccountv1.LocalSecretRef{Name: "prometheus-sa-secret"},
				Key:            "apiKey",
			},
		},
	},
}

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, serviceaccountv1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	return scheme
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

func matchSARE(sare serviceaccountv1.ServiceAccountRequest) any {
	return mock.MatchedBy(func(s *serviceaccountv1.ServiceAccountRequest) bool {
		return s != nil && s.Name == sare.Name
	})
}

func matchSAPR(sapr serviceaccountv1.ServiceAccountProducer) any {
	return mock.MatchedBy(func(p *serviceaccountv1.ServiceAccountProducer) bool {
		return p != nil && p.Name == sapr.Name
	})
}

func TestController_Reconcile(t *testing.T) {
	t.Run("should ignore not found SARE", func(t *testing.T) {
		scheme := newTestScheme(t)
		rtClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		controller := New(rtClient, scheme)

		result, err := controller.Reconcile(testCtx, reconcileRequest("missing", "ecosystem"))

		require.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)
	})

	t.Run("should add finalizer when missing and continue reconciling in the same pass", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := testSare
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&sare).Build()
		controller := New(rtClient, scheme)

		// The required producer is missing, so reconcile proceeds past the finalizer step and errors out.
		// This proves the finalizer addition no longer short-circuits the pass.
		_, err := controller.Reconcile(testCtx, reconcileRequest("grafana-to-prometheus", "ecosystem"))
		require.Error(t, err)

		var updated serviceaccountv1.ServiceAccountRequest
		require.NoError(t, rtClient.Get(testCtx, types.NamespacedName{Name: "grafana-to-prometheus", Namespace: "ecosystem"}, &updated))
		assert.Equal(t, []string{finalizer}, updated.Finalizers)
	})

	t.Run("should remove finalizer when SARE is being deleted", func(t *testing.T) {
		scheme := newTestScheme(t)
		now := metav1.NewTime(time.Now())
		sare := testSare
		sare.Finalizers = []string{finalizer}
		sare.DeletionTimestamp = &now
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&sare).Build()
		controller := New(rtClient, scheme)

		result, err := controller.Reconcile(testCtx, reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)

		// The fake client removes the object once the last finalizer is gone and deletionTimestamp is set.
		var updated serviceaccountv1.ServiceAccountRequest
		err = rtClient.Get(testCtx, types.NamespacedName{Name: "grafana-to-prometheus", Namespace: "ecosystem"}, &updated)
		if err == nil {
			assert.Empty(t, updated.Finalizers)
		}
	})

	t.Run("should skip reconcile when target secret already exists in cluster", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := testSare
		sare.Finalizers = []string{finalizer}
		existingSecret := newOwnedSecret("grafana-to-prometheus", "ecosystem", &sare, scheme, t)
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&sare, existingSecret).Build()
		factoryMock := newMockProducerClientFactory(t)
		controller := New(rtClient, scheme)
		controller.producerClientFactory = factoryMock

		result, err := controller.Reconcile(testCtx, reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)
	})

	t.Run("should skip reconcile when custom secretRef target already exists in cluster", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := testSare
		sare.Finalizers = []string{finalizer}
		sare.Spec.SecretRef = &serviceaccountv1.LocalSecretRef{Name: "custom-secret"}
		existingSecret := newOwnedSecret("custom-secret", "ecosystem", &sare, scheme, t)
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&sare, existingSecret).Build()
		controller := New(rtClient, scheme)

		result, err := controller.Reconcile(testCtx, reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)
	})

	t.Run("should return wrapped error when secretManager.Exists fails", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := testSare
		sare.Finalizers = []string{finalizer}
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&sare).Build()
		secretMgrMock := newMockSecretManager(t)
		secretMgrMock.EXPECT().Exists(testCtx, matchSARE(testSare)).Return(false, errors.New("storage error"))
		controller := New(rtClient, scheme)
		controller.secretManager = secretMgrMock

		_, err := controller.Reconcile(testCtx, reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to check if service account secret exists for")
		assert.Contains(t, err.Error(), "grafana-to-prometheus")
	})

	t.Run("should return ErrSecretConflict and set ServiceAccountReady=False when secret exists but is not owned by this SARE", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := testSare
		sare.Finalizers = []string{finalizer}
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(&sare).
			WithStatusSubresource(&serviceaccountv1.ServiceAccountRequest{}).
			Build()
		secretMgrMock := newMockSecretManager(t)
		conflictErr := fmt.Errorf("%w: secret %q in namespace %q", sa.ErrSecretConflict, "grafana-to-prometheus", "ecosystem")
		secretMgrMock.EXPECT().Exists(testCtx, matchSARE(testSare)).Return(false, conflictErr)
		controller := New(rtClient, scheme)
		controller.secretManager = secretMgrMock

		_, err := controller.Reconcile(testCtx, reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.ErrorIs(t, err, sa.ErrSecretConflict)
		var updated serviceaccountv1.ServiceAccountRequest
		require.NoError(t, rtClient.Get(testCtx, types.NamespacedName{Name: "grafana-to-prometheus", Namespace: "ecosystem"}, &updated))
		cond := findCondition(updated.Status.Conditions, serviceaccountv1.ConditionTypeServiceAccountReady)
		require.NotNil(t, cond)
		assert.Equal(t, metav1.ConditionFalse, cond.Status)
		assert.Equal(t, serviceaccountv1.ConditionReasonServiceAccountReadyFailed, cond.Reason)
		assert.Contains(t, cond.Message, "not owned by this service account request")
	})

	t.Run("should return empty result and set ServiceAccountReady=False with ProducerNotFound reason for optional SARE when producer is not found", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := testSare
		sare.Finalizers = []string{finalizer}
		sare.Spec.Optional = true
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(&sare).
			WithStatusSubresource(&serviceaccountv1.ServiceAccountRequest{}).
			Build()
		controller := New(rtClient, scheme)

		result, err := controller.Reconcile(testCtx, reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)

		var updated serviceaccountv1.ServiceAccountRequest
		require.NoError(t, rtClient.Get(testCtx, types.NamespacedName{Name: "grafana-to-prometheus", Namespace: "ecosystem"}, &updated))
		cond := findCondition(updated.Status.Conditions, serviceaccountv1.ConditionTypeServiceAccountReady)
		require.NotNil(t, cond)
		assert.Equal(t, metav1.ConditionFalse, cond.Status)
		assert.Equal(t, serviceaccountv1.ConditionReasonServiceAccountReadyProducerNotFound, cond.Reason)
	})

	t.Run("should return error for required SARE when producer is not found", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := testSare
		sare.Finalizers = []string{finalizer}
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&sare).Build()
		controller := New(rtClient, scheme)

		_, err := controller.Reconcile(testCtx, reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.Error(t, err)
	})

	t.Run("should return error and set ServiceAccountReady=False when factory fails to build client", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := testSare
		sare.Finalizers = []string{finalizer}
		sapr := testSapr
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(&sare, &sapr).
			WithStatusSubresource(&serviceaccountv1.ServiceAccountRequest{}).
			Build()

		factoryMock := newMockProducerClientFactory(t)
		factoryMock.EXPECT().NewForProducer(testCtx, "ecosystem", matchSAPR(testSapr)).
			Return(nil, errors.New("auth secret not found"))
		controller := New(rtClient, scheme)
		controller.producerClientFactory = factoryMock

		_, err := controller.Reconcile(testCtx, reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.Error(t, err)
		var updated serviceaccountv1.ServiceAccountRequest
		require.NoError(t, rtClient.Get(testCtx, types.NamespacedName{Name: "grafana-to-prometheus", Namespace: "ecosystem"}, &updated))
		cond := findCondition(updated.Status.Conditions, serviceaccountv1.ConditionTypeServiceAccountReady)
		require.NotNil(t, cond)
		assert.Equal(t, metav1.ConditionFalse, cond.Status)
		assert.Equal(t, serviceaccountv1.ConditionReasonServiceAccountReadyFailed, cond.Reason)
	})

	t.Run("should return error and set ServiceAccountReady=False when HTTP client fails", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := testSare
		sare.Finalizers = []string{finalizer}
		sapr := testSapr
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(&sare, &sapr).
			WithStatusSubresource(&serviceaccountv1.ServiceAccountRequest{}).
			Build()

		httpClientMock := newMockServiceAccountClient(t)
		httpClientMock.EXPECT().Create(testCtx, "grafana-ecosystem", producerclient.Params(nil)).Return(nil, errors.New("connection refused"))
		factoryMock := newMockProducerClientFactory(t)
		factoryMock.EXPECT().NewForProducer(testCtx, "ecosystem", matchSAPR(testSapr)).Return(httpClientMock, nil)
		controller := New(rtClient, scheme)
		controller.producerClientFactory = factoryMock

		_, err := controller.Reconcile(testCtx, reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.Error(t, err)
		var updated serviceaccountv1.ServiceAccountRequest
		require.NoError(t, rtClient.Get(testCtx, types.NamespacedName{Name: "grafana-to-prometheus", Namespace: "ecosystem"}, &updated))
		cond := findCondition(updated.Status.Conditions, serviceaccountv1.ConditionTypeServiceAccountReady)
		require.NotNil(t, cond)
		assert.Equal(t, metav1.ConditionFalse, cond.Status)
	})

	t.Run("should return error when adding finalizer fails", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := testSare
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(&sare).
			WithInterceptorFuncs(interceptor.Funcs{
				Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
					return errors.New("etcd unavailable")
				},
			}).
			Build()
		controller := New(rtClient, scheme)

		_, err := controller.Reconcile(testCtx, reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to add finalizer")
	})

	t.Run("should return error when getProducer fails with a non-not-found error", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := testSare
		sare.Finalizers = []string{finalizer}
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(&sare).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if _, ok := obj.(*serviceaccountv1.ServiceAccountProducer); ok {
						return errors.New("error while getting producer")
					}
					return c.Get(ctx, key, obj, opts...)
				},
			}).
			Build()
		controller := New(rtClient, scheme)

		_, err := controller.Reconcile(testCtx, reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get producer")
	})

	t.Run("should return error and set ServiceAccountReady=False when secret storage fails", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := testSare
		sare.Finalizers = []string{finalizer}
		sapr := testSapr
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(&sare, &sapr).
			WithStatusSubresource(&serviceaccountv1.ServiceAccountRequest{}).
			Build()
		httpClientMock := newMockServiceAccountClient(t)
		httpClientMock.EXPECT().Create(testCtx, "grafana-ecosystem", producerclient.Params(nil)).
			Return(map[string]string{"key": "val"}, nil)
		factoryMock := newMockProducerClientFactory(t)
		factoryMock.EXPECT().NewForProducer(testCtx, "ecosystem", matchSAPR(testSapr)).Return(httpClientMock, nil)
		secretMgrMock := newMockSecretManager(t)
		secretMgrMock.EXPECT().Exists(testCtx, matchSARE(testSare)).Return(false, nil)
		secretMgrMock.EXPECT().CreateOrUpdate(testCtx, matchSARE(testSare), map[string]string{"key": "val"}).
			Return("", errors.New("disk full"))
		controller := New(rtClient, scheme)
		controller.producerClientFactory = factoryMock
		controller.secretManager = secretMgrMock

		_, err := controller.Reconcile(testCtx, reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to store credentials")
		var updated serviceaccountv1.ServiceAccountRequest
		require.NoError(t, rtClient.Get(testCtx, types.NamespacedName{Name: "grafana-to-prometheus", Namespace: "ecosystem"}, &updated))
		cond := findCondition(updated.Status.Conditions, serviceaccountv1.ConditionTypeServiceAccountReady)
		require.NotNil(t, cond)
		assert.Equal(t, metav1.ConditionFalse, cond.Status)
	})

	t.Run("should return original error when fail() cannot update the status condition", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := testSare
		sare.Finalizers = []string{finalizer}
		sapr := testSapr
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(&sare, &sapr).
			WithStatusSubresource(&serviceaccountv1.ServiceAccountRequest{}).
			WithInterceptorFuncs(interceptor.Funcs{
				SubResourcePatch: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
					return errors.New("status patch failed")
				},
			}).
			Build()
		factoryMock := newMockProducerClientFactory(t)
		factoryMock.EXPECT().NewForProducer(testCtx, "ecosystem", matchSAPR(testSapr)).
			Return(nil, errors.New("auth secret not found"))
		controller := New(rtClient, scheme)
		controller.producerClientFactory = factoryMock

		_, err := controller.Reconcile(testCtx, reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.Error(t, err)
		assert.Contains(t, err.Error(), "auth secret not found")
	})

	t.Run("should return error when serviceAccountReady status update fails after successful create", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := testSare
		sare.Finalizers = []string{finalizer}
		sapr := testSapr
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(&sare, &sapr).
			WithStatusSubresource(&serviceaccountv1.ServiceAccountRequest{}).
			WithInterceptorFuncs(interceptor.Funcs{
				SubResourcePatch: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
					return errors.New("status patch failed")
				},
			}).
			Build()
		httpClientMock := newMockServiceAccountClient(t)
		httpClientMock.EXPECT().Create(testCtx, "grafana-ecosystem", producerclient.Params(nil)).
			Return(map[string]string{"key": "val"}, nil)
		factoryMock := newMockProducerClientFactory(t)
		factoryMock.EXPECT().NewForProducer(testCtx, "ecosystem", matchSAPR(testSapr)).Return(httpClientMock, nil)
		secretMgrMock := newMockSecretManager(t)
		secretMgrMock.EXPECT().Exists(testCtx, matchSARE(testSare)).Return(false, nil)
		secretMgrMock.EXPECT().CreateOrUpdate(testCtx, matchSARE(testSare), map[string]string{"key": "val"}).Return("grafana-to-prometheus", nil)
		controller := New(rtClient, scheme)
		controller.producerClientFactory = factoryMock
		controller.secretManager = secretMgrMock

		_, err := controller.Reconcile(testCtx, reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to update status after successful create")
	})

	t.Run("should return nil when SARE is being deleted but does not carry the correct finalizer", func(t *testing.T) {
		scheme := newTestScheme(t)
		now := metav1.NewTime(time.Now())
		sare := testSare
		sare.DeletionTimestamp = &now
		sare.Finalizers = []string{"some-other-controller/finalizer"} // fake client requires at least one finalizer when DeletionTimestamp is set
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&sare).Build()
		controller := New(rtClient, scheme)

		result, err := controller.Reconcile(testCtx, reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)
	})

	t.Run("should return error when removing finalizer fails", func(t *testing.T) {
		scheme := newTestScheme(t)
		now := metav1.NewTime(time.Now())
		sare := testSare
		sare.Finalizers = []string{finalizer}
		sare.DeletionTimestamp = &now
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(&sare).
			WithInterceptorFuncs(interceptor.Funcs{
				Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
					return errors.New("update denied")
				},
			}).
			Build()
		controller := New(rtClient, scheme)

		_, err := controller.Reconcile(testCtx, reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to remove finalizer")
	})

	t.Run("should forward spec.params to the producer Create call", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := testSare
		sare.Finalizers = []string{finalizer}
		sare.Spec.Params = map[string]string{"readOnly": "true", "scrapeInterval": "30s"}
		sapr := testSapr
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(&sare, &sapr).
			WithStatusSubresource(&serviceaccountv1.ServiceAccountRequest{}).
			Build()

		httpClientMock := newMockServiceAccountClient(t)
		httpClientMock.EXPECT().
			Create(testCtx, "grafana-ecosystem", producerclient.Params{"readOnly": "true", "scrapeInterval": "30s"}).
			Return(map[string]string{"apiKey": "abc"}, nil)
		factoryMock := newMockProducerClientFactory(t)
		factoryMock.EXPECT().NewForProducer(testCtx, "ecosystem", matchSAPR(testSapr)).Return(httpClientMock, nil)
		secretMgrMock := newMockSecretManager(t)
		secretMgrMock.EXPECT().Exists(testCtx, matchSARE(testSare)).Return(false, nil)
		secretMgrMock.EXPECT().CreateOrUpdate(testCtx, matchSARE(testSare), map[string]string{"apiKey": "abc"}).Return("grafana-to-prometheus", nil)
		controller := New(rtClient, scheme)
		controller.producerClientFactory = factoryMock
		controller.secretManager = secretMgrMock

		_, err := controller.Reconcile(testCtx, reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.NoError(t, err)
	})

	t.Run("should create secret and update status with Ready conditions on success", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := testSare
		sare.Finalizers = []string{finalizer}
		sapr := testSapr
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(&sare, &sapr).
			WithStatusSubresource(&serviceaccountv1.ServiceAccountRequest{}).
			Build()

		httpClientMock := newMockServiceAccountClient(t)
		httpClientMock.EXPECT().Create(testCtx, "grafana-ecosystem", producerclient.Params(nil)).Return(map[string]string{"username": "grafana-user", "password": "pass"}, nil)
		factoryMock := newMockProducerClientFactory(t)
		factoryMock.EXPECT().NewForProducer(testCtx, "ecosystem", matchSAPR(testSapr)).Return(httpClientMock, nil)

		secretMgrMock := newMockSecretManager(t)
		secretMgrMock.EXPECT().Exists(testCtx, matchSARE(testSare)).Return(false, nil)
		secretMgrMock.EXPECT().CreateOrUpdate(testCtx, matchSARE(testSare), map[string]string{"username": "grafana-user", "password": "pass"}).Return("grafana-to-prometheus", nil)

		controller := New(rtClient, scheme)
		controller.producerClientFactory = factoryMock
		controller.secretManager = secretMgrMock

		result, err := controller.Reconcile(testCtx, reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)

		var updated serviceaccountv1.ServiceAccountRequest
		require.NoError(t, rtClient.Get(testCtx, types.NamespacedName{Name: "grafana-to-prometheus", Namespace: "ecosystem"}, &updated))
		require.NotNil(t, updated.Status.SecretRef)
		assert.Equal(t, "grafana-to-prometheus", updated.Status.SecretRef.Name)

		saCond := findCondition(updated.Status.Conditions, serviceaccountv1.ConditionTypeServiceAccountReady)
		require.NotNil(t, saCond)
		assert.Equal(t, metav1.ConditionTrue, saCond.Status)
		assert.Equal(t, serviceaccountv1.ConditionReasonServiceAccountReadyCreated, saCond.Reason)
	})
}

func TestController_EnqueueRequestsForProducer(t *testing.T) {
	t.Run("should enqueue SAREs that reference the given producer", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare1 := testSare
		sare1.Spec.Optional = true
		sare2 := testSare
		sare2.Name = "loki-to-prometheus"
		sare2.Spec.Optional = true
		sare3 := testSare
		sare3.Name = "grafana-to-alertmanager"
		sare3.Spec.Producer = "alertmanager"
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&sare1, &sare2, &sare3).Build()
		controller := New(rtClient, scheme)

		sapr := testSapr
		requests := controller.enqueueRequestsForProducer(testCtx, &sapr)

		require.Len(t, requests, 2)
		names := []string{requests[0].Name, requests[1].Name}
		assert.Contains(t, names, "grafana-to-prometheus")
		assert.Contains(t, names, "loki-to-prometheus")
	})

	t.Run("should return empty list when no SAREs reference the producer", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := testSare
		sare.Name = "grafana-to-alertmanager"
		sare.Spec.Producer = "alertmanager"
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&sare).Build()
		controller := New(rtClient, scheme)

		sapr := testSapr
		requests := controller.enqueueRequestsForProducer(testCtx, &sapr)

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
		controller := New(rtClient, scheme)

		sapr := testSapr
		requests := controller.enqueueRequestsForProducer(testCtx, &sapr)

		assert.Empty(t, requests)
	})

	t.Run("should not enqueue SAREs from a different namespace", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := testSare
		sare.Namespace = "other-namespace"
		sare.Spec.Optional = true
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&sare).Build()
		controller := New(rtClient, scheme)

		sapr := testSapr
		requests := controller.enqueueRequestsForProducer(testCtx, &sapr)

		assert.Empty(t, requests)
	})
}
