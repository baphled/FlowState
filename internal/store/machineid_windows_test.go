//go:build windows

package store

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Windows machine id read", func() {
	var orig func() (string, error)

	BeforeEach(func() {
		orig = readMachineGUID
		DeferCleanup(func() { readMachineGUID = orig })
	})

	It("reads MachineGuid", func() {
		readMachineGUID = func() (string, error) { return "GUID-1", nil }
		Expect(readMachineID()).To(Equal("GUID-1"))
	})

	It("reads nothing when the registry read fails", func() {
		readMachineGUID = func() (string, error) { return "", errors.New("boom") }
		Expect(readMachineID()).To(BeEmpty())
	})
})
