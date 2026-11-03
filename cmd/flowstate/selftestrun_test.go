package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// selfTestReceiptDoc reads one emitted attempt doc from the temp root.
func selfTestReceiptDoc(root, id, name string) map[string]any {
	raw, err := os.ReadFile(filepath.Clean(filepath.Join(root, "attempts", id, name)))
	Expect(err).NotTo(HaveOccurred())
	var doc map[string]any
	unmarshalErr := json.Unmarshal(raw, &doc)
	Expect(unmarshalErr).NotTo(HaveOccurred())
	return doc
}

// runSelfTestLive runs `session run --task` with both gates open
// (dummy key; the fake provider never contacts the network), the
// commit-sha and identity envs set; returns the result plus the temp
// metadata root.
func runSelfTestLive(args ...string) (result, string) {
	withSelfTestEnv("ZAI_API_KEY", "dummy-key-value")
	withSelfTestEnv("FLOWSTATE_ALLOW_LIVE", "1")
	withSelfTestEnv("FLOWSTATE_COMMIT_SHA", "0123456789012345678901234567890123456789")
	withSelfTestEnv("FLOWSTATE_AUTHOR_IDENTITY", "selftest-tester")
	root := GinkgoT().TempDir()
	withSelfTestEnv("FLOWSTATE_METADATA_ROOT", root)
	return runArgv(append([]string{"session", "run", "--db", dbFor()}, args...)), root
}

var _ = Describe("Session run live self-test path", func() {
	BeforeEach(func() {
		clearSelfTestEnv("ZAI_API_KEY", "FLOWSTATE_ALLOW_LIVE", "FLOWSTATE_COMMIT_SHA", "FLOWSTATE_AUTHOR_IDENTITY")
	})

	It("completes a fake-provider turn", func() {
		srv := fakeZaiServer(
			`{"choices":[{"delta":{"content":"he"}}]}`,
			`{"choices":[{"delta":{"content":"y"}}]}`,
		)
		withFakeZai(srv)
		res, _ := runSelfTestLive("--task", "greet")
		Expect(res.exit).To(Equal(exitOK))
	})

	It("emits the standard session events on the run path", func() {
		srv := fakeZaiServer(`{"choices":[{"delta":{"content":"x"}}]}`)
		withFakeZai(srv)
		res, _ := runSelfTestLive("--task", "greet")
		Expect(res.stdout).To(ContainSubstring("turn.start"))
		Expect(res.stdout).To(ContainSubstring("turn."))
	})

	It("records runtime-observed attribution from the session row", func() {
		srv := fakeZaiServer(`{"choices":[{"delta":{"content":"x"}}]}`)
		withFakeZai(srv)
		res, root := runSelfTestLive("--task", "greet")
		rec := decodeNew(res.stdout)
		attr := selfTestReceiptDoc(root, "selftest-"+rec.ID, "attribution.json")
		Expect(attr["provider"]).To(Equal("z.ai"))
	})

	It("records a fail outcome on budget stop", func() {
		srv := fakeZaiServer(
			`{"choices":[{"delta":{"content":"aaaaaaaaaa"}}]}`,
			`{"choices":[{"delta":{"content":"bbbbbbbbbb"}}]}`,
		)
		withFakeZai(srv)
		res, root := runSelfTestLive("--task", "greet", "--budget", "5")
		rec := decodeNew(res.stdout)
		packet := selfTestReceiptDoc(root, "selftest-"+rec.ID, "packet.json")
		Expect(packet["gate_outcome"]).To(Equal("fail"))
	})

	It("emits the transcript artefact under artefacts/", func() {
		srv := fakeZaiServer(`{"choices":[{"delta":{"content":"x"}}]}`)
		withFakeZai(srv)
		res, root := runSelfTestLive("--task", "greet")
		rec := decodeNew(res.stdout)
		artefact := filepath.Join(root, "attempts", "selftest-"+rec.ID, "artefacts", "transcript.jsonl")
		Expect(artefactExists(artefact)).To(BeTrue())
	})

	It("exits 2 usage when an id and --task are both given", func() {
		withSelfTestEnv("ZAI_API_KEY", "dummy-key-value")
		withSelfTestEnv("FLOWSTATE_ALLOW_LIVE", "1")
		res := runSelfTestEnvOnly("session", "run", "--db", dbFor(), "--task", "greet", "some-id")
		Expect(res.exit).To(Equal(exitUsage))
	})
})

// runSelfTest runs `session run --task` with a temp metadata root.
func runSelfTest(args ...string) result {
	withSelfTestEnv("FLOWSTATE_METADATA_ROOT", GinkgoT().TempDir())
	return runArgv(append([]string{"session", "run", "--db", dbFor()}, args...))
}

// runSelfTestEnvOnly runs an argv with a temp metadata root only.
func runSelfTestEnvOnly(argv ...string) result {
	withSelfTestEnv("FLOWSTATE_METADATA_ROOT", GinkgoT().TempDir())
	return runArgv(argv)
}

var _ = Describe("Session run live gate", func() {
	BeforeEach(func() {
		clearSelfTestEnv("ZAI_API_KEY", "FLOWSTATE_ALLOW_LIVE")
	})

	It("exits 4 config_invalid when neither gate env var is set", func() {
		res := runSelfTest("--task", "greet")
		Expect(res.exit).To(Equal(exitConfigInvalid))
	})

	It("exits 4 config_invalid when only the api key gate is open", func() {
		withSelfTestEnv("ZAI_API_KEY", "dummy-key-value")
		res := runSelfTest("--task", "greet")
		Expect(res.exit).To(Equal(exitConfigInvalid))
	})

	It("exits 4 config_invalid when only the allow-live gate is open", func() {
		withSelfTestEnv("FLOWSTATE_ALLOW_LIVE", "1")
		res := runSelfTest("--task", "greet")
		Expect(res.exit).To(Equal(exitConfigInvalid))
	})
})

// withSelfTestEnv sets one env var for the spec, restored afterwards.
func withSelfTestEnv(key, val string) {
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

// clearSelfTestEnv removes env vars for the spec, restoring them.
func clearSelfTestEnv(keys ...string) {
	for _, k := range keys {
		old, had := os.LookupEnv(k)
		_ = os.Unsetenv(k)
		if had {
			DeferCleanup(func() { _ = os.Setenv(k, old) })
		}
	}
}

// artefactExists reports whether the artefact path exists on disk.
func artefactExists(path string) bool {
	_, err := os.Stat(filepath.Clean(path))
	return err == nil
}
