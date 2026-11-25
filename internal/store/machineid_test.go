package store

import (
	"errors"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("MachineIdentity", func() {
	const id = "0123456789abcdef0123456789abcdef"
	var origRead func() string
	var origHost func() (string, error)

	BeforeEach(func() {
		origRead, origHost = readMachineID, hostName
		DeferCleanup(func() { readMachineID, hostName = origRead, origHost })
		readMachineID = func() string { return id + "\n" }
		hostName = func() (string, error) { return "box", nil }
	})

	It("never contains the raw id", func() {
		Expect(MachineIdentity()).NotTo(ContainSubstring(id))
	})

	It("never contains any 8-character substring of the id", func() {
		got := MachineIdentity()
		found := false
		for i := 0; i+8 <= len(id); i++ {
			if strings.Contains(got, id[i:i+8]) {
				found = true
			}
		}
		Expect(found).To(BeFalse())
	})

	It("is the HMAC-SHA256 of the fixed message keyed by the id", func() {
		Expect(MachineIdentity()).To(Equal(hashMachineID(id)))
	})

	It("pins the HMAC value", func() {
		Expect(hashMachineID("k")).To(Equal(
			"19d47de79154087866e530d3bb80525691c483593f208edc7cefa46cd234eb68"))
	})

	It("gives the same value for the same id", func() {
		Expect(MachineIdentity()).To(Equal(MachineIdentity()))
	})

	It("gives different values for different ids", func() {
		first := MachineIdentity()
		readMachineID = func() string { return "ffffffffffffffffffffffffffffffff" }
		Expect(MachineIdentity()).NotTo(Equal(first))
	})

	It("falls back to host:<name> when no id is readable", func() {
		readMachineID = func() string { return "  \n" }
		Expect(MachineIdentity()).To(Equal("host:box"))
	})

	It("is empty when neither an id nor a host name is available", func() {
		readMachineID = func() string { return "" }
		hostName = func() (string, error) { return "", errors.New("no host") }
		Expect(MachineIdentity()).To(BeEmpty())
	})
})
