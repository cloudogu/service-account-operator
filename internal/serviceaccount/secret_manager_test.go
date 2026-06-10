package serviceaccount

import (
	"context"
	"testing"

	serviceaccountv1 "github.com/cloudogu/k8s-serviceaccount-lib/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := serviceaccountv1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme(serviceaccountv1) error: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme(corev1) error: %v", err)
	}
	return scheme
}

func newTestSARE(name, namespace string) *serviceaccountv1.ServiceAccountRequest {
	return &serviceaccountv1.ServiceAccountRequest{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: serviceaccountv1.ServiceAccountRequestSpec{
			Consumer:     "grafana",
			ConsumerType: serviceaccountv1.DoguConsumerType,
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
		exists, err := sm.Exists(context.Background(), sare)
		if err != nil {
			t.Fatalf("Exists() returned error: %v", err)
		}
		if exists {
			t.Errorf("exists = true, want false")
		}
	})

	t.Run("should return true when target secret exists and is owned by the SARE", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSARE("grafana-to-prometheus", "ecosystem")
		existing := newOwnedSecret("grafana-to-prometheus", "ecosystem", sare, scheme, t)
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare, existing).Build()

		sm := NewSecretManager(rtClient, scheme)
		exists, err := sm.Exists(context.Background(), sare)
		if err != nil {
			t.Fatalf("Exists() returned error: %v", err)
		}
		if !exists {
			t.Errorf("exists = false, want true")
		}
	})

	t.Run("should return false when target secret exists but is not owned by this SARE", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSARE("grafana-to-prometheus", "ecosystem")
		existing := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "grafana-to-prometheus", Namespace: "ecosystem"},
		}
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare, existing).Build()

		sm := NewSecretManager(rtClient, scheme)
		exists, err := sm.Exists(context.Background(), sare)
		if err != nil {
			t.Fatalf("Exists() returned error: %v", err)
		}
		if exists {
			t.Errorf("exists = true, want false")
		}
	})

	t.Run("should resolve the custom secretRef name", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSARE("grafana-to-prometheus", "ecosystem")
		sare.Spec.SecretRef = &serviceaccountv1.LocalSecretRef{Name: "custom-creds"}
		existing := newOwnedSecret("custom-creds", "ecosystem", sare, scheme, t)
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare, existing).Build()

		sm := NewSecretManager(rtClient, scheme)
		exists, err := sm.Exists(context.Background(), sare)
		if err != nil {
			t.Fatalf("Exists() returned error: %v", err)
		}
		if !exists {
			t.Errorf("exists = false, want true")
		}
	})
}

