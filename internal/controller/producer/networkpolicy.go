package producer

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	v2 "github.com/cloudogu/k8s-serviceaccount-lib/v2/api/v2"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

var managedResourceLabels = map[string]string{
	"app":                          "ces",
	"app.kubernetes.io/managed-by": "service-account-operator",
}

type defaultNetworkPolicyUpserter struct {
	netpolsEnabled bool
	k8sClient      client.Client
	selectorLabels map[string]string
}

func (n *defaultNetworkPolicyUpserter) UpsertNetworkPolicy(ctx context.Context, producer *v2.ServiceAccountProducer) error {
	netpolName := networkPolicyName(producer)

	if !n.netpolsEnabled {
		err := n.k8sClient.Delete(ctx, &networkingv1.NetworkPolicy{Name: netpolName, Namespace: producer.Namespace}, &client.DeleteOptions{})
		if client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("delete disabled network policy: %w", err)
		}

		return nil
	}

	networkPolicy, err := n.generate(ctx, netpolName, producer)
	if err != nil {
		return fmt.Errorf("generate network policy: %w", err)
	}

	target := &networkingv1.NetworkPolicy{Name: networkPolicy.Name, Namespace: networkPolicy.Namespace}
	_, err = controllerutil.CreateOrUpdate(ctx, n.k8sClient, target, func() error {
		target.Labels = networkPolicy.Labels
		target.Annotations = networkPolicy.Annotations
		target.OwnerReferences = networkPolicy.OwnerReferences
		target.Spec = networkPolicy.Spec
		return nil
	})
	if err != nil {
		return fmt.Errorf("create or update network policy: %w", err)
	}

	return nil
}

func (n *defaultNetworkPolicyUpserter) generate(ctx context.Context, name string, producer *v2.ServiceAccountProducer) (*networkingv1.NetworkPolicy, error) {
	svcName, svcPort, err := parseServiceNameAndPortFromEndpointUrl(producer)
	if err != nil {
		return nil, fmt.Errorf("parse service name and port from endpoint url: %w", err)
	}

	service, err := n.getService(ctx, svcName, producer.Namespace)
	if err != nil {
		return nil, fmt.Errorf("get service %q: %w", svcName, err)
	}

	targetPort, err := getNetworkPolicyTargetPort(corev1.ProtocolTCP, service, svcPort)
	if err != nil {
		return nil, fmt.Errorf("get network policy target port: %w", err)
	}

	netpol := &networkingv1.NetworkPolicy{
		Name:      name,
		Namespace: producer.Namespace,
		Labels:    managedResourceLabels,
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: service.Spec.Selector},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress: []networkingv1.NetworkPolicyIngressRule{
				{
					Ports: []networkingv1.NetworkPolicyPort{targetPort},
					From: []networkingv1.NetworkPolicyPeer{
						{
							PodSelector: &metav1.LabelSelector{MatchLabels: n.selectorLabels},
						},
					},
				},
			},
		},
	}
	err = controllerutil.SetOwnerReference(producer, netpol, n.k8sClient.Scheme())
	if err != nil {
		return nil, fmt.Errorf("set owner reference of producer %s on network policy %s: %w", producer.Name, name, err)
	}

	return netpol, nil
}

func (n *defaultNetworkPolicyUpserter) getService(ctx context.Context, svcName, namespace string) (*corev1.Service, error) {
	svc := &corev1.Service{}
	err := n.k8sClient.Get(ctx, types.NamespacedName{Name: svcName, Namespace: namespace}, svc)
	if err != nil {
		return nil, fmt.Errorf("get service %q: %w", svcName, err)
	}

	return svc, nil
}

func networkPolicyName(producer *v2.ServiceAccountProducer) string {
	return fmt.Sprintf("%s-sa-manager", producer.GetName())
}

func parseServiceNameAndPortFromEndpointUrl(producer *v2.ServiceAccountProducer) (string, int32, error) {
	endpointString := producer.Spec.HTTP.Endpoint
	if !strings.HasPrefix(endpointString, "http://") && !strings.HasPrefix(endpointString, "https://") {
		// url.Parse will fail if the endpoint string does not have a scheme.
		endpointString = fmt.Sprintf("http://%s", endpointString)
	}

	endpointUrl, err := url.Parse(endpointString)
	if err != nil {
		return "", 0, fmt.Errorf("parse endpoint url %q: %w", endpointString, err)
	}

	hostname := endpointUrl.Hostname()
	portString := endpointUrl.Port()
	if portString == "" {
		switch endpointUrl.Scheme {
		case "http":
			portString = "80"
		case "https":
			portString = "443"
		default:
			return "", 0, fmt.Errorf("determine port for endpoint url %q", endpointString)
		}
	}

	port, err := strconv.ParseInt(portString, 10, 32)
	if err != nil {
		return "", 0, fmt.Errorf("parse port %q: %w", portString, err)
	}

	// I thought about checking against the cluster domain if the endpoint URL points to a service.
	// However, it's unnecessarily complex, and we will know once we try to get the service.
	serviceName, _, _ := strings.Cut(hostname, ".")
	return serviceName, int32(port), nil
}

func getNetworkPolicyTargetPort(protocol corev1.Protocol, service *corev1.Service, servicePort int32) (networkingv1.NetworkPolicyPort, error) {
	for _, port := range service.Spec.Ports {
		if port.Port == servicePort {
			return networkingv1.NetworkPolicyPort{
				Protocol: &protocol,
				Port:     &port.TargetPort,
			}, nil
		}
	}

	return networkingv1.NetworkPolicyPort{}, fmt.Errorf("no matching port found in service %q", service.Name)
}
