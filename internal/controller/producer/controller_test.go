package producer

import (
	"context"
	"errors"
	"testing"
	"time"

	serviceaccountv2 "github.com/cloudogu/k8s-serviceaccount-lib/v2/api/v2"
	"github.com/cloudogu/service-account-operator/internal/config"
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

const reconcileInterval = time.Second * 10

var testCtx = context.Background()

func TestController_Reconcile(t *testing.T) {
	t.Run("should set Ready=True and requeue when the producer is reachable", func(t *testing.T) {
		sapr := newTestSAPR("example-producer", "default")
		rtClient := newClientWith(t, sapr)
		factory := newMockClientFactory(t)
		saClient := newMockServiceAccountClient(t)
		saClient.EXPECT().Ready(testCtx).Return(nil)
		factory.EXPECT().NewForProducer(testCtx, "default", matchSAPR(sapr)).Return(saClient, nil)
		upserter := newMockNetworkPolicyUpserter(t)
		upserter.EXPECT().UpsertNetworkPolicy(testCtx, matchSAPR(sapr)).Return(nil)
		cfg := config.OperatorConfig{
			ProducerReconcileInterval: reconcileInterval,
			NetworkPoliciesEnabled:    false,
			SelectorLabels:            map[string]string{"foo": "bar"},
		}

		controller := New(rtClient, &cfg)
		controller.clientFactory = factory
		controller.netpolUpserter = upserter

		result, err := controller.Reconcile(testCtx, reconcileRequest("example-producer", "default"))
		require.NoError(t, err)
		assert.Equal(t, reconcileInterval, result.RequeueAfter)

		cond := readyCondition(t, rtClient, "example-producer", "default")
		require.NotNil(t, cond)
		assert.Equal(t, metav1.ConditionTrue, cond.Status)
		assert.Equal(t, reasonProducerReady, cond.Reason)
	})

	t.Run("should set Ready=False and NetworkPolicyUpserted=False when the netpol upsert fails", func(t *testing.T) {
		sapr := newTestSAPR("example-producer", "default")
		rtClient := newClientWith(t, sapr)
		upserter := newMockNetworkPolicyUpserter(t)
		upserter.EXPECT().UpsertNetworkPolicy(testCtx, matchSAPR(sapr)).Return(assert.AnError)
		cfg := config.OperatorConfig{
			ProducerReconcileInterval: reconcileInterval,
			NetworkPoliciesEnabled:    false,
			SelectorLabels:            map[string]string{"foo": "bar"},
		}

		controller := New(rtClient, &cfg)
		controller.clientFactory = newMockClientFactory(t)
		controller.netpolUpserter = upserter

		result, err := controller.Reconcile(testCtx, reconcileRequest("example-producer", "default"))
		require.NoError(t, err)
		assert.Equal(t, reconcileInterval, result.RequeueAfter)

		readyCond := readyCondition(t, rtClient, "example-producer", "default")
		require.NotNil(t, readyCond)
		assert.Equal(t, metav1.ConditionFalse, readyCond.Status)
		assert.Equal(t, reasonNetworkPolicyFailed, readyCond.Reason)
		assert.Equal(t, messageNetworkPolicyFailed, readyCond.Message)

		netpolCond := netpolCondition(t, rtClient, "example-producer", "default")
		require.NotNil(t, netpolCond)
		assert.Equal(t, metav1.ConditionFalse, netpolCond.Status)
		assert.Equal(t, reasonNetworkPolicyFailed, netpolCond.Reason)
		assert.Equal(t, assert.AnError.Error(), netpolCond.Message)
	})

	t.Run("should return error when status.notReady write fails for netpols", func(t *testing.T) {
		scheme := newTestScheme(t)
		sapr := newTestSAPR("example-producer", "default")
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(sapr).
			WithStatusSubresource(&serviceaccountv2.ServiceAccountProducer{}).
			WithInterceptorFuncs(interceptor.Funcs{
				SubResourcePatch: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
					return errors.New("status patch failed")
				},
			}).
			Build()
		upserter := newMockNetworkPolicyUpserter(t)
		upserter.EXPECT().UpsertNetworkPolicy(testCtx, matchSAPR(sapr)).Return(assert.AnError)
		cfg := config.OperatorConfig{
			ProducerReconcileInterval: reconcileInterval,
			NetworkPoliciesEnabled:    false,
			SelectorLabels:            map[string]string{"foo": "bar"},
		}

		controller := New(rtClient, &cfg)
		controller.clientFactory = newMockClientFactory(t)
		controller.netpolUpserter = upserter

		_, err := controller.Reconcile(testCtx, reconcileRequest("example-producer", "default"))

		require.Error(t, err)
		assert.ErrorContains(t, err, "status patch failed")
	})

	t.Run("should return error when status.ready write fails for netpols", func(t *testing.T) {
		scheme := newTestScheme(t)
		sapr := newTestSAPR("example-producer", "default")
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(sapr).
			WithStatusSubresource(&serviceaccountv2.ServiceAccountProducer{}).
			WithInterceptorFuncs(interceptor.Funcs{
				SubResourcePatch: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
					return errors.New("status patch failed")
				},
			}).
			Build()
		upserter := newMockNetworkPolicyUpserter(t)
		upserter.EXPECT().UpsertNetworkPolicy(testCtx, matchSAPR(sapr)).Return(nil)
		cfg := config.OperatorConfig{
			ProducerReconcileInterval: reconcileInterval,
			NetworkPoliciesEnabled:    false,
			SelectorLabels:            map[string]string{"foo": "bar"},
		}

		controller := New(rtClient, &cfg)
		controller.clientFactory = newMockClientFactory(t)
		controller.netpolUpserter = upserter

		_, err := controller.Reconcile(testCtx, reconcileRequest("example-producer", "default"))

		require.Error(t, err)
		assert.ErrorContains(t, err, "status patch failed")
	})

	t.Run("should set Ready=False with ConnectionFailed when the endpoint is unreachable", func(t *testing.T) {
		sapr := newTestSAPR("example-producer", "default")
		rtClient := newClientWith(t, sapr)
		factory := newMockClientFactory(t)
		saClient := newMockServiceAccountClient(t)
		saClient.EXPECT().Ready(testCtx).Return(errors.New("connection refused"))
		factory.EXPECT().NewForProducer(testCtx, "default", matchSAPR(sapr)).Return(saClient, nil)
		upserter := newMockNetworkPolicyUpserter(t)
		upserter.EXPECT().UpsertNetworkPolicy(testCtx, matchSAPR(sapr)).Return(nil)
		cfg := config.OperatorConfig{
			ProducerReconcileInterval: reconcileInterval,
			NetworkPoliciesEnabled:    false,
			SelectorLabels:            map[string]string{"foo": "bar"},
		}

		controller := New(rtClient, &cfg)
		controller.clientFactory = factory
		controller.netpolUpserter = upserter

		result, err := controller.Reconcile(testCtx, reconcileRequest("example-producer", "default"))
		require.NoError(t, err)
		assert.Equal(t, reconcileInterval, result.RequeueAfter)

		cond := readyCondition(t, rtClient, "example-producer", "default")
		require.NotNil(t, cond)
		assert.Equal(t, metav1.ConditionFalse, cond.Status)
		assert.Equal(t, serviceaccountv2.ConditionReadyReasonConnectionFailed, cond.Reason)
	})

	t.Run("should set Ready=False with AuthSecretNotFound when the client cannot be built", func(t *testing.T) {
		sapr := newTestSAPR("example-producer", "default")
		rtClient := newClientWith(t, sapr)
		factory := newMockClientFactory(t)
		factory.EXPECT().NewForProducer(testCtx, "default", matchSAPR(sapr)).
			Return(nil, errors.New("failed to get auth secret"))
		upserter := newMockNetworkPolicyUpserter(t)
		upserter.EXPECT().UpsertNetworkPolicy(testCtx, matchSAPR(sapr)).Return(nil)
		cfg := config.OperatorConfig{
			ProducerReconcileInterval: reconcileInterval,
			NetworkPoliciesEnabled:    false,
			SelectorLabels:            map[string]string{"foo": "bar"},
		}

		controller := New(rtClient, &cfg)
		controller.clientFactory = factory
		controller.netpolUpserter = upserter

		result, err := controller.Reconcile(testCtx, reconcileRequest("example-producer", "default"))
		require.NoError(t, err)
		assert.Equal(t, reconcileInterval, result.RequeueAfter)

		cond := readyCondition(t, rtClient, "example-producer", "default")
		require.NotNil(t, cond)
		assert.Equal(t, metav1.ConditionFalse, cond.Status)
		assert.Equal(t, serviceaccountv2.ConditionReadyReasonAuthSecretNotFound, cond.Reason)
	})

	t.Run("should set Ready=False with InvalidConfiguration when no HTTP spec is configured", func(t *testing.T) {
		sapr := newTestSAPR("example-producer", "default")
		sapr.Spec.HTTP = nil
		rtClient := newClientWith(t, sapr)
		upserter := newMockNetworkPolicyUpserter(t)
		upserter.EXPECT().UpsertNetworkPolicy(testCtx, matchSAPR(sapr)).Return(nil)
		cfg := config.OperatorConfig{
			ProducerReconcileInterval: reconcileInterval,
			NetworkPoliciesEnabled:    false,
			SelectorLabels:            map[string]string{"foo": "bar"},
		}

		controller := New(rtClient, &cfg)
		controller.netpolUpserter = upserter
		// The factory is never consulted because the configuration is rejected first.
		controller.clientFactory = newMockClientFactory(t)

		result, err := controller.Reconcile(testCtx, reconcileRequest("example-producer", "default"))
		require.NoError(t, err)
		assert.Equal(t, reconcileInterval, result.RequeueAfter)

		cond := readyCondition(t, rtClient, "example-producer", "default")
		require.NotNil(t, cond)
		assert.Equal(t, metav1.ConditionFalse, cond.Status)
		assert.Equal(t, serviceaccountv2.ConditionReadyReasonInvalidConfiguration, cond.Reason)
	})

	t.Run("should return error when status.notReady write fails", func(t *testing.T) {
		scheme := newTestScheme(t)
		sapr := newTestSAPR("example-producer", "default")
		subResourcePatchCounter := 0
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(sapr).
			WithStatusSubresource(&serviceaccountv2.ServiceAccountProducer{}).
			WithInterceptorFuncs(interceptor.Funcs{
				SubResourcePatch: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
					subResourcePatchCounter++
					if subResourcePatchCounter == 2 {
						return errors.New("status patch failed")
					}

					return nil
				},
			}).
			Build()
		factory := newMockClientFactory(t)
		saClient := newMockServiceAccountClient(t)
		saClient.EXPECT().Ready(testCtx).Return(errors.New("connection refused"))
		factory.EXPECT().NewForProducer(testCtx, "default", matchSAPR(sapr)).Return(saClient, nil)
		upserter := newMockNetworkPolicyUpserter(t)
		upserter.EXPECT().UpsertNetworkPolicy(testCtx, matchSAPR(sapr)).Return(nil)
		cfg := config.OperatorConfig{
			ProducerReconcileInterval: reconcileInterval,
			NetworkPoliciesEnabled:    false,
			SelectorLabels:            map[string]string{"foo": "bar"},
		}

		controller := New(rtClient, &cfg)
		controller.clientFactory = factory
		controller.netpolUpserter = upserter

		_, err := controller.Reconcile(testCtx, reconcileRequest("example-producer", "default"))

		require.Error(t, err)
		assert.Contains(t, err.Error(), "status patch failed")
	})

	t.Run("should return error when status.ready write fails", func(t *testing.T) {
		scheme := newTestScheme(t)
		sapr := newTestSAPR("example-producer", "default")
		subResourcePatchCounter := 0
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(sapr).
			WithStatusSubresource(&serviceaccountv2.ServiceAccountProducer{}).
			WithInterceptorFuncs(interceptor.Funcs{
				SubResourcePatch: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
					subResourcePatchCounter++
					if subResourcePatchCounter == 2 {
						return errors.New("status patch failed")
					}

					return nil
				},
			}).
			Build()
		factory := newMockClientFactory(t)
		saClient := newMockServiceAccountClient(t)
		saClient.EXPECT().Ready(testCtx).Return(nil)
		factory.EXPECT().NewForProducer(testCtx, "default", matchSAPR(sapr)).Return(saClient, nil)
		upserter := newMockNetworkPolicyUpserter(t)
		upserter.EXPECT().UpsertNetworkPolicy(testCtx, matchSAPR(sapr)).Return(nil)
		cfg := config.OperatorConfig{
			ProducerReconcileInterval: reconcileInterval,
			NetworkPoliciesEnabled:    false,
			SelectorLabels:            map[string]string{"foo": "bar"},
		}

		controller := New(rtClient, &cfg)
		controller.clientFactory = factory
		controller.netpolUpserter = upserter

		_, err := controller.Reconcile(testCtx, reconcileRequest("example-producer", "default"))

		require.Error(t, err)
		assert.Contains(t, err.Error(), "status patch failed")
	})

	t.Run("should ignore not found", func(t *testing.T) {
		rtClient := newClientWith(t)
		cfg := config.OperatorConfig{
			ProducerReconcileInterval: reconcileInterval,
			NetworkPoliciesEnabled:    false,
			SelectorLabels:            map[string]string{"foo": "bar"},
		}
		controller := New(rtClient, &cfg)
		controller.clientFactory = newMockClientFactory(t)
		controller.netpolUpserter = newMockNetworkPolicyUpserter(t)

		result, err := controller.Reconcile(testCtx, reconcileRequest("missing-producer", "default"))
		require.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)
	})
}

