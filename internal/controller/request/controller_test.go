package request

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	serviceaccountv2 "github.com/cloudogu/k8s-serviceaccount-lib/v2/api/v2"
	"github.com/cloudogu/service-account-operator/internal/config"
	producerclient "github.com/cloudogu/service-account-operator/internal/producer"
	sa "github.com/cloudogu/service-account-operator/internal/serviceaccount"
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
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

var (
	testCtx        = context.Background()
	testSecretName = "grafana-prom-sa"
)

var testSare = serviceaccountv2.ServiceAccountRequest{
	ObjectMeta: metav1.ObjectMeta{Name: "grafana-to-prometheus", Namespace: "ecosystem"},
	Spec: serviceaccountv2.ServiceAccountRequestSpec{
		Consumer:     "grafana",
		ConsumerType: serviceaccountv2.DoguConsumerType,
		Producer:     "prometheus",
		Rotation:     &serviceaccountv2.ServiceAccountRotation{},
	},
}

var testSapr = serviceaccountv2.ServiceAccountProducer{
	ObjectMeta: metav1.ObjectMeta{Name: "prometheus", Namespace: "ecosystem"},
	Spec: serviceaccountv2.ServiceAccountProducerSpec{
		Producer: "prometheus",
		HTTP: &serviceaccountv2.HTTPProducer{
			Endpoint: "http://prometheus:9090/serviceaccounts",
			AuthSecret: serviceaccountv2.ServiceAccountProducerAuthSecret{
				LocalSecretRef: serviceaccountv2.LocalSecretRef{Name: "prometheus-sa-secret"},
				Key:            "apiKey",
			},
		},
	},
}

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, serviceaccountv2.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	return scheme
}

func reconcileRequest(name, namespace string) ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: name}}
}

func findCondition(conditions []metav1.Condition, condType string) *metav1.Condition {
	return apimeta.FindStatusCondition(conditions, condType)
}

func matchSARE(sare serviceaccountv2.ServiceAccountRequest) any {
	return mock.MatchedBy(func(s *serviceaccountv2.ServiceAccountRequest) bool {
		return s != nil && s.Name == sare.Name
	})
}

type fakeEventRecorder struct {
	*events.FakeRecorder
}

func newFakeRecorder(t *testing.T, expectedEvents []string) events.EventRecorder {
	t.Helper()
	recorder := fakeEventRecorder{events.NewFakeRecorder(len(expectedEvents) + 1)}
	t.Cleanup(func() {
		recorder.drainAndAssertExpected(t, expectedEvents)
	})
	return recorder
}

func (f *fakeEventRecorder) drainAndAssertExpected(t *testing.T, expectedEvents []string) {
	t.Helper()
	for _, expected := range expectedEvents {
		select {
		case actual := <-f.Events:
			assert.Equal(t, expected, actual)
		default:
			assert.Fail(t, "less than the expected number of events were recorded")
			return
		}
	}

	var recordedEvents []string
	defer func() {
		if len(recordedEvents) > 0 {
			assert.Failf(t, "more than the expected number of events were recorded", "Events: %s", recordedEvents)
		}
	}()
	for {
		select {
		case actual := <-f.Events:
			recordedEvents = append(recordedEvents, actual)
		default:
			return
		}
	}
}

func matchSAPR(sapr serviceaccountv2.ServiceAccountProducer) any {
	return mock.MatchedBy(func(p *serviceaccountv2.ServiceAccountProducer) bool {
		return p != nil && p.Name == sapr.Name
	})
}

var testOperatorConfig = &config.OperatorConfig{}

