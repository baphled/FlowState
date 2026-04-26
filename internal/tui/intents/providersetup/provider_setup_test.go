package providersetup_test

import (
	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/tui/intents/providersetup"
)

var _ = Describe("ProviderSetupIntent", func() {
	var (
		mockApp    *MockAppShell
		cfg        *config.AppConfig
		mcpServers []config.MCPServerConfig
		intent     *providersetup.Intent
	)

	BeforeEach(func() {
		mockApp = NewMockAppShell()
		cfg = config.DefaultConfig()
		mcpServers = []config.MCPServerConfig{
			{Name: "memory", Command: "mcp-mem0-server", Enabled: false},
			{Name: "vault-rag", Command: "mcp-vault-server", Enabled: false},
			{Name: "filesystem", Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"}, Enabled: false},
		}
		intent = providersetup.NewIntent(providersetup.IntentConfig{
			Shell:      mockApp,
			Config:     cfg,
			MCPServers: mcpServers,
		})
	})

	Describe("NewIntent", func() {
		It("creates an intent on the providers step", func() {
			Expect(intent).NotTo(BeNil())
			Expect(intent.CurrentStep()).To(Equal(0))
		})

		It("loads providers from config", func() {
			providers := intent.Providers()
			Expect(providers).NotTo(BeEmpty())
		})

		It("loads MCP servers", func() {
			servers := intent.MCPServers()
			Expect(servers).To(HaveLen(3))
		})
	})

	Describe("View", func() {
		It("shows provider setup title", func() {
			view := intent.View()
			Expect(view).To(ContainSubstring("Provider Setup"))
		})

		It("shows step subtitle", func() {
			view := intent.View()
			Expect(view).To(ContainSubstring("Configure AI providers"))
		})

		It("shows configured providers with indicator", func() {
			view := intent.View()
			Expect(view).To(ContainSubstring("ollama"))
		})

		It("shows unconfigured providers with indicator", func() {
			view := intent.View()
			Expect(view).To(ContainSubstring("openai"))
		})
	})

	Describe("step navigation", func() {
		Context("Tab key advances steps", func() {
			It("moves from providers to MCP servers", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyTab})
				Expect(intent.CurrentStep()).To(Equal(1))
			})

			It("wraps from MCP back to providers", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyTab})
				intent.Update(tea.KeyMsg{Type: tea.KeyTab})
				Expect(intent.CurrentStep()).To(Equal(0))
			})
		})

		Context("Shift+Tab moves backwards", func() {
			It("moves from MCP back to providers", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyTab})
				intent.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
				Expect(intent.CurrentStep()).To(Equal(0))
			})
		})
	})

	Describe("provider selection", func() {
		Context("on providers step", func() {
			It("highlights the selected provider", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				providers := intent.Providers()
				Expect(intent.SelectedProvider()).To(Equal(1))
				Expect(providers[intent.SelectedProvider()].Name).To(Equal("openai"))
			})

			It("entering unconfigured provider opens manual API-key entry", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				intent.Update(tea.KeyMsg{Type: tea.KeyEnter})
				Expect(intent.IsEditingAPIKey()).To(BeTrue())
			})

			It("entering configured provider toggles enabled state", func() {
				initial := intent.Providers()[0].Enabled
				intent.Update(tea.KeyMsg{Type: tea.KeyEnter})
				Expect(intent.Providers()[0].Enabled).To(Equal(!initial))
			})
		})
	})

	Describe("API key input", func() {
		BeforeEach(func() {
			intent.Update(tea.KeyMsg{Type: tea.KeyDown})
			intent.Update(tea.KeyMsg{Type: tea.KeyEnter})
		})

		It("accepts typed characters", func() {
			intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s', 'k', '-'}})
			Expect(intent.APIKeyInput()).To(Equal("sk-"))
		})

		It("handles backspace", func() {
			intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s', 'k'}})
			intent.Update(tea.KeyMsg{Type: tea.KeyBackspace})
			Expect(intent.APIKeyInput()).To(Equal("s"))
		})

		It("Escape saves and returns to provider list", func() {
			intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s', 'k', '-', 'a', 'n', 't', '-', 'a', 'p', 'i', '0', '3', '-', 't', 'e', 's', 't'}})
			intent.Update(tea.KeyMsg{Type: tea.KeyEsc})
			Expect(intent.IsEditingAPIKey()).To(BeFalse())
			Expect(intent.CurrentStep()).To(Equal(0))
		})
	})

	Describe("MCP server step", func() {
		BeforeEach(func() {
			intent.Update(tea.KeyMsg{Type: tea.KeyTab})
		})

		It("shows MCP servers", func() {
			view := intent.View()
			Expect(view).To(ContainSubstring("memory"))
			Expect(view).To(ContainSubstring("vault-rag"))
			Expect(view).To(ContainSubstring("filesystem"))
		})

		It("toggles MCP server on Enter", func() {
			initial := intent.MCPServers()[0].Enabled
			intent.Update(tea.KeyMsg{Type: tea.KeyEnter})
			Expect(intent.MCPServers()[0].Enabled).To(Equal(!initial))
		})
	})

	Describe("credential validation", func() {
		It("accepts valid Anthropic API key format", func() {
			intent.Update(tea.KeyMsg{Type: tea.KeyDown})
			intent.Update(tea.KeyMsg{Type: tea.KeyEnter})

			credential := "sk-ant-api03-test123"
			for _, r := range credential {
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
			}
			intent.Update(tea.KeyMsg{Type: tea.KeyEsc})

			providers := intent.Providers()
			Expect(providers[1].APIKey).To(Equal(credential))
		})

		It("accepts valid Anthropic OAuth token format", func() {
			intent.Update(tea.KeyMsg{Type: tea.KeyDown})
			intent.Update(tea.KeyMsg{Type: tea.KeyEnter})

			credential := "sk-ant-oat01-oauth123"
			for _, r := range credential {
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
			}
			intent.Update(tea.KeyMsg{Type: tea.KeyEsc})

			providers := intent.Providers()
			Expect(providers[1].APIKey).To(Equal(credential))
		})

		It("rejects invalid credential format", func() {
			intent.Update(tea.KeyMsg{Type: tea.KeyDown})
			intent.Update(tea.KeyMsg{Type: tea.KeyEnter})

			credential := "invalid-key"
			for _, r := range credential {
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
			}
			intent.Update(tea.KeyMsg{Type: tea.KeyEsc})

			providers := intent.Providers()
			Expect(providers[1].APIKey).To(BeEmpty())
		})

		It("accepts valid GitHub key format", func() {
			intent.Update(tea.KeyMsg{Type: tea.KeyDown})
			intent.Update(tea.KeyMsg{Type: tea.KeyDown})
			intent.Update(tea.KeyMsg{Type: tea.KeyDown})
			intent.Update(tea.KeyMsg{Type: tea.KeyEnter})

			credential := "gho_test123456789"
			for _, r := range credential {
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
			}
			intent.Update(tea.KeyMsg{Type: tea.KeyEsc})

			providers := intent.Providers()
			Expect(providers[3].APIKey).To(Equal(credential))
		})
	})

	Describe("Escape from providers step", func() {
		It("triggers save and return", func() {
			intent.Update(tea.KeyMsg{Type: tea.KeyEsc})
			Expect(mockApp.WriteConfigCalled).To(BeTrue())
			Expect(mockApp.SavedConfig).NotTo(BeNil())
		})
	})

	Describe("WindowSizeMsg", func() {
		It("updates dimensions", func() {
			intent.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
			Expect(intent.Width()).To(Equal(120))
			Expect(intent.Height()).To(Equal(40))
		})
	})

	Describe("Intent interface compliance", func() {
		It("satisfies app.Intent interface", func() {
			var _ interface {
				Init() tea.Cmd
				Update(tea.Msg) tea.Cmd
				View() string
			} = intent
		})
	})
})

type MockAppShell struct {
	WriteConfigCalled bool
	SavedConfig       *config.AppConfig
}

func NewMockAppShell() *MockAppShell {
	return &MockAppShell{}
}

func (m *MockAppShell) WriteConfig(cfg *config.AppConfig) error {
	m.WriteConfigCalled = true
	m.SavedConfig = cfg
	return nil
}

func (m *MockAppShell) SetIntent(intent interface{}) {}

func (m *MockAppShell) Width() int  { return 80 }
func (m *MockAppShell) Height() int { return 24 }
