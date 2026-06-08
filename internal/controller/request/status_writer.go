package request

import (
	"context"
	"fmt"

	serviceaccountv1 "github.com/cloudogu/k8s-serviceaccount-lib/api/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// statusWriter buffers condition changes on a ServiceAccountRequest and patches the status
// subresource in one go. It captures a snapshot of the SARE at construction time so each
// patch only contains the actual status diff.
type statusWriter struct {
	client client.Client
	sare   *serviceaccountv1.ServiceAccountRequest
	before *serviceaccountv1.ServiceAccountRequest
}

// newStatusWriter snapshots the SARE and returns a writer for its status conditions.
// Create it before mutating the SARE's status.
func newStatusWriter(c client.Client, sare *serviceaccountv1.ServiceAccountRequest) *statusWriter {
	return &statusWriter{
		client: c,
		sare:   sare,
		before: sare.DeepCopy(),
	}
}

func (s *statusWriter) set(condType string, status metav1.ConditionStatus, reason, message string) {
	apimeta.SetStatusCondition(&s.sare.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: s.sare.Generation,
	})
}

func (s *statusWriter) producerReady(ctx context.Context) error {
	s.set(serviceaccountv1.ConditionTypeProducerReady, metav1.ConditionTrue,
		serviceaccountv1.ConditionReasonProducerReadyProducerFound, "")
	return s.persist(ctx)
}

func (s *statusWriter) producerNotFound(ctx context.Context, producer string) error {
	s.set(serviceaccountv1.ConditionTypeProducerReady, metav1.ConditionFalse,
		serviceaccountv1.ConditionReasonProducerReadyProducerNotFound,
		fmt.Sprintf("producer %q not found", producer))
	return s.persist(ctx)
}

func (s *statusWriter) serviceAccountReady(ctx context.Context) error {
	s.set(serviceaccountv1.ConditionTypeServiceAccountReady, metav1.ConditionTrue,
		serviceaccountv1.ConditionReasonServiceAccountReadyCreated, "")
	return s.persist(ctx)
}

func (s *statusWriter) serviceAccountFailed(ctx context.Context, err error) error {
	s.set(serviceaccountv1.ConditionTypeServiceAccountReady, metav1.ConditionFalse,
		serviceaccountv1.ConditionReasonServiceAccountReadyFailed, err.Error())
	return s.persist(ctx)
}

// persist patches the status subresource with the buffered condition changes and advances
// the internal snapshot so subsequent calls on the same writer only diff against the new state.
func (s *statusWriter) persist(ctx context.Context) error {
	err := s.client.Status().Patch(ctx, s.sare, client.MergeFrom(s.before))
	if err == nil {
		s.before = s.sare.DeepCopy()
	}
	return err
}