func TestController_Reconcile(t *testing.T) {
	t.Run("should ignore not found SARE", func(t *testing.T) {
		scheme := newTestScheme(t)
		rtClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		recorder := newFakeRecorder(t, nil)
		controller := New(rtClient, scheme, testOperatorConfig, recorder)

		result, err := controller.Reconcile(testCtx, reconcileRequest("missing", "ecosystem"))

		require.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)
	})

	t.Run("should fail on error when getting SARE", func(t *testing.T) {
		scheme := newTestScheme(t)
		rtClient := fake.NewClientBuilder().WithScheme(scheme).
			WithInterceptorFuncs(interceptor.Funcs{Get: func(ctx context.Context, client client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				return assert.AnError
			}}).Build()
		recorder := newFakeRecorder(t, nil)
		controller := New(rtClient, scheme, testOperatorConfig, recorder)

		result, err := controller.Reconcile(testCtx, reconcileRequest("get-error", "ecosystem"))

		assert.ErrorIs(t, err, assert.AnError)
		assert.ErrorContains(t, err, "failed to get service account request \"get-error\"")
		assert.Equal(t, ctrl.Result{}, result)
	})

	t.Run("should add finalizer when missing and continue reconciling in the same pass", func(t *testing.T) {
		scheme := newTestScheme(t)
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(new(testSare)).Build()
		recorder := newFakeRecorder(t, nil)
		controller := New(rtClient, scheme, testOperatorConfig, recorder)

		// The required producer is missing, so reconcile proceeds past the finalizer step and errors out.
		// This proves the finalizer addition no longer short-circuits the pass.
		_, err := controller.Reconcile(testCtx, reconcileRequest("grafana-to-prometheus", "ecosystem"))
		require.Error(t, err)

		var updated serviceaccountv2.ServiceAccountRequest
		require.NoError(t, rtClient.Get(testCtx, types.NamespacedName{Name: "grafana-to-prometheus", Namespace: "ecosystem"}, &updated))
		assert.Equal(t, []string{finalizer}, updated.Finalizers)
	})

	t.Run("should remove finalizer when SARE is being deleted", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := testSare
		sare.Finalizers = []string{finalizer}
		sare.DeletionTimestamp = new(metav1.NewTime(time.Now()))
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&sare).Build()
		recorder := newFakeRecorder(t, nil)
		controller := New(rtClient, scheme, testOperatorConfig, recorder)

		result, err := controller.Reconcile(testCtx, reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)

		// The fake client removes the object once the last finalizer is gone and deletionTimestamp is set.
		var updated serviceaccountv2.ServiceAccountRequest
		err = rtClient.Get(testCtx, types.NamespacedName{Name: "grafana-to-prometheus", Namespace: "ecosystem"}, &updated)
		if err == nil {
			assert.Empty(t, updated.Finalizers)
		}
	})

	t.Run("should return wrapped error when secretManager.Exists fails", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := testSare
		sare.Finalizers = []string{finalizer}
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&sare).Build()
		secretMgrMock := newMockSecretManager(t)
		secretMgrMock.EXPECT().Exists(testCtx, matchSARE(testSare)).Return(false, testSecretName, errors.New("storage error"))
		recorder := newFakeRecorder(t, nil)
		controller := New(rtClient, scheme, testOperatorConfig, recorder)
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
			WithStatusSubresource(&serviceaccountv2.ServiceAccountRequest{}).
			Build()
		secretMgrMock := newMockSecretManager(t)
		conflictErr := fmt.Errorf("%w: secret %q in namespace %q", sa.ErrSecretConflict, "grafana-to-prometheus", "ecosystem")
		secretMgrMock.EXPECT().Exists(testCtx, matchSARE(testSare)).Return(false, testSecretName, conflictErr)
		recorder := newFakeRecorder(t, nil)
		controller := New(rtClient, scheme, testOperatorConfig, recorder)
		controller.secretManager = secretMgrMock

		_, err := controller.Reconcile(testCtx, reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.ErrorIs(t, err, sa.ErrSecretConflict)
		var updated serviceaccountv2.ServiceAccountRequest
		require.NoError(t, rtClient.Get(testCtx, types.NamespacedName{Name: "grafana-to-prometheus", Namespace: "ecosystem"}, &updated))
		cond := findCondition(updated.Status.Conditions, serviceaccountv2.ConditionTypeServiceAccountReady)
		require.NotNil(t, cond)
		assert.Equal(t, metav1.ConditionFalse, cond.Status)
		assert.Equal(t, serviceaccountv2.ConditionReasonServiceAccountReadyFailed, cond.Reason)
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
			WithStatusSubresource(&serviceaccountv2.ServiceAccountRequest{}).
			Build()
		recorder := newFakeRecorder(t, nil)
		controller := New(rtClient, scheme, testOperatorConfig, recorder)

		result, err := controller.Reconcile(testCtx, reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)

		var updated serviceaccountv2.ServiceAccountRequest
		require.NoError(t, rtClient.Get(testCtx, types.NamespacedName{Name: "grafana-to-prometheus", Namespace: "ecosystem"}, &updated))
		cond := findCondition(updated.Status.Conditions, serviceaccountv2.ConditionTypeServiceAccountReady)
		require.NotNil(t, cond)
		assert.Equal(t, metav1.ConditionFalse, cond.Status)
		assert.Equal(t, serviceaccountv2.ConditionReasonServiceAccountReadyProducerNotFound, cond.Reason)
	})

	t.Run("should return error for required SARE when producer is not found", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := testSare
		sare.Finalizers = []string{finalizer}
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&sare).Build()
		recorder := newFakeRecorder(t, nil)
		controller := New(rtClient, scheme, testOperatorConfig, recorder)

		_, err := controller.Reconcile(testCtx, reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.Error(t, err)
	})

	t.Run("should return error and set ServiceAccountReady=False when factory fails to build client", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := testSare
		sare.Finalizers = []string{finalizer}
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(&sare, new(testSapr)).
			WithStatusSubresource(&serviceaccountv2.ServiceAccountRequest{}).
			Build()

		factoryMock := newMockProducerClientFactory(t)
		factoryMock.EXPECT().NewForProducer(testCtx, "ecosystem", matchSAPR(testSapr)).
			Return(nil, errors.New("auth secret not found"))
		recorder := newFakeRecorder(t, nil)
		controller := New(rtClient, scheme, testOperatorConfig, recorder)
		controller.producerClientFactory = factoryMock

		_, err := controller.Reconcile(testCtx, reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.Error(t, err)
		var updated serviceaccountv2.ServiceAccountRequest
		require.NoError(t, rtClient.Get(testCtx, types.NamespacedName{Name: "grafana-to-prometheus", Namespace: "ecosystem"}, &updated))
		cond := findCondition(updated.Status.Conditions, serviceaccountv2.ConditionTypeServiceAccountReady)
		require.NotNil(t, cond)
		assert.Equal(t, metav1.ConditionFalse, cond.Status)
		assert.Equal(t, serviceaccountv2.ConditionReasonServiceAccountReadyFailed, cond.Reason)
	})

	t.Run("should return error and set ServiceAccountReady=False when HTTP client fails", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := testSare
		sare.Finalizers = []string{finalizer}
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(&sare, new(testSapr)).
			WithStatusSubresource(&serviceaccountv2.ServiceAccountRequest{}).
			Build()

		httpClientMock := newMockServiceAccountClient(t)
		httpClientMock.EXPECT().CreateOrUpdate(testCtx, "grafana-ecosystem", producerclient.Params(nil), producerclient.BehaviorParams{RotateServiceAccountNow: true}).Return(nil, errors.New("connection refused"))
		factoryMock := newMockProducerClientFactory(t)
		factoryMock.EXPECT().NewForProducer(testCtx, "ecosystem", matchSAPR(testSapr)).Return(httpClientMock, nil)
		recorder := newFakeRecorder(t, nil)
		controller := New(rtClient, scheme, testOperatorConfig, recorder)
		controller.producerClientFactory = factoryMock

		_, err := controller.Reconcile(testCtx, reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.Error(t, err)
		var updated serviceaccountv2.ServiceAccountRequest
		require.NoError(t, rtClient.Get(testCtx, types.NamespacedName{Name: "grafana-to-prometheus", Namespace: "ecosystem"}, &updated))
		cond := findCondition(updated.Status.Conditions, serviceaccountv2.ConditionTypeServiceAccountReady)
		require.NotNil(t, cond)
		assert.Equal(t, metav1.ConditionFalse, cond.Status)
	})

	t.Run("should return error when adding finalizer fails", func(t *testing.T) {
		scheme := newTestScheme(t)
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(new(testSare)).
			WithInterceptorFuncs(interceptor.Funcs{
				Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
					return errors.New("etcd unavailable")
				},
			}).
			Build()
		recorder := newFakeRecorder(t, nil)
		controller := New(rtClient, scheme, testOperatorConfig, recorder)

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
					if _, ok := obj.(*serviceaccountv2.ServiceAccountProducer); ok {
						return errors.New("error while getting producer")
					}
					return c.Get(ctx, key, obj, opts...)
				},
			}).
			Build()
		recorder := newFakeRecorder(t, nil)
		controller := New(rtClient, scheme, testOperatorConfig, recorder)

		_, err := controller.Reconcile(testCtx, reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get producer")
	})

	t.Run("should return error and set ServiceAccountReady=False when secret storage fails", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := testSare
		sare.Finalizers = []string{finalizer}
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(&sare, new(testSapr)).
			WithStatusSubresource(&serviceaccountv2.ServiceAccountRequest{}).
			Build()
		httpClientMock := newMockServiceAccountClient(t)
		httpClientMock.EXPECT().CreateOrUpdate(testCtx, "grafana-ecosystem", producerclient.Params(nil), producerclient.BehaviorParams{RotateServiceAccountNow: true}).
			Return(map[string]string{"key": "val"}, nil)
		factoryMock := newMockProducerClientFactory(t)
		factoryMock.EXPECT().NewForProducer(testCtx, "ecosystem", matchSAPR(testSapr)).Return(httpClientMock, nil)
		secretMgrMock := newMockSecretManager(t)
		secretMgrMock.EXPECT().Exists(testCtx, matchSARE(testSare)).Return(false, testSecretName, nil)
		secretMgrMock.EXPECT().CreateOrUpdate(testCtx, matchSARE(testSare), map[string]string{"key": "val"}).
			Return("", errors.New("disk full"))
		recorder := newFakeRecorder(t, nil)
		controller := New(rtClient, scheme, testOperatorConfig, recorder)
		controller.producerClientFactory = factoryMock
		controller.secretManager = secretMgrMock

		_, err := controller.Reconcile(testCtx, reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to store credentials")
		var updated serviceaccountv2.ServiceAccountRequest
		require.NoError(t, rtClient.Get(testCtx, types.NamespacedName{Name: "grafana-to-prometheus", Namespace: "ecosystem"}, &updated))
		cond := findCondition(updated.Status.Conditions, serviceaccountv2.ConditionTypeServiceAccountReady)
		require.NotNil(t, cond)
		assert.Equal(t, metav1.ConditionFalse, cond.Status)
	})

	t.Run("should return original error when fail() cannot update the status condition", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := testSare
		sare.Finalizers = []string{finalizer}
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(&sare, new(testSapr)).
			WithStatusSubresource(&serviceaccountv2.ServiceAccountRequest{}).
			WithInterceptorFuncs(interceptor.Funcs{
				SubResourcePatch: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
					return errors.New("status patch failed")
				},
			}).
			Build()
		factoryMock := newMockProducerClientFactory(t)
		factoryMock.EXPECT().NewForProducer(testCtx, "ecosystem", matchSAPR(testSapr)).
			Return(nil, errors.New("auth secret not found"))
		recorder := newFakeRecorder(t, nil)
		controller := New(rtClient, scheme, testOperatorConfig, recorder)
		controller.producerClientFactory = factoryMock

		_, err := controller.Reconcile(testCtx, reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.Error(t, err)
		assert.Contains(t, err.Error(), "auth secret not found")
	})

	t.Run("should return error when serviceAccountReady status update fails after successful create", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := testSare
		sare.Finalizers = []string{finalizer}
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(&sare, new(testSapr)).
			WithStatusSubresource(&serviceaccountv2.ServiceAccountRequest{}).
			WithInterceptorFuncs(interceptor.Funcs{
				SubResourcePatch: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
					return errors.New("status patch failed")
				},
			}).
			Build()
		httpClientMock := newMockServiceAccountClient(t)
		httpClientMock.EXPECT().CreateOrUpdate(testCtx, "grafana-ecosystem", producerclient.Params(nil), producerclient.BehaviorParams{RotateServiceAccountNow: true}).
			Return(map[string]string{"key": "val"}, nil)
		factoryMock := newMockProducerClientFactory(t)
		factoryMock.EXPECT().NewForProducer(testCtx, "ecosystem", matchSAPR(testSapr)).Return(httpClientMock, nil)
		secretMgrMock := newMockSecretManager(t)
		secretMgrMock.EXPECT().Exists(testCtx, matchSARE(testSare)).Return(false, testSecretName, nil)
		secretMgrMock.EXPECT().CreateOrUpdate(testCtx, matchSARE(testSare), map[string]string{"key": "val"}).Return("grafana-to-prometheus", nil)
		recorder := newFakeRecorder(t, []string{"Normal ServiceAccountRequest Created service account \"grafana\""})
		controller := New(rtClient, scheme, testOperatorConfig, recorder)
		controller.producerClientFactory = factoryMock
		controller.secretManager = secretMgrMock

		_, err := controller.Reconcile(testCtx, reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to update status after successful create")
	})

	t.Run("should return nil when SARE is being deleted but does not carry the correct finalizer", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := testSare
		sare.DeletionTimestamp = new(metav1.NewTime(time.Now()))
		sare.Finalizers = []string{"some-other-controller/finalizer"} // fake client requires at least one finalizer when DeletionTimestamp is set
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&sare).Build()
		recorder := newFakeRecorder(t, nil)
		controller := New(rtClient, scheme, testOperatorConfig, recorder)

		result, err := controller.Reconcile(testCtx, reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)
	})

	t.Run("should return error when removing finalizer fails", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := testSare
		sare.Finalizers = []string{finalizer}
		sare.DeletionTimestamp = new(metav1.NewTime(time.Now()))
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(&sare).
			WithInterceptorFuncs(interceptor.Funcs{
				Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
					return errors.New("update denied")
				},
			}).
			Build()
		recorder := newFakeRecorder(t, nil)
		controller := New(rtClient, scheme, testOperatorConfig, recorder)

		_, err := controller.Reconcile(testCtx, reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to remove finalizer")
	})

	t.Run("should forward spec.params to the producer Create call", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := testSare
		sare.Finalizers = []string{finalizer}
		sare.Spec.Params = map[string]string{"readOnly": "true", "scrapeInterval": "30s"}
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(&sare, new(testSapr)).
			WithStatusSubresource(&serviceaccountv2.ServiceAccountRequest{}).
			Build()

		httpClientMock := newMockServiceAccountClient(t)
		httpClientMock.EXPECT().
			CreateOrUpdate(testCtx, "grafana-ecosystem", producerclient.Params{"readOnly": "true", "scrapeInterval": "30s"}, producerclient.BehaviorParams{RotateServiceAccountNow: true}).
			Return(map[string]string{"apiKey": "abc"}, nil)
		factoryMock := newMockProducerClientFactory(t)
		factoryMock.EXPECT().NewForProducer(testCtx, "ecosystem", matchSAPR(testSapr)).Return(httpClientMock, nil)
		secretMgrMock := newMockSecretManager(t)
		secretMgrMock.EXPECT().Exists(testCtx, matchSARE(testSare)).Return(false, testSecretName, nil)
		secretMgrMock.EXPECT().CreateOrUpdate(testCtx, matchSARE(testSare), map[string]string{"apiKey": "abc"}).Return("grafana-to-prometheus", nil)
		recorder := newFakeRecorder(t, []string{"Normal ServiceAccountRequest Created service account \"grafana\""})
		controller := New(rtClient, scheme, testOperatorConfig, recorder)
		controller.producerClientFactory = factoryMock
		controller.secretManager = secretMgrMock

		_, err := controller.Reconcile(testCtx, reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.NoError(t, err)
	})

	t.Run("should create secret and update status with Ready conditions on success", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := testSare
		sare.Finalizers = []string{finalizer}
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(&sare, new(testSapr)).
			WithStatusSubresource(&serviceaccountv2.ServiceAccountRequest{}).
			Build()

		httpClientMock := newMockServiceAccountClient(t)
		httpClientMock.EXPECT().CreateOrUpdate(testCtx, "grafana-ecosystem", producerclient.Params(nil), producerclient.BehaviorParams{RotateServiceAccountNow: true}).Return(map[string]string{"username": "grafana-user", "password": "pass"}, nil)
		factoryMock := newMockProducerClientFactory(t)
		factoryMock.EXPECT().NewForProducer(testCtx, "ecosystem", matchSAPR(testSapr)).Return(httpClientMock, nil)

		secretMgrMock := newMockSecretManager(t)
		secretMgrMock.EXPECT().Exists(testCtx, matchSARE(testSare)).Return(false, testSecretName, nil)
		secretMgrMock.EXPECT().CreateOrUpdate(testCtx, matchSARE(testSare), map[string]string{"username": "grafana-user", "password": "pass"}).Return("grafana-to-prometheus", nil)

		recorder := newFakeRecorder(t, []string{"Normal ServiceAccountRequest Created service account \"grafana\""})
		controller := New(rtClient, scheme, testOperatorConfig, recorder)
		controller.producerClientFactory = factoryMock
		controller.secretManager = secretMgrMock

		result, err := controller.Reconcile(testCtx, reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)

		var updated serviceaccountv2.ServiceAccountRequest
		require.NoError(t, rtClient.Get(testCtx, types.NamespacedName{Name: "grafana-to-prometheus", Namespace: "ecosystem"}, &updated))
		require.NotNil(t, updated.Status.SecretRef)
		assert.Equal(t, "grafana-to-prometheus", updated.Status.SecretRef.Name)

		saCond := findCondition(updated.Status.Conditions, serviceaccountv2.ConditionTypeServiceAccountReady)
		require.NotNil(t, saCond)
		assert.Equal(t, metav1.ConditionTrue, saCond.Status)
		assert.Equal(t, serviceaccountv2.ConditionReasonServiceAccountReadyCreated, saCond.Reason)
	})
	t.Run("should successful set rotation watcher if rotation is enabled", func(t *testing.T) {
		// given

		// when

		// then
		assert.Fail(t, "implement me")
	})
	t.Run("should error on setting rotation watcher if cron syntax is invalid", func(t *testing.T) {
		// given

		// when

		// then
		assert.Fail(t, "implement me")
	})

	t.Run("should not update secret if the credentials did not change", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := testSare
		sare.Finalizers = []string{finalizer}
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(&sare, new(testSapr)).
			WithStatusSubresource(&serviceaccountv2.ServiceAccountRequest{}).
			Build()

		httpClientMock := newMockServiceAccountClient(t)
		httpClientMock.EXPECT().CreateOrUpdate(testCtx, "grafana-ecosystem", producerclient.Params(nil), producerclient.BehaviorParams{RotateServiceAccountNow: true}).Return(nil, nil)
		factoryMock := newMockProducerClientFactory(t)
		factoryMock.EXPECT().NewForProducer(testCtx, "ecosystem", matchSAPR(testSapr)).Return(httpClientMock, nil)

		secretMgrMock := newMockSecretManager(t)
		secretMgrMock.EXPECT().Exists(testCtx, matchSARE(testSare)).Return(false, sare.Name, nil)

		recorder := newFakeRecorder(t, nil)
		controller := New(rtClient, scheme, testOperatorConfig, recorder)
		controller.producerClientFactory = factoryMock
		controller.secretManager = secretMgrMock

		result, err := controller.Reconcile(testCtx, reconcileRequest("grafana-to-prometheus", "ecosystem"))

		require.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)

		var updated serviceaccountv2.ServiceAccountRequest
		require.NoError(t, rtClient.Get(testCtx, types.NamespacedName{Name: "grafana-to-prometheus", Namespace: "ecosystem"}, &updated))
		require.NotNil(t, updated.Status.SecretRef)
		assert.Equal(t, "grafana-to-prometheus", updated.Status.SecretRef.Name)

		saCond := findCondition(updated.Status.Conditions, serviceaccountv2.ConditionTypeServiceAccountReady)
		require.NotNil(t, saCond)
		assert.Equal(t, metav1.ConditionTrue, saCond.Status)
		assert.Equal(t, serviceaccountv2.ConditionReasonServiceAccountReadyCreated, saCond.Reason)
	})
}