func matchSAPR(sapr *serviceaccountv2.ServiceAccountProducer) any {
	return mock.MatchedBy(func(p *serviceaccountv2.ServiceAccountProducer) bool {
		return p != nil && p.Name == sapr.Name
	})
}

func reconcileRequest(name, namespace string) ctrl.Request {
	return ctrl.Request{Namespace: namespace, Name: name}
}

func newClientWith(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().
		WithScheme(newTestScheme(t)).
		WithObjects(objs...).
		WithStatusSubresource(&serviceaccountv2.ServiceAccountProducer{}).
		Build()
}

func readyCondition(t *testing.T, rtClient client.Client, name, namespace string) *metav1.Condition {
	t.Helper()
	var updated serviceaccountv2.ServiceAccountProducer
	require.NoError(t, rtClient.Get(testCtx, types.NamespacedName{Namespace: namespace, Name: name}, &updated))
	return apimeta.FindStatusCondition(updated.Status.Conditions, serviceaccountv2.ConditionTypeReady)
}

func netpolCondition(t *testing.T, rtClient client.Client, name, namespace string) *metav1.Condition {
	t.Helper()
	var updated serviceaccountv2.ServiceAccountProducer
	require.NoError(t, rtClient.Get(testCtx, types.NamespacedName{Namespace: namespace, Name: name}, &updated))
	return apimeta.FindStatusCondition(updated.Status.Conditions, conditionTypeNetworkPolicyUpserted)
}

func newTestSAPR(name, namespace string) *serviceaccountv2.ServiceAccountProducer {
	return &serviceaccountv2.ServiceAccountProducer{
		Name: name, Namespace: namespace,
		Spec: serviceaccountv2.ServiceAccountProducerSpec{
			Producer: name,
			HTTP: &serviceaccountv2.HTTPProducer{
				Endpoint: "https://nexus:8081/serviceaccounts",
				AuthSecret: serviceaccountv2.ServiceAccountProducerAuthSecret{
					LocalSecretRef: serviceaccountv2.LocalSecretRef{Name: name + "-auth"},
					Key:            "token",
				},
			},
		},
	}
}

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	scheme := runtime.NewScheme()
	metav1.AddToGroupVersion(scheme, serviceaccountv2.GroupVersion)
	require.NoError(t, serviceaccountv2.AddToScheme(scheme), "AddToScheme() failed")

	return scheme
}
