package themes_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestThemes(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Themes Suite")
}
