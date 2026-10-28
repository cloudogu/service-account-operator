package producer

import (
	"context"
	"fmt"
	"testing"

	serviceaccountv2 "github.com/cloudogu/k8s-serviceaccount-lib/v2/api/v2"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func Test_defaultNetworkPolicyUpserter_UpsertNetworkPolicy(t *testing.T) {
	type fields struct {
		netpolsEnabled bool
		k8sClient      client.Client
		selectorLabels map[string]string
	}
	tests := []struct {
		name     string
		fields   fields
		producer *serviceaccountv2.ServiceAccountProducer
		wantErr  assert.ErrorAssertionFunc
		expected *networkingv1.NetworkPolicy
	}{
		{
			name: "netpols disabled: should succeed if not found",
			fields: fields{
				netpolsEnabled: false,
				k8sClient:      fake.NewClientBuilder().Build(),
			},
			producer: &serviceaccountv2.ServiceAccountProducer{Name: "test", Namespace: "test"},
			wantErr:  assert.NoError,
		},
		{
			name: "netpols disabled: should succeed delete",
			fields: fields{
				netpolsEnabled: false,
				k8sClient: fake.NewClientBuilder().
					WithObjects(&networkingv1.NetworkPolicy{Name: "test-sa-manager", Namespace: "test"}).
					Build(),
			},
			producer: &serviceaccountv2.ServiceAccountProducer{Name: "test", Namespace: "test"},
			wantErr:  assert.NoError,
		},
		{
			name: "netpols disabled: should fail delete",
			fields: fields{
				netpolsEnabled: false,
				k8sClient: fake.NewClientBuilder().
					WithInterceptorFuncs(interceptor.Funcs{
						Delete: func(ctx context.Context, client client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
							return assert.AnError
						},
					}).
					WithObjects(&networkingv1.NetworkPolicy{Name: "test-sa-manager", Namespace: "test"}).
					Build(),
			},
			producer: &serviceaccountv2.ServiceAccountProducer{Name: "test", Namespace: "test"},
			wantErr: func(t assert.TestingT, err error, i ...any) bool {
				return assert.ErrorIs(t, err, assert.AnError, i) &&
					assert.ErrorContains(t, err, "delete disabled network policy", i)
			},
			expected: &networkingv1.NetworkPolicy{Name: "test-sa-manager", Namespace: "test", ResourceVersion: "999"},
		},
		{
			name: "netpols enabled: should fail to parse endpoint url",
			fields: fields{
				netpolsEnabled: true,
				k8sClient:      fake.NewClientBuilder().Build(),
			},
			producer: &serviceaccountv2.ServiceAccountProducer{Name: "test", Namespace: "test",
				Spec: serviceaccountv2.ServiceAccountProducerSpec{HTTP: &serviceaccountv2.HTTPProducer{Endpoint: "\x7f"}}},
			wantErr: func(t assert.TestingT, err error, i ...any) bool {
				return assert.ErrorContains(t, err, "generate network policy", i) &&
					assert.ErrorContains(t, err, "parse service name and port from endpoint url", i) &&
					assert.ErrorContains(t, err, "parse endpoint url \"http://\\x7f\"", i)
			},
		},
		{
			name: "netpols enabled: should fail to get service",
			fields: fields{
				netpolsEnabled: true,
				k8sClient:      fake.NewClientBuilder().Build(),
			},
			producer: &serviceaccountv2.ServiceAccountProducer{Name: "test", Namespace: "test",
				Spec: serviceaccountv2.ServiceAccountProducerSpec{HTTP: &serviceaccountv2.HTTPProducer{Endpoint: "http://test.namespace.svc.cluster.local:8080"}}},
			wantErr: func(t assert.TestingT, err error, i ...any) bool {
				return assert.ErrorContains(t, err, "generate network policy", i) &&
					assert.ErrorContains(t, err, "get service \"test\"", i)
			},
		},
		{
			name: "netpols enabled: should fail to get target port",
			fields: fields{
				netpolsEnabled: true,
				k8sClient: fake.NewClientBuilder().
					WithObjects(&corev1.Service{Name: "test", Namespace: "test"}).
					Build(),
			},
			producer: &serviceaccountv2.ServiceAccountProducer{Name: "test", Namespace: "test",
				Spec: serviceaccountv2.ServiceAccountProducerSpec{HTTP: &serviceaccountv2.HTTPProducer{Endpoint: "http://test.namespace.svc.cluster.local:8080"}}},
			wantErr: func(t assert.TestingT, err error, i ...any) bool {
				return assert.ErrorContains(t, err, "generate network policy", i) &&
					assert.ErrorContains(t, err, "get network policy target port", i) &&
					assert.ErrorContains(t, err, "no matching port found in service \"test\"", i)
			},
		},
		{
			name: "netpols enabled: should fail to set owner reference",
			fields: fields{
				netpolsEnabled: true,
				k8sClient: fake.NewClientBuilder().
					WithObjects(&corev1.Service{Name: "test", Namespace: "test",
						Spec: corev1.ServiceSpec{
							Ports: []corev1.ServicePort{{Port: 8080, TargetPort: intstr.FromInt32(8000)}},
						}}).
					Build(),
			},
			producer: &serviceaccountv2.ServiceAccountProducer{Name: "test", Namespace: "test",
				Spec: serviceaccountv2.ServiceAccountProducerSpec{HTTP: &serviceaccountv2.HTTPProducer{Endpoint: "http://test.namespace.svc.cluster.local:8080"}}},
			wantErr: func(t assert.TestingT, err error, i ...any) bool {
				return assert.ErrorContains(t, err, "generate network policy", i) &&
					assert.ErrorContains(t, err, "set owner reference of producer \"test\" on network policy \"test-sa-manager\"", i)
			},
		},
		{
			name: "netpols enabled: should succeed with coercion to http and port 80",
			fields: fields{
				netpolsEnabled: true,
				k8sClient: fake.NewClientBuilder().
					WithScheme(newTestScheme(t)).
					WithObjects(&corev1.Service{Name: "test", Namespace: "test",
						Spec: corev1.ServiceSpec{
							Selector: map[string]string{"app.kubernetes.io/component": "test"},
							Ports:    []corev1.ServicePort{{Port: 80, TargetPort: intstr.FromInt32(8080)}},
						}}).
					Build(),
				selectorLabels: map[string]string{"k8s.cloudogu.com/component.name": "service-account-operator"},
			},
			producer: &serviceaccountv2.ServiceAccountProducer{Name: "test", Namespace: "test", UID: "1234",
				Spec: serviceaccountv2.ServiceAccountProducerSpec{HTTP: &serviceaccountv2.HTTPProducer{Endpoint: "test.namespace.svc.cluster.local"}}},
			wantErr: assert.NoError,
			expected: &networkingv1.NetworkPolicy{Name: "test-sa-manager", Namespace: "test", ResourceVersion: "1",
				OwnerReferences: []metav1.OwnerReference{{APIVersion: "k8s.cloudogu.com/v2", Kind: "ServiceAccountProducer", Name: "test", UID: "1234"}},
				Labels: map[string]string{
					"app":                          "ces",
					"app.kubernetes.io/managed-by": "service-account-operator",
				},
				Spec: networkingv1.NetworkPolicySpec{
					PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/component": "test"}},
					PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
					Ingress: []networkingv1.NetworkPolicyIngressRule{
						{
							Ports: []networkingv1.NetworkPolicyPort{{
								Protocol: new(corev1.ProtocolTCP),
								Port:     new(intstr.FromInt32(8080)),
							}},
							From: []networkingv1.NetworkPolicyPeer{
								{
									PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{
										"k8s.cloudogu.com/component.name": "service-account-operator",
									}},
									NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{
										"kubernetes.io/metadata.name": "test",
									}},
								},
							},
						},
					},
				}},
		},
		{
			name: "netpols enabled: should succeed with coercion to port 443 on https",
			fields: fields{
				netpolsEnabled: true,
				k8sClient: fake.NewClientBuilder().
					WithScheme(newTestScheme(t)).
					WithObjects(&corev1.Service{Name: "test", Namespace: "test",
						Spec: corev1.ServiceSpec{
							Selector: map[string]string{"app.kubernetes.io/component": "test"},
							Ports:    []corev1.ServicePort{{Port: 443, TargetPort: intstr.FromInt32(8443)}},
						}}).
					Build(),
				selectorLabels: map[string]string{"k8s.cloudogu.com/component.name": "service-account-operator"},
			},
			producer: &serviceaccountv2.ServiceAccountProducer{Name: "test", Namespace: "test", UID: "1234",
				Spec: serviceaccountv2.ServiceAccountProducerSpec{HTTP: &serviceaccountv2.HTTPProducer{Endpoint: "https://test.namespace.svc.cluster.local"}}},
			wantErr: assert.NoError,
			expected: &networkingv1.NetworkPolicy{Name: "test-sa-manager", Namespace: "test", ResourceVersion: "1",
				OwnerReferences: []metav1.OwnerReference{{APIVersion: "k8s.cloudogu.com/v2", Kind: "ServiceAccountProducer", Name: "test", UID: "1234"}},
				Labels: map[string]string{
					"app":                          "ces",
					"app.kubernetes.io/managed-by": "service-account-operator",
				},
				Spec: networkingv1.NetworkPolicySpec{
					PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/component": "test"}},
					PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
					Ingress: []networkingv1.NetworkPolicyIngressRule{
						{
							Ports: []networkingv1.NetworkPolicyPort{{
								Protocol: new(corev1.ProtocolTCP),
								Port:     new(intstr.FromInt32(8443)),
							}},
							From: []networkingv1.NetworkPolicyPeer{
								{
									PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{
										"k8s.cloudogu.com/component.name": "service-account-operator",
									}},
									NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{
										"kubernetes.io/metadata.name": "test",
									}},
								},
							},
						},
					},
				}},
		},
		{
			name: "netpols enabled: should succeed",
			fields: fields{
				netpolsEnabled: true,
				k8sClient: fake.NewClientBuilder().
					WithScheme(newTestScheme(t)).
					WithObjects(&corev1.Service{Name: "test", Namespace: "test",
						Spec: corev1.ServiceSpec{
							Selector: map[string]string{"app.kubernetes.io/component": "test"},
							Ports:    []corev1.ServicePort{{Port: 8080, TargetPort: intstr.FromInt32(8000)}},
						}}).
					Build(),
				selectorLabels: map[string]string{"k8s.cloudogu.com/component.name": "service-account-operator"},
			},
			producer: &serviceaccountv2.ServiceAccountProducer{Name: "test", Namespace: "test", UID: "1234",
				Spec: serviceaccountv2.ServiceAccountProducerSpec{HTTP: &serviceaccountv2.HTTPProducer{Endpoint: "http://test.namespace.svc.cluster.local:8080"}}},
			wantErr: assert.NoError,
			expected: &networkingv1.NetworkPolicy{Name: "test-sa-manager", Namespace: "test", ResourceVersion: "1",
				OwnerReferences: []metav1.OwnerReference{{APIVersion: "k8s.cloudogu.com/v2", Kind: "ServiceAccountProducer", Name: "test", UID: "1234"}},
				Labels: map[string]string{
					"app":                          "ces",
					"app.kubernetes.io/managed-by": "service-account-operator",
				},
				Spec: networkingv1.NetworkPolicySpec{
					PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/component": "test"}},
					PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
					Ingress: []networkingv1.NetworkPolicyIngressRule{
						{
							Ports: []networkingv1.NetworkPolicyPort{{
								Protocol: new(corev1.ProtocolTCP),
								Port:     new(intstr.FromInt32(8000)),
							}},
							From: []networkingv1.NetworkPolicyPeer{
								{
									PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{
										"k8s.cloudogu.com/component.name": "service-account-operator",
									}},
									NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{
										"kubernetes.io/metadata.name": "test",
									}},
								},
							},
						},
					},
				}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := &defaultNetworkPolicyUpserter{
				netpolsEnabled: tt.fields.netpolsEnabled,
				k8sClient:      tt.fields.k8sClient,
				selectorLabels: tt.fields.selectorLabels,
			}
			tt.wantErr(t, n.UpsertNetworkPolicy(t.Context(), tt.producer))

			actual := &networkingv1.NetworkPolicy{}
			err := n.k8sClient.Get(t.Context(), types.NamespacedName{Name: fmt.Sprintf("%s-sa-manager", tt.producer.Name), Namespace: tt.producer.Namespace}, actual)
			if apierrors.IsNotFound(err) && tt.expected == nil {
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.expected, actual)
		})
	}
}
