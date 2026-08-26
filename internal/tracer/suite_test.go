package tracer_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTracer(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Tracer Suite")
}
