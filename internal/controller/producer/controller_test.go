package producer

import (
	"context"
	"errors"
	"testing"

	serviceaccountv1 "github.com/cloudogu/k8s-serviceaccount-lib/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

var testCtx = context.Background()

func TestController_Reconcile(t *testing.T) {
	t.Run("should set Ready=True and requeue when the producer is reachable", func(t *testing.T) {
		sapr := newTestSAPR("example-producer", "default")
		rtClient := newClientWith(t, sapr)
		factory := newMockProducerClientFactory(t)
		saClient := newMockServiceAccountClient(t)
		saClient.EXPECT().Ready(testCtx).Return(nil)
		factory.EXPECT().NewForProducer(testCtx, "default", matchSAPR(sapr)).Return(saClient, nil)

		controller := New(rtClient)
		controller.clientFactory = factory

		result, err := controller.Reconcile(testCtx, reconcileRequest("example-producer", "default"))
		require.NoError(t, err)
		assert.Equal(t, readinessCheckInterval, result.RequeueAfter)

		cond := readyCondition(t, rtClient, "example-producer", "default")
		require.NotNil(t, cond)
		assert.Equal(t, metav1.ConditionTrue, cond.Status)
		assert.Equal(t, reasonProducerReady, cond.Reason)
	})

	t.Run("should set Ready=False with ConnectionFailed when the endpoint is unreachable", func(t *testing.T) {
		sapr := newTestSAPR("example-producer", "default")
		rtClient := newClientWith(t, sapr)
		factory := newMockProducerClientFactory(t)
		saClient := newMockServiceAccountClient(t)
		saClient.EXPECT().Ready(testCtx).Return(errors.New("connection refused"))
		factory.EXPECT().NewForProducer(testCtx, "default", matchSAPR(sapr)).Return(saClient, nil)

		controller := New(rtClient)
		controller.clientFactory = factory

		result, err := controller.Reconcile(testCtx, reconcileRequest("example-producer", "default"))
		require.NoError(t, err)
		assert.Equal(t, readinessCheckInterval, result.RequeueAfter)

		cond := readyCondition(t, rtClient, "example-producer", "default")
		require.NotNil(t, cond)
		assert.Equal(t, metav1.ConditionFalse, cond.Status)
		assert.Equal(t, serviceaccountv1.ConditionReadyReasonConnectionFailed, cond.Reason)
	})

	t.Run("should set Ready=False with AuthSecretNotFound when the client cannot be built", func(t *testing.T) {
		sapr := newTestSAPR("example-producer", "default")
		rtClient := newClientWith(t, sapr)
		factory := newMockProducerClientFactory(t)
		factory.EXPECT().NewForProducer(testCtx, "default", matchSAPR(sapr)).
			Return(nil, errors.New("failed to get auth secret"))

		controller := New(rtClient)
		controller.clientFactory = factory

		result, err := controller.Reconcile(testCtx, reconcileRequest("example-producer", "default"))
		require.NoError(t, err)
		assert.Equal(t, readinessCheckInterval, result.RequeueAfter)

		cond := readyCondition(t, rtClient, "example-producer", "default")
		require.NotNil(t, cond)
		assert.Equal(t, metav1.ConditionFalse, cond.Status)
		assert.Equal(t, serviceaccountv1.ConditionReadyReasonAuthSecretNotFound, cond.Reason)
	})

	t.Run("should set Ready=False with InvalidConfiguration when no HTTP spec is configured", func(t *testing.T) {
		sapr := newTestSAPR("example-producer", "default")
		sapr.Spec.HTTP = nil
		rtClient := newClientWith(t, sapr)
		// The factory is never consulted because the configuration is rejected first.
		controller := New(rtClient)
		controller.clientFactory = newMockProducerClientFactory(t)

		result, err := controller.Reconcile(testCtx, reconcileRequest("example-producer", "default"))
		require.NoError(t, err)
		assert.Equal(t, readinessCheckInterval, result.RequeueAfter)

		cond := readyCondition(t, rtClient, "example-producer", "default")
		require.NotNil(t, cond)
		assert.Equal(t, metav1.ConditionFalse, cond.Status)
		assert.Equal(t, serviceaccountv1.ConditionReadyReasonInvalidConfiguration, cond.Reason)
	})

	t.Run("should return error when status.notReady write fails", func(t *testing.T) {
		scheme := newTestScheme(t)
		sapr := newTestSAPR("example-producer", "default")
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(sapr).
			WithStatusSubresource(&serviceaccountv1.ServiceAccountProducer{}).
			WithInterceptorFuncs(interceptor.Funcs{
				SubResourcePatch: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
					return errors.New("status patch failed")
				},
			}).
			Build()
		factory := newMockProducerClientFactory(t)
		saClient := newMockServiceAccountClient(t)
		saClient.EXPECT().Ready(testCtx).Return(errors.New("connection refused"))
		factory.EXPECT().NewForProducer(testCtx, "default", matchSAPR(sapr)).Return(saClient, nil)

		controller := New(rtClient)
		controller.clientFactory = factory

		_, err := controller.Reconcile(testCtx, reconcileRequest("example-producer", "default"))

		require.Error(t, err)
		assert.Contains(t, err.Error(), "status patch failed")
	})

	t.Run("should return error when status.ready write fails", func(t *testing.T) {
		scheme := newTestScheme(t)
		sapr := newTestSAPR("example-producer", "default")
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(sapr).
			WithStatusSubresource(&serviceaccountv1.ServiceAccountProducer{}).
			WithInterceptorFuncs(interceptor.Funcs{
				SubResourcePatch: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
					return errors.New("status patch failed")
				},
			}).
			Build()
		factory := newMockProducerClientFactory(t)
		saClient := newMockServiceAccountClient(t)
		saClient.EXPECT().Ready(testCtx).Return(nil)
		factory.EXPECT().NewForProducer(testCtx, "default", matchSAPR(sapr)).Return(saClient, nil)

		controller := New(rtClient)
		controller.clientFactory = factory

		_, err := controller.Reconcile(testCtx, reconcileRequest("example-producer", "default"))

		require.Error(t, err)
		assert.Contains(t, err.Error(), "status patch failed")
	})

	t.Run("should ignore not found", func(t *testing.T) {
		rtClient := newClientWith(t)
		controller := New(rtClient)
		controller.clientFactory = newMockProducerClientFactory(t)

		result, err := controller.Reconcile(testCtx, reconcileRequest("missing-producer", "default"))
		require.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)
	})
}

