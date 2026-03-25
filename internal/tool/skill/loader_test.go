package skill_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/skill"
	"github.com/baphled/flowstate/internal/tool"
	toolskill "github.com/baphled/flowstate/internal/tool/skill"
)

type fakeLoader struct {
	skills []skill.Skill
	err    error
}

func (f *fakeLoader) LoadAll() ([]skill.Skill, error) {
	return f.skills, f.err
}

var _ = Describe("Tool", func() {
	var (
		skillTool *toolskill.Tool
		fake      *fakeLoader
	)

	BeforeEach(func() {
		fake = &fakeLoader{}
		skillTool = toolskill.New(fake)
	})

	Describe("Name", func() {
		It("returns skill_load", func() {
			Expect(skillTool.Name()).To(Equal("skill_load"))
		})
	})

	Describe("Description", func() {
		It("returns a non-empty string", func() {
			Expect(skillTool.Description()).NotTo(BeEmpty())
		})
	})

	Describe("Schema", func() {
		It("has name in Required", func() {
			schema := skillTool.Schema()
			Expect(schema.Required).To(ContainElement("name"))
		})

		It("has name property", func() {
			schema := skillTool.Schema()
			_, exists := schema.Properties["name"]
			Expect(exists).To(BeTrue())
		})
	})

	Describe("Execute", func() {
		var (
			ctx   context.Context
			input tool.Input
		)

		BeforeEach(func() {
			ctx = context.Background()
		})

		Context("with a valid skill name", func() {
			It("returns the skill content", func() {
				fake.skills = []skill.Skill{{Name: "golang", Content: "# Golang skill content"}}
				input = tool.Input{
					Name:      "skill_load",
					Arguments: map[string]interface{}{"name": "golang"},
				}
				result, err := skillTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(Equal("# Golang skill content"))
			})
		})

		Context("with missing name argument", func() {
			It("returns a Go error", func() {
				input = tool.Input{
					Name:      "skill_load",
					Arguments: map[string]interface{}{},
				}
				_, err := skillTool.Execute(ctx, input)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("name argument is required"))
			})
		})

		Context("with a non-existent skill name", func() {
			It("returns error mentioning the skill name", func() {
				fake.skills = []skill.Skill{{Name: "golang", Content: "stuff"}}
				input = tool.Input{
					Name:      "skill_load",
					Arguments: map[string]interface{}{"name": "nonexistent"},
				}
				_, err := skillTool.Execute(ctx, input)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("skill not found: nonexistent"))
			})
		})

		Context("when loader fails", func() {
			It("returns a wrapped error", func() {
				fake.err = errors.New("disk error")
				input = tool.Input{
					Name:      "skill_load",
					Arguments: map[string]interface{}{"name": "golang"},
				}
				_, err := skillTool.Execute(ctx, input)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("loading skills"))
			})
		})
	})
})
