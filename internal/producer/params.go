package producer

import (
	"fmt"
	"sort"

	serviceaccountv1 "github.com/cloudogu/k8s-serviceaccount-lib/api/v1"
)

// Params is the list of parameters forwarded to the producer when creating a service account.
type Params []string

// NewParamsFromSpec converts a ServiceAccountRequestParams spec into Params for use with an HttpClient.
// Options are serialized as --key=value flags (sorted by key for stable ordering, repeated for multi-value keys),
// followed by positional Args. A nil spec returns nil.
func NewParamsFromSpec(spec *serviceaccountv1.ServiceAccountRequestParams) Params {
	if spec == nil {
		return nil
	}

	var result Params

	// first add options: {"repo": ["x", "y"], "role": ["admin"]} => ["--repo=x", "--repo=y", "--role=admin"]
	if len(spec.Options) > 0 {
		keys := make([]string, 0, len(spec.Options))
		for k := range spec.Options {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, k := range keys {
			for _, v := range spec.Options[k] {
				result = append(result, fmt.Sprintf("--%s=%s", k, v))
			}
		}
	}

	// then append remaining args
	result = append(result, spec.Args...)

	if len(result) == 0 {
		return nil
	}
	return result
}