func TestController_deleteSaRotationWatcher(t *testing.T) {
	t.Run("should return just fine if watcher does not contain consumer", func(t *testing.T) {
		// given

		// when

		// then
		assert.Fail(t, "implement me")
	})

	t.Run("should remove consumer from watcher map", func(t *testing.T) {
		// given

		// when

		// then
		assert.Fail(t, "implement me")
	})
}
func TestController_setSaRotationWatcher(t *testing.T) {
	t.Run("should add consumer to watcher on new creation", func(t *testing.T) {
		// given

		// when

		// then
		assert.Fail(t, "implement me")
	})

	t.Run("should replace consumer in watcher on update", func(t *testing.T) {
		// given

		// when

		// then
		assert.Fail(t, "implement me")
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
		recorder := newFakeRecorder(t, nil)
		controller := New(rtClient, scheme, testOperatorConfig, recorder)

		requests := controller.enqueueRequestsForProducer(testCtx, new(testSapr))

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
		recorder := newFakeRecorder(t, nil)
		controller := New(rtClient, scheme, testOperatorConfig, recorder)

		requests := controller.enqueueRequestsForProducer(testCtx, new(testSapr))

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
		recorder := newFakeRecorder(t, nil)
		controller := New(rtClient, scheme, testOperatorConfig, recorder)

		requests := controller.enqueueRequestsForProducer(testCtx, new(testSapr))

		assert.Empty(t, requests)
	})

	t.Run("should not enqueue SAREs from a different namespace", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := testSare
		sare.Namespace = "other-namespace"
		sare.Spec.Optional = true
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&sare).Build()
		recorder := newFakeRecorder(t, nil)
		controller := New(rtClient, scheme, testOperatorConfig, recorder)

		requests := controller.enqueueRequestsForProducer(testCtx, new(testSapr))

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
		client                func(t *testing.T) (client.Client, *serviceaccountv2.ServiceAccountProducer)
		producerClientFactory func(t *testing.T, sapr *serviceaccountv2.ServiceAccountProducer) producerClientFactory
		operatorConfig        *config.OperatorConfig
		eventRecorder         func(t *testing.T) events.EventRecorder
	}
	type args struct {
		sare *serviceaccountv2.ServiceAccountRequest
	}

	testSare := &serviceaccountv2.ServiceAccountRequest{ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: testReqName, DeletionTimestamp: new(metav1.NewTime(time.Now())), Finalizers: []string{"k8s.cloudogu.com/service-account-request-finalizer"}}, Spec: serviceaccountv2.ServiceAccountRequestSpec{Producer: testPrName, Consumer: testConsumer}}

	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name: "should skip if no finalizer is present",
			args: args{
				sare: &serviceaccountv2.ServiceAccountRequest{},
			},
			wantErr: assert.NoError,
		},
		{
			name: "should remove finalizer if timeout is reached",
			args: args{
				sare: testSare,
			},
			fields: fields{
				client: func(t *testing.T) (client.Client, *serviceaccountv2.ServiceAccountProducer) {
					sClient := newMockK8sClient(t)
					expectClientEmptyFinalizerSare(t, sClient, nil)
					return sClient, nil
				},
				operatorConfig: &config.OperatorConfig{DeletionTimeout: time.Nanosecond},
			},
			wantErr: assert.NoError,
		},
		{
			name: "should return nil on successful deletion",
			args: args{
				sare: testSare,
			},
			fields: fields{
				client: func(t *testing.T) (client.Client, *serviceaccountv2.ServiceAccountProducer) {
					sClient := newMockK8sClient(t)

					testSapr := &serviceaccountv2.ServiceAccountProducer{ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: testPrName}}
					expectClientGetProducer(sClient, testSapr, nil)
					expectClientEmptyFinalizerSare(t, sClient, nil)
					expectClientPatchStatus(t, sClient, nil)

					return sClient, testSapr
				},
				producerClientFactory: func(t *testing.T, sapr *serviceaccountv2.ServiceAccountProducer) producerClientFactory {
					return mockProducerFactory(t, sapr, true, nil, nil, nil)
				},
				eventRecorder: func(t *testing.T) events.EventRecorder {
					return newFakeRecorder(t, []string{"Normal ServiceAccountRequest Deleted service account \"grafana\""})
				},
			},
			wantErr: assert.NoError,
		},
		{
			name: "should return nil if producer is not found",
			args: args{
				sare: testSare,
			},
			fields: fields{
				client: func(t *testing.T) (client.Client, *serviceaccountv2.ServiceAccountProducer) {
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
				client: func(t *testing.T) (client.Client, *serviceaccountv2.ServiceAccountProducer) {
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
				client: func(t *testing.T) (client.Client, *serviceaccountv2.ServiceAccountProducer) {
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
				client: func(t *testing.T) (client.Client, *serviceaccountv2.ServiceAccountProducer) {
					sClient := newMockK8sClient(t)
					expectClientGetProducer(sClient, nil, assert.AnError)
					expectClientErrorStatusSare(t, sClient)
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
				client: func(t *testing.T) (client.Client, *serviceaccountv2.ServiceAccountProducer) {
					sClient := newMockK8sClient(t)
					testSapr := &serviceaccountv2.ServiceAccountProducer{ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: testPrName}}
					expectClientGetProducer(sClient, testSapr, nil)
					expectClientErrorStatusSare(t, sClient)
					return sClient, testSapr
				},
				producerClientFactory: func(t *testing.T, sapr *serviceaccountv2.ServiceAccountProducer) producerClientFactory {
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
				client: func(t *testing.T) (client.Client, *serviceaccountv2.ServiceAccountProducer) {
					sClient := newMockK8sClient(t)
					testSapr := &serviceaccountv2.ServiceAccountProducer{ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: testPrName}}
					expectClientGetProducer(sClient, testSapr, nil)
					expectClientErrorStatusSare(t, sClient)
					return sClient, testSapr
				},
				producerClientFactory: func(t *testing.T, sapr *serviceaccountv2.ServiceAccountProducer) producerClientFactory {
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
				client: func(t *testing.T) (client.Client, *serviceaccountv2.ServiceAccountProducer) {
					sClient := newMockK8sClient(t)
					testSapr := &serviceaccountv2.ServiceAccountProducer{ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: testPrName}}

					expectClientGetProducer(sClient, testSapr, nil)
					expectClientEmptyFinalizerSare(t, sClient, nil)

					return sClient, testSapr
				},
				producerClientFactory: func(t *testing.T, sapr *serviceaccountv2.ServiceAccountProducer) producerClientFactory {
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
				client: func(t *testing.T) (client.Client, *serviceaccountv2.ServiceAccountProducer) {
					sClient := newMockK8sClient(t)
					testSapr := &serviceaccountv2.ServiceAccountProducer{ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: testPrName}}
					expectClientGetProducer(sClient, testSapr, nil)
					expectClientErrorStatusSare(t, sClient)

					return sClient, testSapr
				},
				producerClientFactory: func(t *testing.T, sapr *serviceaccountv2.ServiceAccountProducer) producerClientFactory {
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
				client: func(t *testing.T) (client.Client, *serviceaccountv2.ServiceAccountProducer) {
					sClient := newMockK8sClient(t)
					testSapr := &serviceaccountv2.ServiceAccountProducer{ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: testPrName}}
					expectClientGetProducer(sClient, testSapr, nil)
					expectClientPatchStatus(t, sClient, assert.AnError)
					expectClientEmptyFinalizerSare(t, sClient, nil)

					return sClient, testSapr
				},
				producerClientFactory: func(t *testing.T, sapr *serviceaccountv2.ServiceAccountProducer) producerClientFactory {
					return mockProducerFactory(t, sapr, true, nil, nil, nil)
				},
				eventRecorder: func(t *testing.T) events.EventRecorder {
					return newFakeRecorder(t, []string{"Normal ServiceAccountRequest Deleted service account \"grafana\""})
				},
			},
			wantErr: assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Controller{}
			sapr := &serviceaccountv2.ServiceAccountProducer{}
			ctx := context.Background()
			if tt.fields.client != nil {
				c.client, sapr = tt.fields.client(t)
			}
			if tt.fields.producerClientFactory != nil {
				c.producerClientFactory = tt.fields.producerClientFactory(t, sapr)
			}
			if tt.fields.eventRecorder != nil {
				c.eventRecorder = tt.fields.eventRecorder(t)
			}

			c.operatorConfig = &config.OperatorConfig{DeletionTimeout: time.Minute}
			if tt.fields.operatorConfig != nil {
				c.operatorConfig = tt.fields.operatorConfig
			}

			sare := &serviceaccountv2.ServiceAccountRequest{}
			if tt.args.sare != nil {
				sare = tt.args.sare.DeepCopy()
			}

			tt.wantErr(t, c.reconcileDelete(ctx, sare), fmt.Sprintf("reconcileDelete(%v, %v)", ctx, tt.args.sare))
		})
	}
}

