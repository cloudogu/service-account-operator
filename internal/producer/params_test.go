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

	t.Run("should map args from spec", func(t *testing.T) {
		spec := &serviceaccountv1.ServiceAccountRequestParams{
			Args: []string{"--verbose", "--format=json"},
		}

		result := NewParamsFromSpec(spec)

		assert.Equal(t, Params{"--verbose", "--format=json"}, result)
	})

	t.Run("should return nil for empty spec", func(t *testing.T) {
		result := NewParamsFromSpec(&serviceaccountv1.ServiceAccountRequestParams{})

		assert.Nil(t, result)
	})
}
