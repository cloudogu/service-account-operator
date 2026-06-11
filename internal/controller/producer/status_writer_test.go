package producer

import (
	"context"
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

// newClientWithoutObject returns a fake client with status subresource support configured but
// without any producer object, so Status().Patch() fails with "not found".
func newClientWithoutObject(t *testing.T) client.Client {
	t.Helper()
	return fake.NewClientBuilder().
		WithScheme(newTestScheme(t)).
		WithStatusSubresource(&serviceaccountv1.ServiceAccountProducer{}).
		Build()
}

func TestStatusWriter_Ready(t *testing.T) {
	t.Run("should set Ready=True, stamp LastExecution and persist to cluster", func(t *testing.T) {
		sapr := newTestSAPR("example-producer", "default")
		rtClient := newClientWith(t, sapr)

		err := newStatusWriter(rtClient, sapr).ready(context.Background())

		require.NoError(t, err)
		updated := getUpdatedSAPR(t, rtClient, "example-producer", "default")
		cond := apimeta.FindStatusCondition(updated.Status.Conditions, serviceaccountv1.ConditionTypeReady)
		require.NotNil(t, cond)
		assert.Equal(t, metav1.ConditionTrue, cond.Status)
		assert.Equal(t, reasonProducerReady, cond.Reason)
		assert.Empty(t, cond.Message)
		assert.False(t, updated.Status.LastExecution.IsZero(), "LastExecution should be stamped")
	})

	t.Run("should return error containing condition type when persist fails", func(t *testing.T) {
		sapr := newTestSAPR("example-producer", "default")
		rtClient := newClientWithoutObject(t)

		err := newStatusWriter(rtClient, sapr).ready(context.Background())

		require.Error(t, err)
		assert.Contains(t, err.Error(), serviceaccountv1.ConditionTypeReady)
	})
}

func TestStatusWriter_NotReady(t *testing.T) {
	t.Run("should set Ready=False with reason and message and persist to cluster", func(t *testing.T) {
		sapr := newTestSAPR("example-producer", "default")
		rtClient := newClientWith(t, sapr)

		err := newStatusWriter(rtClient, sapr).notReady(context.Background(),
			serviceaccountv1.ConditionReadyReasonConnectionFailed, "endpoint is not reachable")

		require.NoError(t, err)
		updated := getUpdatedSAPR(t, rtClient, "example-producer", "default")
		cond := apimeta.FindStatusCondition(updated.Status.Conditions, serviceaccountv1.ConditionTypeReady)
		require.NotNil(t, cond)
		assert.Equal(t, metav1.ConditionFalse, cond.Status)
		assert.Equal(t, serviceaccountv1.ConditionReadyReasonConnectionFailed, cond.Reason)
		assert.Contains(t, cond.Message, "endpoint is not reachable")
	})

	t.Run("should return error containing condition type when persist fails", func(t *testing.T) {
		sapr := newTestSAPR("example-producer", "default")
		rtClient := newClientWithoutObject(t)

		err := newStatusWriter(rtClient, sapr).notReady(context.Background(),
			serviceaccountv1.ConditionReadyReasonAuthSecretNotFound, "secret missing")

		require.Error(t, err)
		assert.Contains(t, err.Error(), serviceaccountv1.ConditionTypeReady)
	})
}

func TestStatusWriter_SequentialConditions(t *testing.T) {
	t.Run("should overwrite a previous condition when reused on the same writer", func(t *testing.T) {
		sapr := newTestSAPR("example-producer", "default")
		rtClient := newClientWith(t, sapr)
		sw := newStatusWriter(rtClient, sapr)

		require.NoError(t, sw.notReady(context.Background(),
			serviceaccountv1.ConditionReadyReasonConnectionFailed, "endpoint is not reachable"))
		require.NoError(t, sw.ready(context.Background()))

		updated := getUpdatedSAPR(t, rtClient, "example-producer", "default")
		cond := apimeta.FindStatusCondition(updated.Status.Conditions, serviceaccountv1.ConditionTypeReady)
		require.NotNil(t, cond)
		assert.Equal(t, metav1.ConditionTrue, cond.Status)
		assert.Equal(t, reasonProducerReady, cond.Reason)
		assert.Empty(t, cond.Message)
	})
}

func getUpdatedSAPR(t *testing.T, c client.Client, name, namespace string) serviceaccountv1.ServiceAccountProducer {
	t.Helper()
	var updated serviceaccountv1.ServiceAccountProducer
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: name, Namespace: namespace}, &updated))
	return updated
}
