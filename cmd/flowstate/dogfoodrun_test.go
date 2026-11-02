package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// receiptDocFor reads one emitted attempt doc from the temp root.
func receiptDocFor(root, id, name string) map[string]any {
	raw, err := os.ReadFile(filepath.Clean(filepath.Join(root, "attempts", id, name)))
	Expect(err).NotTo(HaveOccurred())
	var doc map[string]any
	unmarshalErr := json.Unmarshal(raw, &doc)
	Expect(unmarshalErr).NotTo(HaveOccurred())
	return doc
}

// runDogfoodLive runs `dogfood run` with both gates open (dummy key;
// the fake provider never contacts the network), the commit-sha and
// identity envs set; returns the result plus the temp metadata root.
func runDogfoodLive(args ...string) (result, string) {
	withEnv("ZAI_API_KEY", "dummy-key-value")
	withEnv("FLOWSTATE_ALLOW_LIVE", "1")
	withEnv("FLOWSTATE_COMMIT_SHA", "0123456789012345678901234567890123456789")
	withEnv("FLOWSTATE_AUTHOR_IDENTITY", "dogfood-tester")
	root := GinkgoT().TempDir()
	withEnv("FLOWSTATE_METADATA_ROOT", root)
	return runArgv(append([]string{"dogfood", "run", "--db", dbFor()}, args...)), root
}

var _ = Describe("Dogfood run live path", func() {
	BeforeEach(func() {
		clearEnv("ZAI_API_KEY", "FLOWSTATE_ALLOW_LIVE", "FLOWSTATE_COMMIT_SHA", "FLOWSTATE_AUTHOR_IDENTITY")
	})

	It("completes a fake-provider turn", func() {
		srv := fakeZaiServer(
			`{"choices":[{"delta":{"content":"he"}}]}`,
			`{"choices":[{"delta":{"content":"y"}}]}`,
		)
		withFakeZai(srv)
		res, _ := runDogfoodLive("--task", "greet")
		Expect(res.exit).To(Equal(exitOK))
	})

	It("records runtime-observed attribution from the session row", func() {
		srv := fakeZaiServer(`{"choices":[{"delta":{"content":"x"}}]}`)
		withFakeZai(srv)
		res, root := runDogfoodLive("--task", "greet")
		rec := decodeNew(res.stdout)
		attr := receiptDocFor(root, "dogfood-"+rec.ID, "attribution.json")
		Expect(attr["provider"]).To(Equal("z.ai"))
	})

	It("records a fail outcome on budget stop", func() {
		srv := fakeZaiServer(
			`{"choices":[{"delta":{"content":"aaaaaaaaaa"}}]}`,
			`{"choices":[{"delta":{"content":"bbbbbbbbbb"}}]}`,
		)
		withFakeZai(srv)
		res, root := runDogfoodLive("--task", "greet", "--budget", "5")
		rec := decodeNew(res.stdout)
		packet := receiptDocFor(root, "dogfood-"+rec.ID, "packet.json")
		Expect(packet["gate_outcome"]).To(Equal("fail"))
	})

	It("emits the transcript artefact under artefacts/", func() {
		srv := fakeZaiServer(`{"choices":[{"delta":{"content":"x"}}]}`)
		withFakeZai(srv)
		res, root := runDogfoodLive("--task", "greet")
		rec := decodeNew(res.stdout)
		artefact := filepath.Join(root, "attempts", "dogfood-"+rec.ID, "artefacts", "transcript.jsonl")
		Expect(artefactExists(artefact)).To(BeTrue())
	})
})

// artefactExists reports whether the artefact path exists on disk.
func artefactExists(path string) bool {
	_, err := os.Stat(filepath.Clean(path))
	return err == nil
}

// withEnv sets one env var for the spec, restored afterwards.
func withEnv(key, val string) {
	old, had := os.LookupEnv(key)
	_ = os.Setenv(key, val)
	DeferCleanup(func() {
		if had {
			_ = os.Setenv(key, old)
			return
		}
		_ = os.Unsetenv(key)
	})
}

// clearEnv removes env vars for the spec, restoring them afterwards.
func clearEnv(keys ...string) {
	for _, k := range keys {
		old, had := os.LookupEnv(k)
		_ = os.Unsetenv(k)
		if had {
			DeferCleanup(func() { _ = os.Setenv(k, old) })
		}
	}
}

// runDogfood runs `dogfood run` with a temp metadata root.
func runDogfood(args ...string) result {
	withEnv("FLOWSTATE_METADATA_ROOT", GinkgoT().TempDir())
	return runArgv(append([]string{"dogfood", "run", "--db", dbFor()}, args...))
}

var _ = Describe("Dogfood run gate", func() {
	BeforeEach(func() {
		clearEnv("ZAI_API_KEY", "FLOWSTATE_ALLOW_LIVE")
	})

	It("exits 4 config_invalid when neither gate env var is set", func() {
		res := runDogfood("--task", "greet")
		Expect(res.exit).To(Equal(exitConfigInvalid))
	})

	It("exits 4 config_invalid when only the api key gate is open", func() {
		withEnv("ZAI_API_KEY", "dummy-key-value")
		res := runDogfood("--task", "greet")
		Expect(res.exit).To(Equal(exitConfigInvalid))
	})

	It("exits 4 config_invalid when only the allow-live gate is open", func() {
		withEnv("FLOWSTATE_ALLOW_LIVE", "1")
		res := runDogfood("--task", "greet")
		Expect(res.exit).To(Equal(exitConfigInvalid))
	})

	It("exits 2 usage when --task is missing even with both gates open", func() {
		withEnv("ZAI_API_KEY", "dummy-key-value")
		withEnv("FLOWSTATE_ALLOW_LIVE", "1")
		res := runDogfood()
		Expect(res.exit).To(Equal(exitUsage))
	})
})
