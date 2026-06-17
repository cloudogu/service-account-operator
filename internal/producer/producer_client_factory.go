package producer

import (
	"context"
	"fmt"

	serviceaccountv1 "github.com/cloudogu/k8s-serviceaccount-lib/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// DefaultProducerClientFactory creates ServiceAccountClient instances for ServiceAccountProducer resources.
type DefaultProducerClientFactory struct {
	rtClient client.Client
}

// NewClientFactory creates a DefaultProducerClientFactory backed by the given controller-runtime client.
func NewClientFactory(rtClient client.Client) *DefaultProducerClientFactory {
	return &DefaultProducerClientFactory{rtClient: rtClient}
}

// NewForProducer returns a ServiceAccountClient configured for the given producer's HTTP endpoint and API key.
func (f *DefaultProducerClientFactory) NewForProducer(ctx context.Context, namespace string, sapr *serviceaccountv1.ServiceAccountProducer) (ServiceAccountClient, error) {
	if sapr.Spec.HTTP == nil {
		return nil, fmt.Errorf("producer %q has no HTTP spec configured", sapr.Name)
	}

	apiKey, err := resolveAPIKey(ctx, f.rtClient, namespace, sapr.Spec.HTTP.AuthSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to get API key for producer %q: %w", sapr.Name, err)
	}

	return NewHTTPClient(sapr.Spec.HTTP.Endpoint, apiKey), nil
}

// resolveAPIKey reads the producer's API key from the referenced Kubernetes Secret.
func resolveAPIKey(ctx context.Context, rtClient client.Client, namespace string, authSecret serviceaccountv1.ServiceAccountProducerAuthSecret) (string, error) {
	var secret corev1.Secret
	if err := rtClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: authSecret.Name}, &secret); err != nil {
		return "", fmt.Errorf("failed to get auth secret %q: %w", authSecret.Name, err)
	}

	apiKey, ok := secret.Data[authSecret.Key]
	if !ok {
		return "", fmt.Errorf("auth secret %q does not contain key %q", authSecret.Name, authSecret.Key)
	}

	return string(apiKey), nil
}
