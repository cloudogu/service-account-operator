package serviceaccount

import (
	"context"
	"errors"
	"fmt"
	"slices"
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

// ErrIncompleteMigrationSecret is returned when the migration secret does not have all keys the producer returns.
var ErrIncompleteMigrationSecret = errors.New("migration secret is incomplete")

// MigrationSource reads service account credentials of a consumer dogu migrated from Dogu V2 from its migration secret.
type MigrationSource struct {
	client k8sClient
}

// NewMigrationSource creates a MigrationSource that reads migration secrets from the cluster.
func NewMigrationSource(c k8sClient) *MigrationSource {
	return &MigrationSource{client: c}
}

// MigrationSecretName returns the name of the migration secret of the SARE's dogu, or an empty string if the SARE has no doguName label.
func MigrationSecretName(sare *serviceaccountv2.ServiceAccountRequest) string {
	doguName := sare.Labels[doguNameLabel]
	if doguName == "" {
		return ""
	}

	return doguName + migrationSecretSuffix
}

// Credentials returns the credentials stored in the migration secret <doguname>-migration of the SARE's dogu.
func (ms *MigrationSource) Credentials(ctx context.Context, sare *serviceaccountv2.ServiceAccountRequest, sapr *serviceaccountv2.ServiceAccountProducer) (map[string]string, error) {
	secretName := MigrationSecretName(sare)
	if secretName == "" {
		return nil, nil
	}

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

	if len(credentials) == 0 {
		return credentials, nil
	}

	var missingKeys []string
	for attribute := range returnedAttributes(sapr) {
		if _, ok := credentials[string(attribute)]; !ok {
			missingKeys = append(missingKeys, prefix+string(attribute))
		}
	}

	if len(missingKeys) > 0 {
		slices.Sort(missingKeys)
		return nil, fmt.Errorf("%w: secret %q lacks keys %s", ErrIncompleteMigrationSecret, secretName, strings.Join(missingKeys, ", "))
	}

	return credentials, nil
}

// returnedAttributes returns the attributes the producer declares in its return definition.
// Only the HTTP strategy is implemented.
func returnedAttributes(sapr *serviceaccountv2.ServiceAccountProducer) map[serviceaccountv2.AttributeName]serviceaccountv2.ProducerReturnDefinition {
	if sapr.Spec.HTTP == nil {
		return nil
	}

	return sapr.Spec.HTTP.Return
}
