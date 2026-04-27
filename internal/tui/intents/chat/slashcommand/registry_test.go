package slashcommand_test

import (
	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tui/intents/chat/slashcommand"
	"github.com/baphled/flowstate/internal/tui/uikit/widgets"
)

var _ = Describe("Registry", func() {
	var reg *slashcommand.Registry

	BeforeEach(func() {
		reg = slashcommand.NewRegistry()
	})

	Describe("Register and All", func() {
		It("returns registered commands in registration order", func() {
			reg.Register(newStubCommand("alpha", "First"))
			reg.Register(newStubCommand("beta", "Second"))

			all := reg.All()
			Expect(all).To(HaveLen(2))
			Expect(all[0].Name).To(Equal("alpha"))
			Expect(all[1].Name).To(Equal("beta"))
		})

		It("replaces an existing command when re-registering by name", func() {
			reg.Register(newStubCommand("alpha", "First"))
			reg.Register(newStubCommand("alpha", "Replacement"))

			all := reg.All()
			Expect(all).To(HaveLen(1))
			Expect(all[0].Description).To(Equal("Replacement"))
		})
	})

	Describe("Filter", func() {
		BeforeEach(func() {
			reg.Register(newStubCommand("clear", "Clear"))
			reg.Register(newStubCommand("clone", "Clone"))
			reg.Register(newStubCommand("help", "Help"))
		})

		It("returns every command when prefix is empty", func() {
			Expect(reg.Filter("")).To(HaveLen(3))
		})

		It("returns commands with matching prefix", func() {
			matches := reg.Filter("cl")
			Expect(matches).To(HaveLen(2))
		})

		It("matches case-insensitively", func() {
			Expect(reg.Filter("HE")).To(HaveLen(1))
		})

		It("returns nothing when nothing matches", func() {
			Expect(reg.Filter("zzz")).To(BeEmpty())
		})
	})

	Describe("Lookup", func() {
		BeforeEach(func() {
			reg.Register(newStubCommand("clear", "Clear"))
		})

		It("returns the matched command", func() {
			cmd := reg.Lookup("clear")
			Expect(cmd).NotTo(BeNil())
			Expect(cmd.Name).To(Equal("clear"))
		})

		It("returns nil for an unknown name", func() {
			Expect(reg.Lookup("nope")).To(BeNil())
		})
	})

	Describe("FilterItems", func() {
		It("returns picker items for matched commands", func() {
			reg.Register(newStubCommand("clear", "Wipe"))
			items := reg.FilterItems("cl")
			Expect(items).To(HaveLen(1))
			Expect(items[0].Label).To(Equal("/clear"))
			Expect(items[0].Description).To(Equal("Wipe"))
		})
	})
})

func newStubCommand(name, desc string) slashcommand.Command {
	return slashcommand.Command{
		Name:        name,
		Description: desc,
		Handler: func(_ slashcommand.CommandContext, _ *widgets.Item) tea.Cmd {
			return nil
		},
	}
}
