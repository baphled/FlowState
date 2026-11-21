package config_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/config"
)

// harnessConfig is the shape the older FlowState harness writes to
// config.yaml in the same directory; it does not parse as this CLI's
// document (providers.default is a string, not a provider).
const harnessConfig = "providers:\n  default: zai\n  zai:\n    api_key: harness-key\n"

var _ = Describe("provider configuration file", func() {
	var dir string

	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			panic(err)
		}
	}

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		GinkgoT().Setenv("FLOWSTATE_CONFIG_DIR", dir)
	})

	It("is named providers.yaml", func() {
		Expect(config.FileName).To(Equal("providers.yaml"))
	})

	It("lives directly in FLOWSTATE_CONFIG_DIR", func() {
		Expect(config.Path()).To(Equal(filepath.Join(dir, "providers.yaml")))
	})

	It("defaults to providers.yaml in the user configuration directory's flowstate folder", func() {
		GinkgoT().Setenv("FLOWSTATE_CONFIG_DIR", "")
		base, err := os.UserConfigDir()
		if err != nil {
			panic(err)
		}
		Expect(config.Path()).To(Equal(filepath.Join(base, "flowstate", "providers.yaml")))
	})

	It("reads providers.yaml", func() {
		write("providers.yaml", "providers:\n  zai:\n    plan: general\n")
		doc, err := config.LoadDefault()
		if err != nil {
			panic(err)
		}
		Expect(doc.GetProvider("zai").Plan).To(Equal("general"))
	})

	It("yields the default configuration when only the older harness's config.yaml exists", func() {
		write("config.yaml", harnessConfig)
		doc, err := config.LoadDefault()
		if err != nil {
			panic(err)
		}
		Expect(doc.Providers).To(BeEmpty())
	})

	It("never reads a config.yaml that would parse", func() {
		write("config.yaml", "providers:\n  zai:\n    plan: general\n")
		doc, err := config.LoadDefault()
		if err != nil {
			panic(err)
		}
		Expect(doc.GetProvider("zai")).To(BeNil())
	})

	It("uses providers.yaml when both files exist", func() {
		write("config.yaml", "providers:\n  zai:\n    plan: general\n")
		write("providers.yaml", "providers:\n  zai:\n    plan: coding\n")
		doc, err := config.LoadDefault()
		if err != nil {
			panic(err)
		}
		Expect(doc.GetProvider("zai").Plan).To(Equal("coding"))
	})

	It("rejects a providers.yaml that does not parse", func() {
		write("providers.yaml", harnessConfig)
		_, err := config.LoadDefault()
		Expect(err).To(MatchError(config.ErrInvalid))
	})
})
