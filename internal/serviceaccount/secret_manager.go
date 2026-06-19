package serviceaccount

import (
	"context"
	"errors"
	"fmt"

	serviceaccountv2 "github.com/cloudogu/k8s-serviceaccount-lib/v2/api/v2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// ErrSecretConflict is returned when the target Secret already exists in the cluster but is not
// owned (via controller OwnerReference) by the ServiceAccountRequest that wants to use it.
var ErrSecretConflict = errors.New("secret already exists and is not owned by this service account request")

type SecretManager struct {
	client client.Client
	scheme *runtime.Scheme
}

// NewSecretManager creates a SecretManager that writes Secrets in the cluster.
func NewSecretManager(c client.Client, scheme *runtime.Scheme) *SecretManager {
	return &SecretManager{client: c, scheme: scheme}
}

// resolveSecretName returns the name of the Secret the credentials are written to:
// spec.secretRef.name if set, otherwise the name of the SARE itself.
func resolveSecretName(sare *serviceaccountv2.ServiceAccountRequest) string {
	if sare.Spec.SecretRef != nil && sare.Spec.SecretRef.Name != "" {
		return sare.Spec.SecretRef.Name
	}

	return sare.Name
}

// Exists reports whether the target Secret for the given SARE already exists in the cluster and is owned by the SARE.
// It returns ErrSecretConflict if the secret exists but is not owned by this SARE (no owner or a different owner).
func (sm *SecretManager) Exists(ctx context.Context, sare *serviceaccountv2.ServiceAccountRequest) (bool, error) {
	name := resolveSecretName(sare)
	var secret corev1.Secret
	err := sm.client.Get(ctx, types.NamespacedName{Namespace: sare.Namespace, Name: name}, &secret)
	if err == nil {
		if metav1.IsControlledBy(&secret, sare) {
			return true, nil
		}

		return false, fmt.Errorf("failed to check for existing secret %q for service account request %q: %w", name, sare.Name, ErrSecretConflict)
	}

	if apierrors.IsNotFound(err) {
		return false, nil
	}

	return false, fmt.Errorf("failed to check for existing secret %q for service account request %q: %w", name, sare.Name, err)
}

// CreateOrUpdate creates or updates the Kubernetes Secret for the given SARE with the provided credentials.
// It returns ErrSecretConflict if the secret exists but is not owned by the SARE.
// It returns the name of the secret that was written.
func (sm *SecretManager) CreateOrUpdate(ctx context.Context, sare *serviceaccountv2.ServiceAccountRequest, credentials map[string]string) (string, error) {
	secretName := resolveSecretName(sare)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: sare.Namespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, sm.client, secret, func() error {
		// secret.ResourceVersion is non-empty only when the secret already exists in the cluster.
		if secret.ResourceVersion != "" && !metav1.IsControlledBy(secret, sare) {
			return fmt.Errorf("failed to create or update secret %q for service account request %q: %w", secretName, sare.Name, ErrSecretConflict)
		}
		secret.StringData = credentials

		return controllerutil.SetControllerReference(sare, secret, sm.scheme)
	})

	if err != nil {
		return "", fmt.Errorf("failed to create or update secret %q for service account request %q: %w", secretName, sare.Name, err)
	}

	return secretName, nil
}
