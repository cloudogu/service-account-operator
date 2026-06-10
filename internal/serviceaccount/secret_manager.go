package serviceaccount

import (
	"context"
	"fmt"

	serviceaccountv1 "github.com/cloudogu/k8s-serviceaccount-lib/api/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// SecretManager manages the Kubernetes Secret that holds a service account's credentials.
// TODO interface kann gelöscht werden? Return concrete types
type SecretManager interface {
	// Exists reports whether the target Secret for the given SARE already exists in the cluster.
	Exists(ctx context.Context, sare *serviceaccountv1.ServiceAccountRequest) (bool, error)
	// CreateOrUpdate writes the credentials to the target Secret and returns its name.
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

// resolveSecretName returns the name of the Secret the credentials are written to:
// spec.secretRef.name if set, otherwise the name of the SARE itself.
func resolveSecretName(sare *serviceaccountv1.ServiceAccountRequest) string {
	if sare.Spec.SecretRef != nil && sare.Spec.SecretRef.Name != "" {
		return sare.Spec.SecretRef.Name
	}
	return sare.Name
}

// Exists reports whether the target Secret for the given SARE already exists in the cluster.
func (sm *secretManager) Exists(ctx context.Context, sare *serviceaccountv1.ServiceAccountRequest) (bool, error) {
	name := resolveSecretName(sare)
	var secret corev1.Secret
	err := sm.client.Get(ctx, types.NamespacedName{Namespace: sare.Namespace, Name: name}, &secret)
	if err == nil {
		// TODO should we check if the secret has a specific label? To avoid conflicts with other secrets.
		return true, nil
	}
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	return false, fmt.Errorf("failed to check for existing secret %q: %w", name, err)
}

// CreateOrUpdate creates or updates the Kubernetes Secret for the given SARE with the provided credentials.
// It returns the name of the secret that was written.
func (sm *secretManager) CreateOrUpdate(ctx context.Context, sare *serviceaccountv1.ServiceAccountRequest, credentials map[string]string) (string, error) {
	secretName := resolveSecretName(sare)

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
