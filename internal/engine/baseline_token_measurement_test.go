package engine_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/config"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/hook"
	"github.com/baphled/flowstate/internal/provider"
)

type baselineTokenEvidence struct {
	SystemPromptTokens       int    `json:"system_prompt_tokens"`
	SkillHeaderTokens        int    `json:"skill_header_tokens"`
	EstimatedFirstTurnTokens int    `json:"estimated_first_turn_tokens"`
	SkillCount               int    `json:"skill_count"`
	TotalSkillBytes          int64  `json:"total_skill_bytes"`
	SkillDirPath             string `json:"skill_dir_path"`
}

var _ = Describe("WriteBaselineTokenMeasurementEvidence", func() {
	It("captures pre-skill-cache token measurements as JSON evidence (task-1 baseline)", func() {
		appCfg, err := config.LoadConfig()
		Expect(err).NotTo(HaveOccurred())

		manifest := agent.Manifest{
			ID:   "baseline-token-measurement",
			Name: "Baseline Token Measurement",
			Instructions: agent.Instructions{
				SystemPrompt: "You are a helpful assistant.",
			},
			Capabilities: agent.Capabilities{
				AlwaysActiveSkills: appCfg.AlwaysActiveSkills,
			},
		}

		eng := engine.New(engine.Config{
			ChatProvider: &mockProvider{name: "measurement-provider"},
			Manifest:     manifest,
			TokenCounter: ctxstore.NewTiktokenCounter(),
		})

		counter := ctxstore.NewTiktokenCounter()
		systemPrompt := eng.BuildSystemPrompt()
		systemPromptTokens := counter.Count(systemPrompt)

		autoloadCfg := hook.DefaultSkillAutoLoaderConfig()
		leanHeader := "Your load_skills: [" + strings.Join(autoloadCfg.BaselineSkills, ", ") + "]. Use skill_load(name) only when relevant to the current task."
		skillHeaderTokens := counter.Count(leanHeader)

		request := &provider.ChatRequest{
			Messages: []provider.Message{
				{Role: "system", Content: systemPrompt},
				{Role: "user", Content: "measure baseline tokens"},
			},
		}

		captured := request
		passthrough := func(_ context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
			captured = req
			ch := make(chan provider.StreamChunk, 1)
			ch <- provider.StreamChunk{Done: true}
			close(ch)
			return ch, nil
		}

		hookFn := hook.SkillAutoLoaderHook(autoloadCfg, func() agent.Manifest { return manifest }, nil, nil)
		wrapped := hookFn(passthrough)
		_, err = wrapped(context.Background(), request)
		Expect(err).NotTo(HaveOccurred())
		Expect(captured.Messages).To(HaveLen(2))
		Expect(captured.Messages[0].Content).To(ContainSubstring("Your load_skills: ["))

		skillCount, totalSkillBytes, err := countSkillFiles(appCfg.SkillDir)
		Expect(err).NotTo(HaveOccurred())

		combinedFirstTurnTokens := systemPromptTokens + skillHeaderTokens + estimatedBaselineSkillTokens(totalSkillBytes)

		Expect(systemPromptTokens).To(BeNumerically(">", 0))
		Expect(skillHeaderTokens).To(BeNumerically(">", 0))
		Expect(combinedFirstTurnTokens).To(BeNumerically(">", 0))
		Expect(skillCount).To(BeNumerically(">", 0))
		Expect(totalSkillBytes).To(BeNumerically(">", 0))

		evidence := baselineTokenEvidence{
			SystemPromptTokens:       systemPromptTokens,
			SkillHeaderTokens:        skillHeaderTokens,
			EstimatedFirstTurnTokens: combinedFirstTurnTokens,
			SkillCount:               skillCount,
			TotalSkillBytes:          totalSkillBytes,
			SkillDirPath:             appCfg.SkillDir,
		}

		data, err := json.MarshalIndent(evidence, "", "  ")
		Expect(err).NotTo(HaveOccurred())

		evidencePath := filepath.Join("..", "..", ".sisyphus", "evidence", "task-1-baseline-tokens.json")
		Expect(os.MkdirAll(filepath.Dir(evidencePath), 0o755)).To(Succeed())
		Expect(os.WriteFile(evidencePath, data, 0o600)).To(Succeed())
		_, err = os.Stat(evidencePath)
		Expect(err).NotTo(HaveOccurred())
	})
})

func countSkillFiles(skillDir string) (int, int64, error) {
	var count int
	var total int64

	err := filepath.WalkDir(skillDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || d.Name() != "SKILL.md" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		count++
		total += info.Size()
		return nil
	})

	return count, total, err
}

func estimatedBaselineSkillTokens(totalSkillBytes int64) int {
	if totalSkillBytes <= 0 {
		return 0
	}
	return int((totalSkillBytes + 3) / 4)
}
