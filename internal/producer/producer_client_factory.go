package producer

import (
	"context"
	"fmt"

	serviceaccountv1 "github.com/cloudogu/k8s-serviceaccount-lib/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type DefaultProducerClientFactory struct {
	rtClient client.Client
}

func NewProducerClientFactory(rtClient client.Client) *DefaultProducerClientFactory {
	return &DefaultProducerClientFactory{rtClient: rtClient}
}

func (f *DefaultProducerClientFactory) NewForProducer(ctx context.Context, namespace string, sapr *serviceaccountv1.ServiceAccountProducer) (ServiceAccountClient, error) {
	if sapr.Spec.HTTP == nil {
		return nil, fmt.Errorf("producer %q has no HTTP spec configured", sapr.Name)
	}

	apiKey, err := f.getAPIKey(ctx, namespace, sapr.Spec.HTTP.AuthSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to get API key for producer %q: %w", sapr.Name, err)
	}

	return NewHTTPClient(sapr.Spec.HTTP.Endpoint, apiKey), nil
}

func (f *DefaultProducerClientFactory) getAPIKey(ctx context.Context, namespace string, authSecret serviceaccountv1.ServiceAccountProducerAuthSecret) (string, error) {
	var secret corev1.Secret
	if err := f.rtClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: authSecret.Name}, &secret); err != nil {
		return "", fmt.Errorf("failed to get auth secret %q: %w", authSecret.Name, err)
	}

	apiKey, ok := secret.Data[authSecret.Key]
	if !ok {
		return "", fmt.Errorf("auth secret %q does not contain key %q", authSecret.Name, authSecret.Key)
	}

	return string(apiKey), nil
}
