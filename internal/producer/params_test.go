package producer

import (
	"testing"

	serviceaccountv1 "github.com/cloudogu/k8s-serviceaccount-lib/api/v1"
	"github.com/stretchr/testify/assert"
)

func TestNewParamsFromSpec(t *testing.T) {
	t.Run("should return nil for nil spec", func(t *testing.T) {
		result := NewParamsFromSpec(nil)

		assert.Nil(t, result)
	})

	t.Run("should return nil for empty spec", func(t *testing.T) {
		result := NewParamsFromSpec(&serviceaccountv1.ServiceAccountRequestParams{})

		assert.Nil(t, result)
	})

	t.Run("should map args from spec", func(t *testing.T) {
		spec := &serviceaccountv1.ServiceAccountRequestParams{
			Args: []string{"--verbose", "--format=json"},
		}

		result := NewParamsFromSpec(spec)

		assert.Equal(t, Params{"--verbose", "--format=json"}, result)
	})

	t.Run("should serialize single-value options as --key=value flags", func(t *testing.T) {
		spec := &serviceaccountv1.ServiceAccountRequestParams{
			Options: map[string][]string{
				"role": {"admin"},
			},
		}

		result := NewParamsFromSpec(spec)

		assert.Equal(t, Params{"--role=admin"}, result)
	})

	t.Run("should repeat flag for multi-value options", func(t *testing.T) {
		spec := &serviceaccountv1.ServiceAccountRequestParams{
			Options: map[string][]string{
				"repo": {"x", "y"},
			},
		}

		result := NewParamsFromSpec(spec)

		assert.Equal(t, Params{"--repo=x", "--repo=y"}, result)
	})

	t.Run("should sort options by key and append args", func(t *testing.T) {
		spec := &serviceaccountv1.ServiceAccountRequestParams{
			Options: map[string][]string{
				"repo": {"x", "y"},
				"role": {"admin"},
			},
			Args: []string{"--verbose"},
		}

		result := NewParamsFromSpec(spec)

		assert.Equal(t, Params{"--repo=x", "--repo=y", "--role=admin", "--verbose"}, result)
	})

	t.Run("should return nil when options and args are both empty", func(t *testing.T) {
		spec := &serviceaccountv1.ServiceAccountRequestParams{
			Options: map[string][]string{},
			Args:    []string{},
		}

		result := NewParamsFromSpec(spec)

		assert.Nil(t, result)
	})
}
