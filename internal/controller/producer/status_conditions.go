package producer

import (
	"context"
	"fmt"

	serviceaccountv2 "github.com/cloudogu/k8s-serviceaccount-lib/v2/api/v2"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const conditionTypeNetworkPolicyUpserted = "NetworkPolicyUpserted"

// reasonProducerReady is the condition reason used when a producer passes all readiness checks.
// The lib only defines the failure reasons (ConditionReadyReason*), so the success reason lives here.
const reasonProducerReady = "ProducerReady"

// reasonNetworkPolicyFailed is set if the network policy upsert fails.
const reasonNetworkPolicyFailed = "NetworkPolicyFailed"

// messageNetworkPolicyFailed is set on the ready condition if the network policy upsert fails.
const messageNetworkPolicyFailed = "Failed to upsert network policy target state"

// reasonNetworkPolicyUpserted is set on the network policy condition if the network policy upsert succeeds.
const reasonNetworkPolicyUpserted = "TargetStateReached"

// markReady sets Ready=True on sapr and persists the condition.
func markReady(ctx context.Context, c client.Client, sapr *serviceaccountv2.ServiceAccountProducer) error {
	return setAndPersist(ctx, c, sapr, []metav1.Condition{
		{Type: serviceaccountv2.ConditionTypeReady, Status: metav1.ConditionTrue, Reason: reasonProducerReady},
	})
}

// markNotReady sets Ready=False with the given reason and message and persists the condition.
func markNotReady(ctx context.Context, c client.Client, sapr *serviceaccountv2.ServiceAccountProducer, reason, message string) error {
	return setAndPersist(ctx, c, sapr, []metav1.Condition{
		{Type: serviceaccountv2.ConditionTypeReady, Status: metav1.ConditionFalse, Reason: reason, Message: message},
	})
}

func markNetpolUpserted(ctx context.Context, c client.Client, sapr *serviceaccountv2.ServiceAccountProducer) error {
	return setAndPersist(ctx, c, sapr, []metav1.Condition{
		{Type: conditionTypeNetworkPolicyUpserted, Status: metav1.ConditionTrue, Reason: reasonNetworkPolicyUpserted},
	})
}

func markNetpolFailed(ctx context.Context, c client.Client, sapr *serviceaccountv2.ServiceAccountProducer, message string) error {
	return setAndPersist(ctx, c, sapr, []metav1.Condition{
		{Type: conditionTypeNetworkPolicyUpserted, Status: metav1.ConditionFalse, Reason: reasonNetworkPolicyFailed, Message: message},
		// if the netpol upsert fails, the producer usually cannot be reached
		{Type: serviceaccountv2.ConditionTypeReady, Status: metav1.ConditionFalse, Reason: reasonNetworkPolicyFailed, Message: messageNetworkPolicyFailed},
	})
}

func setAndPersist(ctx context.Context, c client.Client, sapr *serviceaccountv2.ServiceAccountProducer, conditions []metav1.Condition) error {
	// patchBase is needed as a "merge-base" to create the PATCH request with all changed fields
	patchBase := sapr.DeepCopy()

	for _, condition := range conditions {
		condition.ObservedGeneration = sapr.Generation
		apimeta.SetStatusCondition(&sapr.Status.Conditions, condition)
	}
	sapr.Status.LastExecution = metav1.Now()

	if err := c.Status().Patch(ctx, sapr, client.MergeFrom(patchBase)); err != nil {
		return fmt.Errorf("failed to persist conditions: %w", err)
	}

	return nil
}
