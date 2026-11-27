package agent_test

import (
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
)

// skillKnown is the session catalog as internal/app names it once a profile may list skill.load.
var skillKnown = append(append([]string{}, known...), agent.SkillLoadTool)

// skilled builds a profile named a whose tools line and skills line are given (each with its newline).
func skilled(tools, skills string) string {
	return profileText(tools+skills, "Do it.")
}

// skillTools is a tools line that may carry skills.
const skillTools = "tools: [skill.load, fs.read]\n"

// entryList is the YAML of a skills list.
func entryList(entries ...string) string {
	return "skills: [" + strings.Join(entries, ", ") + "]\n"
}

// manyEntries is a skills list of n distinct one-word names.
func manyEntries(n int) []string {
	names := make([]string, n)
	for i := range names {
		names[i] = "s" + strconv.Itoa(i)
	}
	return names
}

var _ = Describe("skills field", func() {
	It("reads the listed names and prefixes in order", func() {
		p, err := agent.Parse([]byte(skilled(skillTools, entryList("code-review", "tdd", "refactor-*"))), skillKnown)
		if err != nil {
			panic(err)
		}
		Expect(p.Skills).To(Equal([]string{"code-review", "tdd", "refactor-*"}))
	})

	It("offers a profile with no skills key nothing, and leaves it out of agents list", func() {
		p, err := agent.Parse([]byte(profileText("tools: [fs.read]\n", "x")), skillKnown)
		if err != nil {
			panic(err)
		}
		Expect(p.Skills).To(BeEmpty())
	})

	It("reads a null skills as no skills", func() {
		p, err := agent.Parse([]byte(skilled(skillTools, "skills:\n")), skillKnown)
		if err != nil {
			panic(err)
		}
		Expect(p.Skills).To(BeNil())
	})

	It("accepts a quoted star alone: every skill, which validate warns about", func() {
		p, err := agent.Parse([]byte(skilled(skillTools, entryList(`"*"`))), skillKnown)
		if err != nil {
			panic(err)
		}
		Expect(p.Skills).To(Equal([]string{"*"}))
	})

	It("accepts a name the loaded set may lack: whether it matches is the run's check, not the parser's", func() {
		p, err := agent.Parse([]byte(skilled(skillTools, entryList("not-installed-yet"))), skillKnown)
		if err != nil {
			panic(err)
		}
		Expect(p.Skills).To(Equal([]string{"not-installed-yet"}))
	})

	It("accepts the largest list: 64 entries", func() {
		p, err := agent.Parse([]byte(skilled(skillTools, entryList(manyEntries(64)...))), skillKnown)
		if err != nil {
			panic(err)
		}
		Expect(p.Skills).To(HaveLen(64))
	})

	DescribeTable("refuses",
		func(text, reason string) {
			_, err := agent.Parse([]byte(text), skillKnown)
			Expect(err).To(MatchError(ContainSubstring(reason)))
		},
		Entry("skills without skill.load in tools", skilled("tools: [fs.read]\n", entryList("tdd")), "skills needs skill.load in tools"),
		Entry("skills with no tools at all", profileText(entryList("tdd"), "x"), "skills needs skill.load in tools"),
		Entry("a list over the cap: 65 entries", skilled(skillTools, entryList(manyEntries(65)...)),
			"skills lists more than 64 entries"),
		Entry("a duplicate", skilled(skillTools, entryList("tdd", "tdd")), `lists "tdd" twice`),
		Entry("a name of 65 characters", skilled(skillTools, entryList(strings.Repeat("s", 65))), "must be a string that is a skill name or a prefix"),
		Entry("an upper-case name", skilled(skillTools, entryList("Tdd")), "must be a string that is a skill name or a prefix"),
		Entry("a trailing hyphen as a name", skilled(skillTools, entryList("tdd-")), "must be a string that is a skill name or a prefix"),
		Entry("a path form", skilled(skillTools, entryList("reference/guide")), "must be a string that is a skill name or a prefix"),
		Entry("a dot form", skilled(skillTools, entryList("..")), "must be a string that is a skill name or a prefix"),
		Entry("a star inside a name", skilled(skillTools, entryList("co*de")), "must be a string that is a skill name or a prefix"),
		Entry("two stars", skilled(skillTools, entryList("code-**")), "must be a string that is a skill name or a prefix"),
		Entry("a prefix over 64 characters", skilled(skillTools, entryList(strings.Repeat("s", 65)+"*")), "must be a string that is a skill name or a prefix"),
		Entry("an empty string", skilled(skillTools, entryList(`""`)), "must be a string that is a skill name or a prefix"),
		Entry("a scalar", skilled(skillTools, "skills: tdd\n"), "skills must be a list"),
		Entry("a null entry", skilled(skillTools, "skills: [tdd, null]\n"), "must be a string"),
		Entry("a number", skilled(skillTools, "skills: [1]\n"), "must be a string"),
		Entry("a mapping entry", skilled(skillTools, "skills: [{name: tdd}]\n"), "must be a string"))

	It("names the tool a profile needs to be offered a skill", func() {
		Expect(agent.SkillLoadTool).To(Equal("skill.load"))
	})
})

