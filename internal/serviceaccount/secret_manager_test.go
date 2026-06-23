package serviceaccount

import (
	"context"
	"errors"
	"fmt"
	"testing"

	serviceaccountv2 "github.com/cloudogu/k8s-serviceaccount-lib/v2/api/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	errors2 "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

var testCtx = context.Background()

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, serviceaccountv2.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	return scheme
}

func newTestSARE(name, namespace string) *serviceaccountv2.ServiceAccountRequest {
	return &serviceaccountv2.ServiceAccountRequest{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: serviceaccountv2.ServiceAccountRequestSpec{
			Consumer:     "grafana",
			ConsumerType: serviceaccountv2.DoguConsumerType,
			Producer:     "prometheus",
		},
	}
}

func TestSecretManager_Exists(t *testing.T) {
	t.Run("should return false when target secret does not exist", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSARE("grafana-to-prometheus", "ecosystem")
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare).Build()

		sm := NewSecretManager(rtClient, scheme)
		exists, err := sm.Exists(testCtx, sare)

		require.NoError(t, err)
		assert.False(t, exists)
	})

	t.Run("should return true when target secret exists and is owned by the SARE", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSARE("grafana-to-prometheus", "ecosystem")
		existing := newOwnedSecret("grafana-to-prometheus", "ecosystem", sare, scheme, t)
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare, existing).Build()

		sm := NewSecretManager(rtClient, scheme)
		exists, err := sm.Exists(testCtx, sare)

		require.NoError(t, err)
		assert.True(t, exists)
	})

	t.Run("should return ErrSecretConflict when target secret exists but is not owned by this SARE", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSARE("grafana-to-prometheus", "ecosystem")
		existing := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "grafana-to-prometheus", Namespace: "ecosystem"},
		}
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare, existing).Build()

		sm := NewSecretManager(rtClient, scheme)
		exists, err := sm.Exists(testCtx, sare)

		require.ErrorIs(t, err, ErrSecretConflict)
		assert.False(t, exists)
	})

	t.Run("should return error when client returns unexpected error", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSARE("grafana-to-prometheus", "ecosystem")
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(sare).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if _, ok := obj.(*corev1.Secret); ok {
						return errors.New("etcd connection refused")
					}
					return c.Get(ctx, key, obj, opts...)
				},
			}).
			Build()

		sm := NewSecretManager(rtClient, scheme)
		_, err := sm.Exists(testCtx, sare)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to check for existing secret")
	})

	t.Run("should resolve the custom secretRef name", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSARE("grafana-to-prometheus", "ecosystem")
		sare.Spec.SecretRef = &serviceaccountv2.LocalSecretRef{Name: "custom-creds"}
		existing := newOwnedSecret("custom-creds", "ecosystem", sare, scheme, t)
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare, existing).Build()

		sm := NewSecretManager(rtClient, scheme)
		exists, err := sm.Exists(testCtx, sare)

		require.NoError(t, err)
		assert.True(t, exists)
	})
}

