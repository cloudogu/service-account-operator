package request

import (
	"context"
	"fmt"

	serviceaccountv2 "github.com/cloudogu/k8s-serviceaccount-lib/v2/api/v2"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// producerNotFound sets ServiceAccountReady=False with the producer name and underlying error in the message.
func producerNotFound(ctx context.Context, c client.Client, sare *serviceaccountv2.ServiceAccountRequest, producer string, err error) error {
	return setAndPersist(ctx, c, sare, nil, serviceaccountv2.ConditionTypeServiceAccountReady, metav1.ConditionFalse,
		serviceaccountv2.ConditionReasonServiceAccountReadyProducerNotFound,
		fmt.Sprintf("producer %q not found: %s", producer, err.Error()))
}

// serviceAccountReady records the created secret and sets ServiceAccountReady=True, persisting both in one patch.
func serviceAccountReady(ctx context.Context, c client.Client, sare *serviceaccountv2.ServiceAccountRequest, secretName string) error {
	return setAndPersist(ctx, c, sare, func() {
		sare.Status.SecretRef = &serviceaccountv2.LocalSecretRef{Name: secretName}
	}, serviceaccountv2.ConditionTypeServiceAccountReady, metav1.ConditionTrue,
		serviceaccountv2.ConditionReasonServiceAccountReadyCreated, "")
}

// serviceAccountNotReadyForRotation removes a previously created secret and sets ServiceAccountReady=false. The given
// SARE will later be reconciled where the ready condition might be re-set to ready.
func serviceAccountNotReadyForRotation(ctx context.Context, c client.Client, sare *serviceaccountv2.ServiceAccountRequest) error {
	return setAndPersist(ctx, c, sare, func() {
		sare.Status.SecretRef = nil // secret rotation is dealt by deleting a previous secret (if existing)
	}, serviceaccountv2.ConditionTypeServiceAccountReady, metav1.ConditionFalse,
		"SecretRotationInProgress", "")
}

// serviceAccountFailed sets ServiceAccountReady=False with the error message and persists the condition.
func serviceAccountFailed(ctx context.Context, c client.Client, sare *serviceaccountv2.ServiceAccountRequest, err error) error {
	return setAndPersist(ctx, c, sare, nil, serviceaccountv2.ConditionTypeServiceAccountReady, metav1.ConditionFalse,
		serviceaccountv2.ConditionReasonServiceAccountReadyFailed, err.Error())
}

// setAndPersist applies an optional status mutation plus a condition to the SARE and patches the status subresource.
// The merge-base is snapshotted before any mutation, so the patch carries every status change made here in one diff.
func setAndPersist(ctx context.Context, c client.Client, sare *serviceaccountv2.ServiceAccountRequest, mutateStatus func(), condType string, condStatus metav1.ConditionStatus, reason, message string) error {
	patchBase := sare.DeepCopy()

	if mutateStatus != nil {
		mutateStatus()
	}

	apimeta.SetStatusCondition(&sare.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             condStatus,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: sare.Generation,
	})

	if err := c.Status().Patch(ctx, sare, client.MergeFrom(patchBase)); err != nil {
		return fmt.Errorf("failed to persist %s condition: %w", condType, err)
	}

	return nil
}
