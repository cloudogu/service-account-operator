package producer

import (
	"context"
	"fmt"

	serviceaccountv1 "github.com/cloudogu/k8s-serviceaccount-lib/api/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// reasonProducerReady is the condition reason used when a producer passes all readiness checks.
// The lib only defines the failure reasons (ConditionReadyReason*), so the success reason lives here.
const reasonProducerReady = "ProducerReady"

// statusWriter buffers Ready-condition changes on a ServiceAccountProducer and patches the status
// subresource. It snapshots the producer at construction time so each patch only contains the diff.
type statusWriter struct {
	client client.Client
	sapr   *serviceaccountv1.ServiceAccountProducer
	before *serviceaccountv1.ServiceAccountProducer
}

// newStatusWriter snapshots the producer and returns a writer for its Ready condition.
func newStatusWriter(c client.Client, sapr *serviceaccountv1.ServiceAccountProducer) *statusWriter {
	return &statusWriter{
		client: c,
		sapr:   sapr,
		before: sapr.DeepCopy(),
	}
}

// ready sets Ready=True and persists the condition.
func (s *statusWriter) ready(ctx context.Context) error {
	return s.setAndPersist(ctx, metav1.ConditionTrue, reasonProducerReady, "")
}

// notReady sets Ready=False with the given reason and message and persists the condition.
func (s *statusWriter) notReady(ctx context.Context, reason, message string) error {
	return s.setAndPersist(ctx, metav1.ConditionFalse, reason, message)
}

// setAndPersist sets the Ready condition, stamps LastExecution and patches the status subresource.
func (s *statusWriter) setAndPersist(ctx context.Context, condStatus metav1.ConditionStatus, reason, message string) error {
	apimeta.SetStatusCondition(&s.sapr.Status.Conditions, metav1.Condition{
		Type:               serviceaccountv1.ConditionTypeReady,
		Status:             condStatus,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: s.sapr.Generation,
	})
	s.sapr.Status.LastExecution = metav1.Now()

	if err := s.client.Status().Patch(ctx, s.sapr, client.MergeFrom(s.before)); err != nil {
		return fmt.Errorf("failed to persist Ready condition: %w", err)
	}
	s.before = s.sapr.DeepCopy()

	return nil
}
