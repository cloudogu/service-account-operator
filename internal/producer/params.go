package producer

import serviceaccountv1 "github.com/cloudogu/k8s-serviceaccount-lib/api/v1"

// NewParamsFromSpec converts a ServiceAccountRequestParams spec into Params for use with an HTTPClient.
// A nil spec returns zero-value Params.
func NewParamsFromSpec(spec *serviceaccountv1.ServiceAccountRequestParams) Params {
	if spec == nil {
		return Params{}
	}
	return Params{
		Options: spec.Options,
		Args:    spec.Args,
	}
}
