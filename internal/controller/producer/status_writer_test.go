package producer

import (
	"testing"

	serviceaccountv2 "github.com/cloudogu/k8s-serviceaccount-lib/v2/api/v2"
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
		WithStatusSubresource(&serviceaccountv2.ServiceAccountProducer{}).
		Build()
}

func TestMarkReady(t *testing.T) {
	t.Run("should set Ready=True, stamp LastExecution and persist to cluster", func(t *testing.T) {
		sapr := newTestSAPR("example-producer", "default")
		rtClient := newClientWith(t, sapr)

		err := markReady(testCtx, rtClient, sapr)

		require.NoError(t, err)
		updated := getUpdatedSAPR(t, rtClient, "example-producer", "default")
		cond := apimeta.FindStatusCondition(updated.Status.Conditions, serviceaccountv2.ConditionTypeReady)
		require.NotNil(t, cond)
		assert.Equal(t, metav1.ConditionTrue, cond.Status)
		assert.Equal(t, reasonProducerReady, cond.Reason)
		assert.Empty(t, cond.Message)
		assert.False(t, updated.Status.LastExecution.IsZero(), "LastExecution should be stamped")
	})

	t.Run("should return error containing condition type when persist fails", func(t *testing.T) {
		sapr := newTestSAPR("example-producer", "default")
		rtClient := newClientWithoutObject(t)

		err := markReady(testCtx, rtClient, sapr)

		require.Error(t, err)
		assert.Contains(t, err.Error(), serviceaccountv2.ConditionTypeReady)
	})
}

func TestMarkNotReady(t *testing.T) {
	t.Run("should set Ready=False with reason and message and persist to cluster", func(t *testing.T) {
		sapr := newTestSAPR("example-producer", "default")
		rtClient := newClientWith(t, sapr)

		err := markNotReady(testCtx, rtClient, sapr,
			serviceaccountv2.ConditionReadyReasonConnectionFailed, "endpoint is not reachable")

		require.NoError(t, err)
		updated := getUpdatedSAPR(t, rtClient, "example-producer", "default")
		cond := apimeta.FindStatusCondition(updated.Status.Conditions, serviceaccountv2.ConditionTypeReady)
		require.NotNil(t, cond)
		assert.Equal(t, metav1.ConditionFalse, cond.Status)
		assert.Equal(t, serviceaccountv2.ConditionReadyReasonConnectionFailed, cond.Reason)
		assert.Contains(t, cond.Message, "endpoint is not reachable")
	})

	t.Run("should return error containing condition type when persist fails", func(t *testing.T) {
		sapr := newTestSAPR("example-producer", "default")
		rtClient := newClientWithoutObject(t)

		err := markNotReady(testCtx, rtClient, sapr,
			serviceaccountv2.ConditionReadyReasonAuthSecretNotFound, "secret missing")

		require.Error(t, err)
		assert.Contains(t, err.Error(), serviceaccountv2.ConditionTypeReady)
	})
}

func getUpdatedSAPR(t *testing.T, c client.Client, name, namespace string) serviceaccountv2.ServiceAccountProducer {
	t.Helper()
	var updated serviceaccountv2.ServiceAccountProducer
	require.NoError(t, c.Get(testCtx,
		types.NamespacedName{Name: name, Namespace: namespace}, &updated))
	return updated
}