func expectClientGetProducer(c *mockK8sClient, sapr *serviceaccountv2.ServiceAccountProducer, err error) {
	c.EXPECT().Get(mock.Anything, types.NamespacedName{Namespace: testNamespace, Name: testPrName}, mock.IsType(&serviceaccountv2.ServiceAccountProducer{})).
		Run(func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) {
			if err == nil && sapr != nil {
				*obj.(*serviceaccountv2.ServiceAccountProducer) = *sapr
			}
		}).Return(err)
}

func expectClientEmptyFinalizerSare(t *testing.T, c *mockK8sClient, err error) {
	c.EXPECT().Update(mock.Anything, mock.IsType(&serviceaccountv2.ServiceAccountRequest{})).
		Run(func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) {
			if err == nil {
				updatedSaReq := obj.(*serviceaccountv2.ServiceAccountRequest)
				assert.Empty(t, updatedSaReq.Finalizers)
			}
		}).Return(err)
}

func expectClientErrorStatusSare(t *testing.T, c *mockK8sClient) {
	statusClient := newMockStatusClient(t)
	statusClient.EXPECT().Patch(mock.Anything, mock.IsType(&serviceaccountv2.ServiceAccountRequest{}), mock.Anything).
		Run(func(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) {
			data, patchErr := patch.Data(obj)
			require.NoError(t, patchErr)
			s := string(data)
			assert.Contains(t, s, `{"status":{"conditions"`)
		}).Return(nil)
	c.EXPECT().Status().Return(statusClient)
}

