package producer

import serviceaccountv1 "github.com/cloudogu/k8s-serviceaccount-lib/api/v1"

// NewParamsFromSpec converts a ServiceAccountRequestParams spec into Params for use with an HttpClient.
// A nil spec returns nil.
// TODO: How should Options (map[string][]string) be serialized into []string?
// Options represent named attributes defined by the producer schema (ProducerParams.Attributes).
// Possible conventions: CLI-style flags ("--role=admin"), key=value pairs, or a flat list.
// Needs to be aligned with what producers actually expect before Options can be included here.
func NewParamsFromSpec(spec *serviceaccountv1.ServiceAccountRequestParams) Params {
	if spec == nil {
		return nil
	}
	return Params(spec.Args)
}