// requiring builds a profile named a whose frontmatter lines are given, in the order a profile writes them.
func requiring(name, header string) string {
	return "---\nname: " + name + "\ndescription: d\n" + header + "---\nDo it.\n"
}

// requiredList is the YAML of a requires_skills list.
func requiredList(names ...string) string {
	return "requires_skills: [" + strings.Join(names, ", ") + "]\n"
}

// sixtyFour is a skill name of the longest kind, 64 characters.
const sixtyFour = "sxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"

var _ = Describe("requires_skills field (ADR-013 D10)", func() {
	It("reads the listed names in order", func() {
		p, err := agent.Parse([]byte(requiring("a", skillTools+entryList("tdd", "code-review")+requiredList("code-review", "tdd"))), skillKnown)
		if err != nil {
			panic(err)
		}
		Expect(p.RequiresSkills).To(Equal([]string{"code-review", "tdd"}))
	})

	DescribeTable("requires nothing when the key is",
		func(tail string) {
			p, err := agent.Parse([]byte(requiring("a", skillTools+entryList("tdd")+tail)), skillKnown)
			if err != nil {
				panic(err)
			}
			Expect(p.RequiresSkills).To(BeNil())
		},
		Entry("absent", ""),
		Entry("null", "requires_skills:\n"),
		Entry("empty", "requires_skills: []\n"))

	It("accepts a name the profile is offered through a prefix", func() {
		p, err := agent.Parse([]byte(requiring("a", skillTools+entryList("code-*")+requiredList("code-review"))), skillKnown)
		if err != nil {
			panic(err)
		}
		Expect(p.RequiresSkills).To(Equal([]string{"code-review"}))
	})

	It("accepts the largest list: 16 names", func() {
		names := manyEntries(16)
		p, err := agent.Parse([]byte(requiring("a", skillTools+entryList("\"*\"")+requiredList(names...))), skillKnown)
		if err != nil {
			panic(err)
		}
		Expect(p.RequiresSkills).To(HaveLen(16))
	})

	DescribeTable("refuses",
		func(header, reason string) {
			_, err := agent.Parse([]byte(requiring("a", header)), skillKnown)
			Expect(err).To(MatchError(ContainSubstring(reason)))
		},
		Entry("a list over the cap: 17 names", skillTools+entryList("\"*\"")+requiredList(manyEntries(17)...),
			"requires_skills lists more than 16 entries"),
		Entry("a star pattern", skillTools+entryList("code-*")+requiredList("code-*"), "an exact skill name"),
		Entry("a name outside the offer set", skillTools+entryList("tdd")+requiredList("code-review"),
			`requires_skills lists "code-review", which the profile's skills do not offer`),
		Entry("a list without skill.load in tools", "tools: [fs.read]\n"+requiredList("tdd"), "requires_skills needs skill.load in tools"),
		Entry("a duplicate", skillTools+entryList("tdd")+requiredList("tdd", "tdd"), `lists "tdd" twice`),
		Entry("a name of 65 characters", skillTools+entryList("\"*\"")+requiredList(sixtyFour+"s"), "an exact skill name"),
		Entry("a null entry", skillTools+entryList("tdd")+requiredList("tdd", "null"), "must be a string"),
		Entry("a scalar", skillTools+entryList("tdd")+"requires_skills: tdd\n", "requires_skills must be a list"))
})
