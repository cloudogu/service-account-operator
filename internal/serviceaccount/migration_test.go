package serviceaccount

import (
	"context"
	"testing"

	serviceaccountv2 "github.com/cloudogu/k8s-serviceaccount-lib/v2/api/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestMigrationSource_Credentials(t *testing.T) {
	t.Run("should not find credentials when the SARE has no dogu label", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newMigratingTestSARE()
		sare.Labels = nil
		rtClient := fake.NewClientBuilder().WithScheme(scheme).Build()

		credentials, err := NewMigrationSource(rtClient).Credentials(testCtx, sare, newTestNexusSAPR())

		require.NoError(t, err)
		assert.Empty(t, credentials)
	})

	t.Run("should not find credentials when the dogu label is empty", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newMigratingTestSARE()
		sare.Labels["k8s.cloudogu.com/dogu.name"] = ""
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					return assert.AnError
				},
			}).
			Build()

		credentials, err := NewMigrationSource(rtClient).Credentials(testCtx, sare, newTestNexusSAPR())

		require.NoError(t, err)
		assert.Empty(t, credentials)
	})

	t.Run("should return the producer's keys without prefix and ignore keys of other producers", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newMigratingTestSARE()
		migrationSecret := newMigrationSecret(map[string]string{
			"nexus_username": "baseline-user",
			"nexus_password": "secret",
			"ldap_username":  "other-user",
		})
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(migrationSecret).Build()

		credentials, err := NewMigrationSource(rtClient).Credentials(testCtx, sare, newTestNexusSAPR())

		require.NoError(t, err)
		assert.Equal(t, map[string]string{"username": "baseline-user", "password": "secret"}, credentials)
	})

	t.Run("should return the credentials when the migration secret holds every key the producer returns", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newMigratingTestSARE()
		sapr := newTestNexusSAPR()
		sapr.Spec.HTTP = &serviceaccountv2.HTTPProducer{Return: map[serviceaccountv2.AttributeName]serviceaccountv2.ProducerReturnDefinition{
			"username": {Description: "user name"},
			"password": {Description: "password"},
		}}
		migrationSecret := newMigrationSecret(map[string]string{
			"nexus_username": "baseline-user",
			"nexus_password": "secret",
		})
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(migrationSecret).Build()

		credentials, err := NewMigrationSource(rtClient).Credentials(testCtx, sare, sapr)

		require.NoError(t, err)
		assert.Equal(t, map[string]string{"username": "baseline-user", "password": "secret"}, credentials)
	})

	t.Run("should return all of the producer's keys without validation when the producer declares no return keys", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newMigratingTestSARE()
		sapr := newTestNexusSAPR()
		sapr.Spec.HTTP = &serviceaccountv2.HTTPProducer{}
		migrationSecret := newMigrationSecret(map[string]string{"nexus_username": "baseline-user"})
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(migrationSecret).Build()

		credentials, err := NewMigrationSource(rtClient).Credentials(testCtx, sare, sapr)

		require.NoError(t, err)
		assert.Equal(t, map[string]string{"username": "baseline-user"}, credentials)
	})

	t.Run("should return error naming the missing keys when the migration secret lacks a key the producer returns", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newMigratingTestSARE()
		sapr := newTestNexusSAPR()
		sapr.Spec.HTTP = &serviceaccountv2.HTTPProducer{Return: map[serviceaccountv2.AttributeName]serviceaccountv2.ProducerReturnDefinition{
			"username":   {Description: "user name"},
			"password":   {Description: "password"},
			"repository": {Description: "repository"},
		}}
		migrationSecret := newMigrationSecret(map[string]string{
			"nexus_username": "baseline-user",
			"nexus_password": "secret",
		})
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(migrationSecret).Build()

		credentials, err := NewMigrationSource(rtClient).Credentials(testCtx, sare, sapr)

		require.ErrorIs(t, err, ErrIncompleteMigrationSecret)
		assert.ErrorContains(t, err, "\"baseline-migration\"")
		assert.ErrorContains(t, err, "nexus_repository")
		assert.Empty(t, credentials)
	})

	t.Run("should not find credentials when the migration secret does not exist", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newMigratingTestSARE()
		rtClient := fake.NewClientBuilder().WithScheme(scheme).Build()

		credentials, err := NewMigrationSource(rtClient).Credentials(testCtx, sare, newTestNexusSAPR())

		require.NoError(t, err)
		assert.Empty(t, credentials)
	})

	t.Run("should not find credentials when the migration secret holds no keys for the producer", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newMigratingTestSARE()
		migrationSecret := newMigrationSecret(map[string]string{"ldap_username": "other-user"})
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(migrationSecret).Build()

		credentials, err := NewMigrationSource(rtClient).Credentials(testCtx, sare, newTestNexusSAPR())

		require.NoError(t, err)
		assert.Empty(t, credentials)
	})

	t.Run("should return error when reading the migration secret fails", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newMigratingTestSARE()
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					return assert.AnError
				},
			}).
			Build()

		credentials, err := NewMigrationSource(rtClient).Credentials(testCtx, sare, newTestNexusSAPR())

		require.ErrorIs(t, err, assert.AnError)
		assert.ErrorContains(t, err, "failed to get migration secret \"baseline-migration\"")
		assert.Empty(t, credentials)
	})
}

func newTestNexusSAPR() *serviceaccountv2.ServiceAccountProducer {
	return &serviceaccountv2.ServiceAccountProducer{
		ObjectMeta: metav1.ObjectMeta{Name: "nexus", Namespace: "ecosystem"},
		Spec:       serviceaccountv2.ServiceAccountProducerSpec{Producer: "nexus"},
	}
}

func newMigratingTestSARE() *serviceaccountv2.ServiceAccountRequest {
	sare := newTestSARE("baseline-to-nexus", "ecosystem")
	sare.Labels = map[string]string{"k8s.cloudogu.com/dogu.name": "baseline"}
	sare.Spec.Consumer = "baseline"
	sare.Spec.Producer = "nexus"
	return sare
}

func newMigrationSecret(data map[string]string) *corev1.Secret {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "baseline-migration", Namespace: "ecosystem"},
		Data:       map[string][]byte{},
	}
	for key, value := range data {
		secret.Data[key] = []byte(value)
	}
	return secret
}