func expectClientPatchStatus(t *testing.T, c *mockK8sClient, err error) {
	statusClient := newMockStatusClient(t)
	statusClient.EXPECT().Patch(mock.Anything, mock.IsType(&serviceaccountv2.ServiceAccountProducer{}), mock.Anything).
		Run(func(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) {
			if err == nil {
				data, patchErr := patch.Data(obj)
				require.NoError(t, patchErr)
				assert.Contains(t, string(data), `{"status":{"lastExecution"`)
			}
		}).Return(err)
	c.EXPECT().Status().Return(statusClient)
}

func mockProducerFactory(t *testing.T, sapr *serviceaccountv2.ServiceAccountProducer, exists bool, existsErr, deleteErr, factoryErr error) producerClientFactory {
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

func Test_wasDeletedPredicate(t *testing.T) {
	sut := wasDeletedPredicate()
	assert.True(t, sut.Delete(event.TypedDeleteEvent[client.Object]{}))
	assert.False(t, sut.Create(event.TypedCreateEvent[client.Object]{}))
	assert.False(t, sut.Update(event.TypedUpdateEvent[client.Object]{}))
	assert.False(t, sut.Generic(event.TypedGenericEvent[client.Object]{}))
}

func Test_producerGotReadyPredicate(t *testing.T) {
	sut := producerGotReadyPredicate()
	tests := []struct {
		name                 string
		objectOld, objectNew client.Object
		want                 bool
	}{
		{
			name:      "should be false if old object has wrong type",
			objectOld: &corev1.Pod{},
			want:      false,
		},
		{
			name:      "should be false if new object has wrong type",
			objectOld: &serviceaccountv2.ServiceAccountProducer{},
			objectNew: &corev1.Pod{},
			want:      false,
		},
		{
			name: "should be false if ready condition changes from true to false",
			objectOld: &serviceaccountv2.ServiceAccountProducer{Status: serviceaccountv2.ServiceAccountProducerStatus{
				Conditions: []metav1.Condition{{
					Type:   serviceaccountv2.ConditionTypeReady,
					Status: metav1.ConditionTrue,
				}},
			}},
			objectNew: &serviceaccountv2.ServiceAccountProducer{Status: serviceaccountv2.ServiceAccountProducerStatus{
				Conditions: []metav1.Condition{{
					Type:   serviceaccountv2.ConditionTypeReady,
					Status: metav1.ConditionFalse,
				}},
			}},
			want: false,
		},
		{
			name: "should be true if ready condition changes from false to true",
			objectOld: &serviceaccountv2.ServiceAccountProducer{Status: serviceaccountv2.ServiceAccountProducerStatus{
				Conditions: []metav1.Condition{{
					Type:   serviceaccountv2.ConditionTypeReady,
					Status: metav1.ConditionFalse,
				}},
			}},
			objectNew: &serviceaccountv2.ServiceAccountProducer{Status: serviceaccountv2.ServiceAccountProducerStatus{
				Conditions: []metav1.Condition{{
					Type:   serviceaccountv2.ConditionTypeReady,
					Status: metav1.ConditionTrue,
				}},
			}},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, sut.Update(event.TypedUpdateEvent[client.Object]{
				ObjectOld: tt.objectOld,
				ObjectNew: tt.objectNew,
			}))
		})
	}
}

func Test_namespacedName_returnsCompoundName(t *testing.T) {
	actual := namespacedName(&testSare)

	assert.Equal(t, "grafana-to-prometheus-ecosystem", actual)
}

func TestController_removeFinalizer(t *testing.T) {
	t.Run("should return when finalizer is already removed", func(t *testing.T) {
		// given
		sare := &serviceaccountv2.ServiceAccountRequest{ObjectMeta: metav1.ObjectMeta{Finalizers: nil}}
		fakeClient := fake.NewClientBuilder().WithInterceptorFuncs(
			interceptor.Funcs{Update: func(ctx context.Context, client client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				assert.Fail(t, "should not be called")
				return nil
			}},
		).Build()
		controller := &Controller{client: fakeClient}

		// when
		err := controller.removeFinalizer(testCtx, sare)

		// then
		require.NoError(t, err)
	})
}
