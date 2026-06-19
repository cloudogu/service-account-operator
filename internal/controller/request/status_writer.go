package request

import (
	"context"
	"fmt"

	serviceaccountv2 "github.com/cloudogu/k8s-serviceaccount-lib/v2/api/v2"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// statusWriter buffers condition changes on a ServiceAccountRequest and patches the status subresource in one go.
// It captures a snapshot of the SARE at construction time so each patch only contains the actual status diff.
type statusWriter struct {
	client    client.Client
	sare      *serviceaccountv2.ServiceAccountRequest
	patchBase *serviceaccountv2.ServiceAccountRequest
}

// newStatusWriter snapshots the SARE and returns a writer for its status conditions.
// Create it patchBase mutating the SARE's status.
func newStatusWriter(c client.Client, sare *serviceaccountv2.ServiceAccountRequest) *statusWriter {
	return &statusWriter{
		client:    c,
		sare:      sare,
		patchBase: sare.DeepCopy(),
	}
}

// producerNotFound sets ServiceAccountReady=False with the producer name and underlying error in the message.
func (s *statusWriter) producerNotFound(ctx context.Context, producer string, err error) error {
	return s.setAndPersist(ctx, serviceaccountv2.ConditionTypeServiceAccountReady, metav1.ConditionFalse,
		serviceaccountv2.ConditionReasonServiceAccountReadyProducerNotFound,
		fmt.Sprintf("producer %q not found: %s", producer, err.Error()))
}

// serviceAccountReady sets ServiceAccountReady=True and persists the condition.
func (s *statusWriter) serviceAccountReady(ctx context.Context) error {
	return s.setAndPersist(ctx, serviceaccountv2.ConditionTypeServiceAccountReady, metav1.ConditionTrue,
		serviceaccountv2.ConditionReasonServiceAccountReadyCreated, "")
}

// serviceAccountFailed sets ServiceAccountReady=False with the error message and persists the condition.
func (s *statusWriter) serviceAccountFailed(ctx context.Context, err error) error {
	return s.setAndPersist(ctx, serviceaccountv2.ConditionTypeServiceAccountReady, metav1.ConditionFalse,
		serviceaccountv2.ConditionReasonServiceAccountReadyFailed, err.Error())
}

// setAndPersist sets a condition on the SARE and patches the status subresource.
// It advances the snapshot so subsequent calls only diff against the latest persisted state.
func (s *statusWriter) setAndPersist(ctx context.Context, condType string, condStatus metav1.ConditionStatus, reason, message string) error {
	apimeta.SetStatusCondition(&s.sare.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             condStatus,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: s.sare.Generation,
	})
	if err := s.client.Status().Patch(ctx, s.sare, client.MergeFrom(s.patchBase)); err != nil {
		return fmt.Errorf("failed to persist %s condition: %w", condType, err)
	}

	// update the snapshot after successful patch
	s.patchBase = s.sare.DeepCopy()

	return nil
}
