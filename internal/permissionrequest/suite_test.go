package permissionrequest_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPermissionRequest(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "PermissionRequest Suite")
}
