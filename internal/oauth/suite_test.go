package oauth_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestOAuth(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "OAuth Suite")
}
