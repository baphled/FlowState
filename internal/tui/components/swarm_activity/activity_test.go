package swarmactivity_test

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	swarmactivity "github.com/baphled/flowstate/internal/tui/components/swarm_activity"
)

func TestSwarmActivity(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "SwarmActivity Suite")
}

var _ = Describe("SwarmActivityPane", func() {
	Describe("NewSwarmActivityPane", func() {
		It("returns a non-nil pane", func() {
			Expect(swarmactivity.NewSwarmActivityPane()).NotTo(BeNil())
		})
	})

	Describe("Render", func() {
		var pane *swarmactivity.SwarmActivityPane

		BeforeEach(func() {
			pane = swarmactivity.NewSwarmActivityPane()
		})

		Context("at a comfortable width and height", func() {
			It("renders the header as the first line", func() {
				output := pane.Render(80, 10)
				Expect(output).NotTo(BeEmpty())
				firstLine := strings.Split(output, "\n")[0]
				Expect(firstLine).To(ContainSubstring("Activity Timeline"))
			})

			It("renders between three and five placeholder timeline items", func() {
				output := pane.Render(80, 10)
				lines := strings.Split(output, "\n")
				// Skip the header line, count body lines with the bullet marker.
				var items int
				for _, line := range lines[1:] {
					if strings.Contains(line, "▸") {
						items++
					}
				}
				Expect(items).To(BeNumerically(">=", 3))
				Expect(items).To(BeNumerically("<=", 5))
			})
		})

		Context("with limited width", func() {
			It("truncates long body lines with an ellipsis suffix", func() {
				output := pane.Render(20, 10)
				lines := strings.Split(output, "\n")
				// At least one body line must have been truncated.
				var truncated bool
				for _, line := range lines[1:] {
					if strings.HasSuffix(line, "…") {
						truncated = true
					}
					// No body line exceeds the declared width when measured visually.
					Expect(lipgloss.Width(line)).To(BeNumerically("<=", 20))
				}
				Expect(truncated).To(BeTrue(), "expected at least one line to be truncated with an ellipsis")
			})
		})

		Context("with limited height", func() {
			It("clamps total rendered lines to the declared height", func() {
				output := pane.Render(80, 2)
				lines := strings.Split(output, "\n")
				Expect(len(lines)).To(BeNumerically("<=", 2))
				Expect(lines[0]).To(ContainSubstring("Activity Timeline"))
			})
		})

		Context("below the minimum usable thresholds", func() {
			It("returns an empty string when width is below the minimum", func() {
				Expect(pane.Render(9, 10)).To(BeEmpty())
			})

			It("returns an empty string when height is below the minimum", func() {
				Expect(pane.Render(80, 1)).To(BeEmpty())
			})
		})
	})
})
