package request

import (
	"context"
	"testing"

	serviceaccountv1 "github.com/cloudogu/k8s-serviceaccount-lib/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newAuthSecret(name, namespace, key, value string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Data:       map[string][]byte{key: []byte(value)},
	}
}

func TestDefaultProducerClientFactory_NewForProducer(t *testing.T) {
	t.Run("should return error when producer has no HTTP spec", func(t *testing.T) {
		scheme := newTestScheme(t)
		rtClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		factory := &defaultProducerClientFactory{rtClient: rtClient}

		sapr := &serviceaccountv1.ServiceAccountProducer{
			ObjectMeta: metav1.ObjectMeta{Name: "prometheus", Namespace: "ecosystem"},
			Spec: serviceaccountv1.ServiceAccountProducerSpec{
				Producer: "prometheus",
				Exec:     &serviceaccountv1.ExecProducer{Command: "/create-sa.sh", Selector: metav1.LabelSelector{}},
			},
		}

		_, err := factory.NewForProducer(context.Background(), "ecosystem", sapr)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "no HTTP spec configured")
	})

	t.Run("should return error when auth secret is not found", func(t *testing.T) {
		scheme := newTestScheme(t)
		rtClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		factory := &defaultProducerClientFactory{rtClient: rtClient}

		sapr := newTestSAPR("prometheus", "ecosystem", "http://prometheus:9090/serviceaccounts")

		_, err := factory.NewForProducer(context.Background(), "ecosystem", sapr)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get auth secret")
	})

	t.Run("should return error when auth secret is missing the expected key", func(t *testing.T) {
		scheme := newTestScheme(t)
		authSecret := newAuthSecret("prometheus-sa-secret", "ecosystem", "wrongKey", "token")
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(authSecret).Build()
		factory := &defaultProducerClientFactory{rtClient: rtClient}

		sapr := newTestSAPR("prometheus", "ecosystem", "http://prometheus:9090/serviceaccounts")

		_, err := factory.NewForProducer(context.Background(), "ecosystem", sapr)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not contain key")
	})

	t.Run("should return a configured HTTP client on success", func(t *testing.T) {
		scheme := newTestScheme(t)
		authSecret := newAuthSecret("prometheus-sa-secret", "ecosystem", "apiKey", "secret-token")
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(authSecret).Build()
		factory := &defaultProducerClientFactory{rtClient: rtClient}

		sapr := newTestSAPR("prometheus", "ecosystem", "http://prometheus:9090/serviceaccounts")

		httpClient, err := factory.NewForProducer(context.Background(), "ecosystem", sapr)

		require.NoError(t, err)
		assert.NotNil(t, httpClient)
	})
}