func TestSecretManager_CreateOrUpdate(t *testing.T) {
	t.Run("should create secret named after SARE when no secretRef is set", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSARE("grafana-to-prometheus", "ecosystem")
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare).Build()

		sm := NewSecretManager(rtClient, scheme)
		creds := map[string]string{"username": "user1", "password": "pass1"}

		secretName, err := sm.CreateOrUpdate(testCtx, sare, creds)

		require.NoError(t, err)
		assert.Equal(t, "grafana-to-prometheus", secretName)

		var secret corev1.Secret
		require.NoError(t, rtClient.Get(testCtx, types.NamespacedName{Name: "grafana-to-prometheus", Namespace: "ecosystem"}, &secret))
		assert.Equal(t, "user1", secret.StringData["username"])
		assert.Equal(t, "pass1", secret.StringData["password"])
	})

	t.Run("should create secret with name from spec.secretRef when set", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSARE("grafana-to-prometheus", "ecosystem")
		sare.Spec.SecretRef = &serviceaccountv2.LocalSecretRef{Name: "custom-prometheus-creds"}
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare).Build()

		sm := NewSecretManager(rtClient, scheme)

		secretName, err := sm.CreateOrUpdate(testCtx, sare, map[string]string{"apiKey": "abc"})

		require.NoError(t, err)
		assert.Equal(t, "custom-prometheus-creds", secretName)

		var secret corev1.Secret
		require.NoError(t, rtClient.Get(testCtx, types.NamespacedName{Name: "custom-prometheus-creds", Namespace: "ecosystem"}, &secret))
	})

	t.Run("should set owner reference pointing to the SARE", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSARE("grafana-to-prometheus", "ecosystem")
		sare.UID = "test-uid-123"
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare).Build()

		sm := NewSecretManager(rtClient, scheme)
		_, err := sm.CreateOrUpdate(testCtx, sare, map[string]string{"key": "val"})
		require.NoError(t, err)

		var secret corev1.Secret
		require.NoError(t, rtClient.Get(testCtx, types.NamespacedName{Name: "grafana-to-prometheus", Namespace: "ecosystem"}, &secret))
		require.Len(t, secret.OwnerReferences, 1)
		assert.Equal(t, "grafana-to-prometheus", secret.OwnerReferences[0].Name)
		assert.Equal(t, "test-uid-123", string(secret.OwnerReferences[0].UID))
	})

	t.Run("should return ErrSecretConflict when secret exists without owner ref", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSARE("grafana-to-prometheus", "ecosystem")
		sare.UID = "test-uid-123"
		existing := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "grafana-to-prometheus", Namespace: "ecosystem"},
		}
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare, existing).Build()

		sm := NewSecretManager(rtClient, scheme)
		_, err := sm.CreateOrUpdate(testCtx, sare, map[string]string{"key": "val"})

		require.ErrorIs(t, err, ErrSecretConflict)
	})

	t.Run("should update existing secret credentials when secret is owned by this SARE", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSARE("grafana-to-prometheus", "ecosystem")
		existing := newOwnedSecret("grafana-to-prometheus", "ecosystem", sare, scheme, t)
		existing.StringData = map[string]string{"username": "old-user", "password": "old-pass"}
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare, existing).Build()

		sm := NewSecretManager(rtClient, scheme)
		_, err := sm.CreateOrUpdate(testCtx, sare, map[string]string{"username": "new-user", "password": "new-pass"})
		require.NoError(t, err)

		var secret corev1.Secret
		require.NoError(t, rtClient.Get(testCtx, types.NamespacedName{Name: "grafana-to-prometheus", Namespace: "ecosystem"}, &secret))
		assert.Equal(t, "new-user", secret.StringData["username"])
	})

	t.Run("should return error when secret creation fails", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSARE("grafana-to-prometheus", "ecosystem")
		rtClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(sare).
			WithInterceptorFuncs(interceptor.Funcs{
				Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
					if _, ok := obj.(*corev1.Secret); ok {
						return errors.New("permission denied")
					}
					return c.Create(ctx, obj, opts...)
				},
			}).
			Build()

		sm := NewSecretManager(rtClient, scheme)
		_, err := sm.CreateOrUpdate(testCtx, sare, map[string]string{"key": "val"})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create or update secret")
	})
}

func newOwnedSecret(name, namespace string, owner *serviceaccountv2.ServiceAccountRequest, scheme *runtime.Scheme, t *testing.T) *corev1.Secret {
	t.Helper()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}
	require.NoError(t, controllerutil.SetControllerReference(owner, secret, scheme))
	return secret
}

func TestSecretManager_Delete(t *testing.T) {
	type fields struct {
		client func(*testing.T) client.Client
	}
	type args struct {
		sare *serviceaccountv2.ServiceAccountRequest
	}
	testCtx := context.Background()
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name: "should return nil on successful delete",
			fields: fields{
				client: func(t *testing.T) client.Client {
					mClient := newMockK8sClient(t)
					mClient.EXPECT().Delete(testCtx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "grafana-to-prometheus", Namespace: "ecosystem"}}).Return(nil)
					return mClient
				},
			},
			args: args{
				sare: newTestSARE("grafana-to-prometheus", "ecosystem"),
			},
			wantErr: assert.NoError,
		},
		{
			name: "should return nil on resource not found error",
			fields: fields{
				client: func(t *testing.T) client.Client {
					mClient := newMockK8sClient(t)
					mClient.EXPECT().Delete(testCtx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "grafana-to-prometheus", Namespace: "ecosystem"}}).Return(errors2.NewNotFound(schema.GroupResource{}, "secret"))
					return mClient
				},
			},
			args: args{
				sare: newTestSARE("grafana-to-prometheus", "ecosystem"),
			},
			wantErr: assert.NoError,
		},
		{
			name: "should return error on error deleting the secret",
			fields: fields{
				client: func(t *testing.T) client.Client {
					mClient := newMockK8sClient(t)
					mClient.EXPECT().Delete(testCtx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "grafana-to-prometheus", Namespace: "ecosystem"}}).Return(assert.AnError)
					return mClient
				},
			},
			args: args{
				sare: newTestSARE("grafana-to-prometheus", "ecosystem"),
			},
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool {
				return assert.ErrorContains(t, err, "failed to delete secret \"grafana-to-prometheus\" for service account request \"grafana-to-prometheus\"")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm := &SecretManager{
				client: tt.fields.client(t),
			}
			tt.wantErr(t, sm.Delete(testCtx, tt.args.sare), fmt.Sprintf("Delete(%v, %v)", testCtx, tt.args.sare))
		})
	}
}