func TestSecretManager_CreateOrUpdate(t *testing.T) {
	t.Run("should create secret named after SARE when no secretRef is set", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSARE("grafana-to-prometheus", "ecosystem")
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare).Build()

		sm := NewSecretManager(rtClient, scheme)
		creds := map[string]string{"username": "user1", "password": "pass1"}

		secretName, err := sm.CreateOrUpdate(context.Background(), sare, creds)
		if err != nil {
			t.Fatalf("CreateOrUpdate() returned error: %v", err)
		}
		if secretName != "grafana-to-prometheus" {
			t.Errorf("secretName = %q, want %q", secretName, "grafana-to-prometheus")
		}

		var secret corev1.Secret
		if err := rtClient.Get(context.Background(), types.NamespacedName{Name: "grafana-to-prometheus", Namespace: "ecosystem"}, &secret); err != nil {
			t.Fatalf("secret not found: %v", err)
		}
		if secret.StringData["username"] != "user1" {
			t.Errorf("username = %q, want %q", secret.StringData["username"], "user1")
		}
		if secret.StringData["password"] != "pass1" {
			t.Errorf("password = %q, want %q", secret.StringData["password"], "pass1")
		}
	})

	t.Run("should create secret with name from spec.secretRef when set", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSARE("grafana-to-prometheus", "ecosystem")
		sare.Spec.SecretRef = &serviceaccountv1.LocalSecretRef{Name: "custom-prometheus-creds"}
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare).Build()

		sm := NewSecretManager(rtClient, scheme)

		secretName, err := sm.CreateOrUpdate(context.Background(), sare, map[string]string{"apiKey": "abc"})
		if err != nil {
			t.Fatalf("CreateOrUpdate() returned error: %v", err)
		}
		if secretName != "custom-prometheus-creds" {
			t.Errorf("secretName = %q, want %q", secretName, "custom-prometheus-creds")
		}

		var secret corev1.Secret
		if err := rtClient.Get(context.Background(), types.NamespacedName{Name: "custom-prometheus-creds", Namespace: "ecosystem"}, &secret); err != nil {
			t.Fatalf("secret not found: %v", err)
		}
	})

	t.Run("should set owner reference pointing to the SARE", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSARE("grafana-to-prometheus", "ecosystem")
		sare.UID = "test-uid-123"
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare).Build()

		sm := NewSecretManager(rtClient, scheme)
		_, err := sm.CreateOrUpdate(context.Background(), sare, map[string]string{"key": "val"})
		if err != nil {
			t.Fatalf("CreateOrUpdate() returned error: %v", err)
		}

		var secret corev1.Secret
		if err := rtClient.Get(context.Background(), types.NamespacedName{Name: "grafana-to-prometheus", Namespace: "ecosystem"}, &secret); err != nil {
			t.Fatalf("secret not found: %v", err)
		}
		if len(secret.OwnerReferences) != 1 {
			t.Fatalf("OwnerReferences length = %d, want 1", len(secret.OwnerReferences))
		}
		if secret.OwnerReferences[0].Name != "grafana-to-prometheus" {
			t.Errorf("owner name = %q, want %q", secret.OwnerReferences[0].Name, "grafana-to-prometheus")
		}
		if string(secret.OwnerReferences[0].UID) != "test-uid-123" {
			t.Errorf("owner UID = %q, want %q", secret.OwnerReferences[0].UID, "test-uid-123")
		}
	})

	t.Run("should set owner reference on pre-existing secret without ownerRef", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSARE("grafana-to-prometheus", "ecosystem")
		sare.UID = "test-uid-123"
		existing := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "grafana-to-prometheus", Namespace: "ecosystem"},
		}
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare, existing).Build()

		sm := NewSecretManager(rtClient, scheme)
		_, err := sm.CreateOrUpdate(context.Background(), sare, map[string]string{"key": "val"})
		if err != nil {
			t.Fatalf("CreateOrUpdate() returned error: %v", err)
		}

		var secret corev1.Secret
		if err := rtClient.Get(context.Background(), types.NamespacedName{Name: "grafana-to-prometheus", Namespace: "ecosystem"}, &secret); err != nil {
			t.Fatalf("secret not found: %v", err)
		}
		if len(secret.OwnerReferences) != 1 {
			t.Fatalf("OwnerReferences length = %d, want 1", len(secret.OwnerReferences))
		}
		if string(secret.OwnerReferences[0].UID) != "test-uid-123" {
			t.Errorf("owner UID = %q, want %q", secret.OwnerReferences[0].UID, "test-uid-123")
		}
	})

	t.Run("should update existing secret credentials", func(t *testing.T) {
		scheme := newTestScheme(t)
		sare := newTestSARE("grafana-to-prometheus", "ecosystem")
		existing := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "grafana-to-prometheus", Namespace: "ecosystem"},
			StringData: map[string]string{"username": "old-user", "password": "old-pass"},
		}
		rtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sare, existing).Build()

		sm := NewSecretManager(rtClient, scheme)
		_, err := sm.CreateOrUpdate(context.Background(), sare, map[string]string{"username": "new-user", "password": "new-pass"})
		if err != nil {
			t.Fatalf("CreateOrUpdate() returned error: %v", err)
		}

		var secret corev1.Secret
		if err := rtClient.Get(context.Background(), types.NamespacedName{Name: "grafana-to-prometheus", Namespace: "ecosystem"}, &secret); err != nil {
			t.Fatalf("secret not found: %v", err)
		}
		if secret.StringData["username"] != "new-user" {
			t.Errorf("username = %q, want %q", secret.StringData["username"], "new-user")
		}
	})
}

func newOwnedSecret(name, namespace string, owner *serviceaccountv1.ServiceAccountRequest, scheme *runtime.Scheme, t *testing.T) *corev1.Secret {
	t.Helper()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}
	if err := controllerutil.SetControllerReference(owner, secret, scheme); err != nil {
		t.Fatalf("SetControllerReference() error: %v", err)
	}
	return secret
}