func matchSAPR(sapr *serviceaccountv1.ServiceAccountProducer) any {
	return mock.MatchedBy(func(p *serviceaccountv1.ServiceAccountProducer) bool {
		return p != nil && p.Name == sapr.Name
	})
}

func reconcileRequest(name, namespace string) ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: name}}
}

func newClientWith(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().
		WithScheme(newTestScheme(t)).
		WithObjects(objs...).
		WithStatusSubresource(&serviceaccountv1.ServiceAccountProducer{}).
		Build()
}

func readyCondition(t *testing.T, rtClient client.Client, name, namespace string) *metav1.Condition {
	t.Helper()
	var updated serviceaccountv1.ServiceAccountProducer
	require.NoError(t, rtClient.Get(testCtx, types.NamespacedName{Namespace: namespace, Name: name}, &updated))
	return apimeta.FindStatusCondition(updated.Status.Conditions, serviceaccountv1.ConditionTypeReady)
}

func newTestSAPR(name, namespace string) *serviceaccountv1.ServiceAccountProducer {
	return &serviceaccountv1.ServiceAccountProducer{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: serviceaccountv1.ServiceAccountProducerSpec{
			Producer: name,
			HTTP: &serviceaccountv1.HTTPProducer{
				Endpoint: "https://nexus:8081/serviceaccounts",
				AuthSecret: serviceaccountv1.ServiceAccountProducerAuthSecret{
					LocalSecretRef: serviceaccountv1.LocalSecretRef{Name: name + "-auth"},
					Key:            "token",
				},
			},
		},
	}
}

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	scheme := runtime.NewScheme()
	metav1.AddToGroupVersion(scheme, serviceaccountv1.GroupVersion)
	require.NoError(t, serviceaccountv1.AddToScheme(scheme), "AddToScheme() failed")

	return scheme
}
