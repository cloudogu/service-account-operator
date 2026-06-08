package producer

import (
	"testing"

	serviceaccountv1 "github.com/cloudogu/k8s-serviceaccount-lib/api/v1"
	"github.com/stretchr/testify/assert"
)

func TestNewParamsFromSpec(t *testing.T) {
	t.Run("should return zero-value Params for nil spec", func(t *testing.T) {
		result := NewParamsFromSpec(nil)

		assert.Empty(t, result.Options)
		assert.Empty(t, result.Args)
	})

	t.Run("should map options and args from spec", func(t *testing.T) {
		spec := &serviceaccountv1.ServiceAccountRequestParams{
			Options: map[string][]string{"role": {"admin", "viewer"}},
			Args:    []string{"--verbose"},
		}

		result := NewParamsFromSpec(spec)

		assert.Equal(t, spec.Options, result.Options)
		assert.Equal(t, spec.Args, result.Args)
	})

	t.Run("should return zero-value Params for empty spec", func(t *testing.T) {
		result := NewParamsFromSpec(&serviceaccountv1.ServiceAccountRequestParams{})

		assert.Empty(t, result.Options)
		assert.Empty(t, result.Args)
	})
}
