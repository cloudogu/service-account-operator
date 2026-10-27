package serviceaccount

import (
	"context"
	"fmt"
	"strings"

	serviceaccountv2 "github.com/cloudogu/k8s-serviceaccount-lib/v2/api/v2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
)

const (
	doguNameLabel         = "k8s.cloudogu.com/dogu.name"
	migrationSecretSuffix = "-migration"
	migrationKeySeparator = "_"
)

// MigrationSource reads service account credentials of a consumer dogu migrated from Dogu V2 from its migration secret.
type MigrationSource struct {
	client k8sClient
}

// NewMigrationSource creates a MigrationSource that reads migration secrets from the cluster.
func NewMigrationSource(c k8sClient) *MigrationSource {
	return &MigrationSource{client: c}
}

// Credentials returns the credentials stored in the migration secret <doguname>-migration of the SARE's dogu.
func (ms *MigrationSource) Credentials(ctx context.Context, sare *serviceaccountv2.ServiceAccountRequest) (map[string]string, error) {
	doguName := sare.Labels[doguNameLabel]
	if doguName == "" {
		return nil, nil
	}

	secretName := doguName + migrationSecretSuffix
	var secret corev1.Secret
	err := ms.client.Get(ctx, types.NamespacedName{Namespace: sare.Namespace, Name: secretName}, &secret)
	if apierrors.IsNotFound(err) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("failed to get migration secret %q for service account request %q: %w", secretName, sare.Name, err)
	}

	prefix := sare.Spec.Producer + migrationKeySeparator
	credentials := map[string]string{}
	for key, value := range secret.Data {
		if attribute, ok := strings.CutPrefix(key, prefix); ok {
			credentials[attribute] = string(value)
		}
	}

	return credentials, nil
}
