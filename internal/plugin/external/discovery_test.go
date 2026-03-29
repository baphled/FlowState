package external_test

import (
	"encoding/json"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/plugin/external"
	"github.com/baphled/flowstate/internal/plugin/manifest"
)

var _ = Describe("Discoverer", func() {
	var (
		discoverer *external.Discoverer
		pluginDir  string
		cfg        config.PluginsConfig
	)

	BeforeEach(func() {
		pluginDir = GinkgoT().TempDir()
		cfg = config.PluginsConfig{}
		discoverer = external.NewDiscoverer(cfg)
	})

	Describe("Discover", func() {
		Context("with a valid plugin manifest", func() {
			BeforeEach(func() {
				createPluginDir(pluginDir, "my-plugin", validManifest("my-plugin"))
			})

			It("returns the discovered manifest", func() {
				manifests, err := discoverer.Discover(pluginDir)
				Expect(err).NotTo(HaveOccurred())
				Expect(manifests).To(HaveLen(1))
				Expect(manifests[0].Name).To(Equal("my-plugin"))
			})
		})

		Context("with an invalid plugin manifest", func() {
			BeforeEach(func() {
				createPluginDir(pluginDir, "bad-plugin", []byte(`{"name": ""}`))
			})

			It("skips the invalid manifest and returns an empty slice", func() {
				manifests, err := discoverer.Discover(pluginDir)
				Expect(err).NotTo(HaveOccurred())
				Expect(manifests).To(BeEmpty())
			})
		})

		Context("with an enabled filter", func() {
			BeforeEach(func() {
				createPluginDir(pluginDir, "alpha", validManifest("alpha"))
				createPluginDir(pluginDir, "beta", validManifest("beta"))
				cfg = config.PluginsConfig{Enabled: []string{"alpha"}}
				discoverer = external.NewDiscoverer(cfg)
			})

			It("returns only the enabled plugin", func() {
				manifests, err := discoverer.Discover(pluginDir)
				Expect(err).NotTo(HaveOccurred())
				Expect(manifests).To(HaveLen(1))
				Expect(manifests[0].Name).To(Equal("alpha"))
			})
		})

		Context("with a disabled filter", func() {
			BeforeEach(func() {
				createPluginDir(pluginDir, "alpha", validManifest("alpha"))
				createPluginDir(pluginDir, "beta", validManifest("beta"))
				cfg = config.PluginsConfig{Disabled: []string{"beta"}}
				discoverer = external.NewDiscoverer(cfg)
			})

			It("excludes the disabled plugin", func() {
				manifests, err := discoverer.Discover(pluginDir)
				Expect(err).NotTo(HaveOccurred())
				Expect(manifests).To(HaveLen(1))
				Expect(manifests[0].Name).To(Equal("alpha"))
			})
		})

		Context("with an empty directory", func() {
			It("returns an empty slice without error", func() {
				manifests, err := discoverer.Discover(pluginDir)
				Expect(err).NotTo(HaveOccurred())
				Expect(manifests).To(BeEmpty())
			})
		})
	})
})

func createPluginDir(base, name string, manifestData []byte) {
	dir := filepath.Join(base, name)
	Expect(os.MkdirAll(dir, 0o755)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(dir, "manifest.json"), manifestData, 0o600)).To(Succeed())
}

func validManifest(name string) []byte {
	m := manifest.Manifest{
		Name:    name,
		Version: "1.0.0",
		Command: "/usr/bin/" + name,
		Hooks:   []string{"chat.params"},
	}
	data, err := json.Marshal(m)
	Expect(err).NotTo(HaveOccurred())
	return data
}
