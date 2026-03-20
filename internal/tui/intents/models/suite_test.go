package models_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestModelSelector(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Model Selector Intent Suite")
}
