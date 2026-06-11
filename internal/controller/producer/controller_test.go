package producer

import (
	"context"
	"errors"
	"testing"

	serviceaccountv1 "github.com/cloudogu/k8s-serviceaccount-lib/api/v1"
	producerclient "github.com/cloudogu/service-account-operator/internal/producer"
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
)

// fakeClient is a hand-written test double for producerclient.ServiceAccountClient.
// A generated mock is not used because that interface lives in another package, where mockery
// places its mock in a non-importable _test.go file.
type fakeClient struct {
	readyErr error
}

func (f *fakeClient) Create(_ context.Context, _ string, _ producerclient.Params) (map[string]string, error) {
	return nil, nil
}

func (f *fakeClient) Update(_ context.Context, _ string, _ producerclient.Params) (map[string]string, error) {
	return nil, nil
}

func (f *fakeClient) Delete(_ context.Context, _ string) error { return nil }

func (f *fakeClient) Ready(_ context.Context) error { return f.readyErr }

func TestController_Reconcile(t *testing.T) {
	t.Run("should set Ready=True and requeue when the producer is reachable", func(t *testing.T) {
		rtClient := newClientWith(t, newTestSAPR("example-producer", "default"))
		factory := newMockProducerClientFactory(t)
		factory.EXPECT().NewForProducer(mock.Anything, "default", mock.Anything).Return(&fakeClient{}, nil)

		controller := New(rtClient)
		controller.clientFactory = factory

		result, err := controller.Reconcile(context.Background(), reconcileRequest("example-producer", "default"))
		require.NoError(t, err)
		assert.Equal(t, readinessCheckInterval, result.RequeueAfter)

		cond := readyCondition(t, rtClient, "example-producer", "default")
		require.NotNil(t, cond)
		assert.Equal(t, metav1.ConditionTrue, cond.Status)
		assert.Equal(t, reasonProducerReady, cond.Reason)
	})

	t.Run("should set Ready=False with ConnectionFailed when the endpoint is unreachable", func(t *testing.T) {
		rtClient := newClientWith(t, newTestSAPR("example-producer", "default"))
		factory := newMockProducerClientFactory(t)
		factory.EXPECT().NewForProducer(mock.Anything, "default", mock.Anything).
			Return(&fakeClient{readyErr: errors.New("connection refused")}, nil)

		controller := New(rtClient)
		controller.clientFactory = factory

		result, err := controller.Reconcile(context.Background(), reconcileRequest("example-producer", "default"))
		require.NoError(t, err)
		assert.Equal(t, readinessCheckInterval, result.RequeueAfter)

		cond := readyCondition(t, rtClient, "example-producer", "default")
		require.NotNil(t, cond)
		assert.Equal(t, metav1.ConditionFalse, cond.Status)
		assert.Equal(t, serviceaccountv1.ConditionReadyReasonConnectionFailed, cond.Reason)
	})

	t.Run("should set Ready=False with AuthSecretNotFound when the client cannot be built", func(t *testing.T) {
		rtClient := newClientWith(t, newTestSAPR("example-producer", "default"))
		factory := newMockProducerClientFactory(t)
		factory.EXPECT().NewForProducer(mock.Anything, "default", mock.Anything).
			Return(nil, errors.New("failed to get auth secret"))

		controller := New(rtClient)
		controller.clientFactory = factory

		result, err := controller.Reconcile(context.Background(), reconcileRequest("example-producer", "default"))
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

		result, err := controller.Reconcile(context.Background(), reconcileRequest("example-producer", "default"))
		require.NoError(t, err)
		assert.Equal(t, readinessCheckInterval, result.RequeueAfter)

		cond := readyCondition(t, rtClient, "example-producer", "default")
		require.NotNil(t, cond)
		assert.Equal(t, metav1.ConditionFalse, cond.Status)
		assert.Equal(t, serviceaccountv1.ConditionReadyReasonInvalidConfiguration, cond.Reason)
	})

	t.Run("should ignore not found", func(t *testing.T) {
		rtClient := newClientWith(t)
		controller := New(rtClient)
		controller.clientFactory = newMockProducerClientFactory(t)

		result, err := controller.Reconcile(context.Background(), reconcileRequest("missing-producer", "default"))
		require.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)
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
	require.NoError(t, rtClient.Get(context.Background(), types.NamespacedName{Namespace: namespace, Name: name}, &updated))
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
