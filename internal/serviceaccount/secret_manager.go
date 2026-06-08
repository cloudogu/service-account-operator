package serviceaccount

import (
	"context"
	"fmt"

	serviceaccountv1 "github.com/cloudogu/k8s-serviceaccount-lib/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// SecretManager writes service account credentials to a Kubernetes Secret.
type SecretManager interface {
	CreateOrUpdate(ctx context.Context, sare *serviceaccountv1.ServiceAccountRequest, credentials map[string]string) (string, error)
}

type secretManager struct {
	client client.Client
	scheme *runtime.Scheme
}

// NewSecretManager creates a SecretManager that writes Secrets in the cluster.
func NewSecretManager(c client.Client, scheme *runtime.Scheme) SecretManager {
	return &secretManager{client: c, scheme: scheme}
}

// CreateOrUpdate creates or updates the Kubernetes Secret for the given SARE with the provided credentials.
// It returns the name of the secret that was written.
func (sm *secretManager) CreateOrUpdate(ctx context.Context, sare *serviceaccountv1.ServiceAccountRequest, credentials map[string]string) (string, error) {
	secretName := sare.Name
	if sare.Spec.SecretRef != nil && sare.Spec.SecretRef.Name != "" {
		secretName = sare.Spec.SecretRef.Name
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: sare.Namespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, sm.client, secret, func() error {
		secret.StringData = credentials
		return controllerutil.SetControllerReference(sare, secret, sm.scheme)
	})
	if err != nil {
		return "", fmt.Errorf("failed to create or update secret %q for service account request %q: %w", secretName, sare.Name, err)
	}

	return secretName, nil
}
