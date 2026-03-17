package ollama

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProvider_Name(t *testing.T) {
	p := &Provider{}
	assert.Equal(t, "ollama", p.Name())
}

func TestBoolPtr(t *testing.T) {
	trueVal := boolPtr(true)
	assert.NotNil(t, trueVal)
	assert.True(t, *trueVal)

	falseVal := boolPtr(false)
	assert.NotNil(t, falseVal)
	assert.False(t, *falseVal)
}
