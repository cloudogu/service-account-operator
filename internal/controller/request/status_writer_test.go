package request

import (
	"errors"
	"fmt"
	"testing"

	serviceaccountv1 "github.com/cloudogu/k8s-serviceaccount-lib/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// buildStatusWriterClient returns a fake client with status subresource support for the given SARE.
func buildStatusWriterClient(t *testing.T, sare *serviceaccountv1.ServiceAccountRequest) client.Client {
	t.Helper()
	scheme := newTestScheme(t)
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(sare).
		WithStatusSubresource(sare).
		Build()
}

// buildStatusWriterClientWithoutObject returns a fake client that has status subresource support
// configured but does NOT contain the SARE object, so Status().Patch() will fail with "not found".
func buildStatusWriterClientWithoutObject(t *testing.T) client.Client {
	t.Helper()
	scheme := newTestScheme(t)
	return fake.NewClientBuilder().
		WithScheme(scheme).
		Build()
}

func getFreshSareFromCluster(t *testing.T, c client.Client, sare *serviceaccountv1.ServiceAccountRequest) serviceaccountv1.ServiceAccountRequest {
	t.Helper()
	var updated serviceaccountv1.ServiceAccountRequest
	require.NoError(t, c.Get(testCtx, types.NamespacedName{Name: sare.Name, Namespace: sare.Namespace}, &updated))
	return updated
}

func TestStatusWriter_ProducerNotFound(t *testing.T) {
	t.Run("should set ServiceAccountReady=False with ProducerNotFound reason and persist to cluster", func(t *testing.T) {
		sare := testSare
		sare.Name = "test-sare"
		sare.Finalizers = []string{finalizer}
		sare.Spec.Optional = true
		rtClient := buildStatusWriterClient(t, &sare)

		err := newStatusWriter(rtClient, &sare).producerNotFound(testCtx, "prometheus", fmt.Errorf("not found"))

		require.NoError(t, err)
		updated := getFreshSareFromCluster(t, rtClient, &sare)
		cond := apimeta.FindStatusCondition(updated.Status.Conditions, serviceaccountv1.ConditionTypeServiceAccountReady)
		require.NotNil(t, cond)
		assert.Equal(t, metav1.ConditionFalse, cond.Status)
		assert.Equal(t, serviceaccountv1.ConditionReasonServiceAccountReadyProducerNotFound, cond.Reason)
		assert.Contains(t, cond.Message, "prometheus")
	})
}

func TestStatusWriter_ServiceAccountReady(t *testing.T) {
	t.Run("should set ServiceAccountReady=True and persist to cluster", func(t *testing.T) {
		sare := testSare
		sare.Name = "test-sare"
		sare.Finalizers = []string{finalizer}
		rtClient := buildStatusWriterClient(t, &sare)

		err := newStatusWriter(rtClient, &sare).serviceAccountReady(testCtx)

		require.NoError(t, err)
		updated := getFreshSareFromCluster(t, rtClient, &sare)
		cond := apimeta.FindStatusCondition(updated.Status.Conditions, serviceaccountv1.ConditionTypeServiceAccountReady)
		require.NotNil(t, cond)
		assert.Equal(t, metav1.ConditionTrue, cond.Status)
		assert.Equal(t, serviceaccountv1.ConditionReasonServiceAccountReadyCreated, cond.Reason)
	})
}

func TestStatusWriter_ServiceAccountFailed(t *testing.T) {
	t.Run("should set ServiceAccountReady=False with error message and persist to cluster", func(t *testing.T) {
		sare := testSare
		sare.Name = "test-sare"
		sare.Finalizers = []string{finalizer}
		rtClient := buildStatusWriterClient(t, &sare)

		reconcileErr := errors.New("connection refused")
		err := newStatusWriter(rtClient, &sare).serviceAccountFailed(testCtx, reconcileErr)

		require.NoError(t, err)
		updated := getFreshSareFromCluster(t, rtClient, &sare)
		cond := apimeta.FindStatusCondition(updated.Status.Conditions, serviceaccountv1.ConditionTypeServiceAccountReady)
		require.NotNil(t, cond)
		assert.Equal(t, metav1.ConditionFalse, cond.Status)
		assert.Equal(t, serviceaccountv1.ConditionReasonServiceAccountReadyFailed, cond.Reason)
		assert.Contains(t, cond.Message, "connection refused")
	})

	t.Run("should return error containing condition type when persist fails", func(t *testing.T) {
		sare := testSare
		sare.Name = "test-sare"
		sare.Finalizers = []string{finalizer}
		rtClient := buildStatusWriterClientWithoutObject(t)

		err := newStatusWriter(rtClient, &sare).serviceAccountFailed(testCtx, errors.New("boom"))

		require.Error(t, err)
		assert.Contains(t, err.Error(), serviceaccountv1.ConditionTypeServiceAccountReady)
	})
}

func TestStatusWriter_SequentialConditions(t *testing.T) {
	t.Run("should persist both conditions when called in sequence on the same writer", func(t *testing.T) {
		sare := testSare
		sare.Name = "test-sare"
		sare.Finalizers = []string{finalizer}
		rtClient := buildStatusWriterClient(t, &sare)
		sw := newStatusWriter(rtClient, &sare)

		require.NoError(t, sw.producerNotFound(testCtx, "prometheus", fmt.Errorf("not found")))
		require.NoError(t, sw.serviceAccountReady(testCtx))

		updated := getFreshSareFromCluster(t, rtClient, &sare)

		cond := apimeta.FindStatusCondition(updated.Status.Conditions, serviceaccountv1.ConditionTypeServiceAccountReady)
		require.NotNil(t, cond)
		assert.Equal(t, metav1.ConditionTrue, cond.Status)
	})
}
