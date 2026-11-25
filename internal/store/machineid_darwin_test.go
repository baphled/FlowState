//go:build darwin

package store

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("macOS machine id read", func() {
	var orig func() ([]byte, error)

	BeforeEach(func() {
		orig = runIoreg
		DeferCleanup(func() { runIoreg = orig })
	})

	It("parses IOPlatformUUID from ioreg output", func() {
		runIoreg = func() ([]byte, error) {
			return []byte("+-o Root\n    {\n      \"IOPlatformSerialNumber\" = \"C02X\"\n      \"IOPlatformUUID\" = \"1A2B3C4D-0000-4111-8222-ABCDEF012345\"\n    }\n"), nil
		}
		Expect(readMachineID()).To(Equal("1A2B3C4D-0000-4111-8222-ABCDEF012345"))
	})

	It("reads nothing when ioreg fails", func() {
		runIoreg = func() ([]byte, error) { return nil, errors.New("boom") }
		Expect(readMachineID()).To(BeEmpty())
	})

	It("reads nothing when the UUID is malformed", func() {
		runIoreg = func() ([]byte, error) { return []byte(`"IOPlatformUUID" = "not-a-uuid"`), nil }
		Expect(readMachineID()).To(BeEmpty())
	})
})
