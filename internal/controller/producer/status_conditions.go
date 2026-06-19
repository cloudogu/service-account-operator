package producer

import (
	"context"
	"fmt"

	serviceaccountv2 "github.com/cloudogu/k8s-serviceaccount-lib/v2/api/v2"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// reasonProducerReady is the condition reason used when a producer passes all readiness checks.
// The lib only defines the failure reasons (ConditionReadyReason*), so the success reason lives here.
const reasonProducerReady = "ProducerReady"

// markReady sets Ready=True on sapr and persists the condition.
func markReady(ctx context.Context, c client.Client, sapr *serviceaccountv2.ServiceAccountProducer) error {
	return setAndPersist(ctx, c, sapr, metav1.ConditionTrue, reasonProducerReady, "")
}

// markNotReady sets Ready=False with the given reason and message and persists the condition.
func markNotReady(ctx context.Context, c client.Client, sapr *serviceaccountv2.ServiceAccountProducer, reason, message string) error {
	return setAndPersist(ctx, c, sapr, metav1.ConditionFalse, reason, message)
}

func setAndPersist(ctx context.Context, c client.Client, sapr *serviceaccountv2.ServiceAccountProducer, condStatus metav1.ConditionStatus, reason, message string) error {
	// patchBase is needed as a "merge-base" to create the PATCH request with all changed fields
	patchBase := sapr.DeepCopy()

	apimeta.SetStatusCondition(&sapr.Status.Conditions, metav1.Condition{
		Type:               serviceaccountv2.ConditionTypeReady,
		Status:             condStatus,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: sapr.Generation,
	})
	sapr.Status.LastExecution = metav1.Now()

	if err := c.Status().Patch(ctx, sapr, client.MergeFrom(patchBase)); err != nil {
		return fmt.Errorf("failed to persist Ready condition: %w", err)
	}

	return nil
}
