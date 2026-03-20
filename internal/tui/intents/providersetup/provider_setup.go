package providersetup

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/baphled/flowstate/internal/auth"
	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/oauth"
	tuiintents "github.com/baphled/flowstate/internal/tui/intents"
	"github.com/baphled/flowstate/internal/tui/uikit/layout"
	"github.com/baphled/flowstate/internal/ui/terminal"
)

const (
	stepProviders  = 0
	stepMCPServers = 1
	maxSteps       = 2
)

// OAuthState represents the current state of an OAuth flow in the TUI.
type OAuthState struct {
	Active          bool
	UserCode        string
	DeviceCode      string
	VerificationURL string
	Polling         bool
	ErrorMessage    string
	Success         bool
}

// ProviderStatus holds the current state of a single provider.
type ProviderStatus struct {
	Name    string
	APIKey  string
	Model   string
	Enabled bool
}

// IntentConfig holds configuration for creating a new provider setup intent.
type IntentConfig struct {
	Shell      Shell
	Config     *config.AppConfig
	MCPServers []config.MCPServerConfig
}

// Shell defines the interface for writing configuration.
type Shell interface {
	// WriteConfig persists the provider configuration.
	WriteConfig(cfg *config.AppConfig) error
}

// Intent handles provider and MCP server configuration in the TUI.
type Intent struct {
	currentStep            int
	providers              []ProviderStatus
	mcpServers             []config.MCPServerConfig
	selectedItem           int
	editingAPIKey          bool
	apiKeyInput            string
	selectedProviderForKey string
	showInputModeSelector  bool
	credentialSource       string
	width                  int
	height                 int
	config                 *config.AppConfig
	shell                  Shell
	oauthState             OAuthState
}

// NewIntent creates a new provider setup intent from the given configuration.
//
// Expected:
//   - cfg is a valid IntentConfig with a non-nil Config and Shell.
//
// Returns:
//   - A fully initialised Intent ready for use in the TUI.
//
// Side effects:
//   - None.
func NewIntent(cfg IntentConfig) *Intent {
	providers := loadProviderStatuses(cfg.Config)
	return &Intent{
		currentStep:           stepProviders,
		providers:             providers,
		mcpServers:            cfg.MCPServers,
		selectedItem:          0,
		editingAPIKey:         false,
		apiKeyInput:           "",
		showInputModeSelector: false,
		credentialSource:      "",
		width:                 80,
		height:                24,
		config:                cfg.Config,
		shell:                 cfg.Shell,
	}
}

// loadProviderStatuses builds the initial provider status list from application configuration.
//
// Expected:
//   - cfg is a non-nil AppConfig with provider settings populated.
//
// Returns:
//   - A slice of ProviderStatus representing all known providers.
//
// Side effects:
//   - None.
func loadProviderStatuses(cfg *config.AppConfig) []ProviderStatus {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	return []ProviderStatus{
		{
			Name:    "ollama",
			APIKey:  "",
			Model:   cfg.Providers.Ollama.Model,
			Enabled: cfg.Providers.Ollama.Host != "",
		},
		{
			Name:    "openai",
			APIKey:  cfg.Providers.OpenAI.APIKey,
			Model:   cfg.Providers.OpenAI.Model,
			Enabled: cfg.Providers.OpenAI.APIKey != "",
		},
		{
			Name:    "anthropic",
			APIKey:  cfg.Providers.Anthropic.APIKey,
			Model:   cfg.Providers.Anthropic.Model,
			Enabled: cfg.Providers.Anthropic.APIKey != "",
		},
		{
			Name:    "github-copilot",
			APIKey:  cfg.Providers.GitHub.APIKey,
			Model:   "",
			Enabled: cfg.Providers.GitHub.APIKey != "",
		},
	}
}

// Init initialises the provider setup intent.
//
// Returns:
//   - nil.
//
// Side effects:
//   - None.
func (i *Intent) Init() tea.Cmd {
	return nil
}

// Update handles messages from the Bubble Tea event loop.
//
// Expected:
//   - msg is a valid Bubble Tea message.
//
// Returns:
//   - A command to be executed by the Bubble Tea runtime.
//
// Side effects:
//   - May update internal state based on the message type.
func (i *Intent) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return i.handleKeyMsg(msg)
	case tea.WindowSizeMsg:
		i.width = msg.Width
		i.height = msg.Height
	}
	return nil
}

// handleKeyMsg processes keyboard input for navigation and selection.
//
// Expected:
//   - msg is a valid Bubble Tea key message.
//
// Returns:
//   - A command to be executed by the Bubble Tea runtime, or nil.
//
// Side effects:
//   - May update navigation state, selected item, or current step.
func (i *Intent) handleKeyMsg(msg tea.KeyMsg) tea.Cmd {
	if i.showInputModeSelector {
		return i.handleInputModeSelection(msg)
	}
	if i.editingAPIKey {
		return i.handleAPIKeyInput(msg)
	}

	switch msg.Type {
	case tea.KeyTab:
		i.currentStep = (i.currentStep + 1) % maxSteps
		i.selectedItem = 0
		return nil
	case tea.KeyShiftTab:
		i.currentStep--
		if i.currentStep < 0 {
			i.currentStep = maxSteps - 1
		}
		i.selectedItem = 0
		return nil
	case tea.KeyUp:
		if i.selectedItem > 0 {
			i.selectedItem--
		}
		return nil
	case tea.KeyDown:
		i.selectedItem++
		return nil
	case tea.KeyEnter:
		return i.handleEnter()
	case tea.KeyEsc:
		return i.saveAndReturn()
	case tea.KeyRunes:
		return nil
	}
	return nil
}

// handleEnter processes the Enter key action for the current step.
//
// Returns:
//   - A command to be executed by the Bubble Tea runtime, or nil.
//
// Side effects:
//   - May toggle provider or MCP server state, enter API key editing mode, or show input mode selector.
func (i *Intent) handleEnter() tea.Cmd {
	switch i.currentStep {
	case stepProviders:
		if i.selectedItem < len(i.providers) {
			provider := &i.providers[i.selectedItem]
			if !provider.Enabled {
				i.selectedProviderForKey = provider.Name
				i.showInputModeSelector = true
				i.selectedItem = 0
			} else {
				provider.Enabled = !provider.Enabled
			}
		}
	case stepMCPServers:
		if i.selectedItem < len(i.mcpServers) {
			i.mcpServers[i.selectedItem].Enabled = !i.mcpServers[i.selectedItem].Enabled
		}
	}
	return nil
}

// handleAPIKeyInput processes keyboard input while editing an API key.
//
// Expected:
//   - msg is a valid Bubble Tea key message.
//
// Returns:
//   - A command to be executed by the Bubble Tea runtime, or nil.
//
// Side effects:
//   - May update the API key input buffer or exit editing mode.
func (i *Intent) handleAPIKeyInput(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEsc:
		i.saveAPIKey()
		i.editingAPIKey = false
		return nil
	case tea.KeyBackspace:
		if i.apiKeyInput != "" {
			i.apiKeyInput = i.apiKeyInput[:len(i.apiKeyInput)-1]
		}
		return nil
	case tea.KeyRunes:
		i.apiKeyInput += string(msg.Runes)
		return nil
	}
	return nil
}

// handleInputModeSelection processes keyboard input in the credential mode selector.
//
// Expected:
//   - msg is a valid Bubble Tea key message.
//
// Returns:
//   - A command to be executed by the Bubble Tea runtime, or nil.
//
// Side effects:
//   - May update the selected mode or enter credential input mode based on selection.
func (i *Intent) handleInputModeSelection(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEsc:
		i.showInputModeSelector = false
		i.selectedItem = 0
		return nil
	case tea.KeyUp:
		if i.selectedItem > 0 {
			i.selectedItem--
		}
		return nil
	case tea.KeyDown:
		if i.selectedItem < 1 {
			i.selectedItem++
		}
		return nil
	case tea.KeyEnter:
		i.showInputModeSelector = false
		if i.selectedItem == 0 {
			i.credentialSource = "OpenCode"
			return i.loadOpenCodeCredential()
		}
		i.credentialSource = "Manual"
		i.editingAPIKey = true
		i.apiKeyInput = ""
		i.selectedItem = 0
		return nil
	}
	return nil
}

// loadOpenCodeCredential loads credentials from OpenCode auth.json.
//
// Returns:
//   - A command to be executed by the Bubble Tea runtime.
//
// Side effects:
//   - Updates provider configuration if OpenCode credentials are found.
func (i *Intent) loadOpenCodeCredential() tea.Cmd {
	if i.config == nil {
		return nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = ""
	}
	opencodePath := filepath.Join(homeDir, ".local", "share", "opencode", "auth.json")

	ocAuth, err := auth.LoadOpenCodeAuthFrom(opencodePath)
	if err != nil || ocAuth == nil {
		return nil
	}

	for idx := range i.providers {
		if i.providers[idx].Name == i.selectedProviderForKey {
			i.providers[idx].Enabled = true
			switch i.selectedProviderForKey {
			case "anthropic":
				if ocAuth.Anthropic != nil && ocAuth.Anthropic.Access != "" {
					i.providers[idx].APIKey = ocAuth.Anthropic.Access
					i.config.Providers.Anthropic.APIKey = ocAuth.Anthropic.Access
				}
			case "github-copilot":
				if ocAuth.GitHubCopilot != nil && ocAuth.GitHubCopilot.Access != "" {
					i.providers[idx].APIKey = ocAuth.GitHubCopilot.Access
					i.config.Providers.GitHub.APIKey = ocAuth.GitHubCopilot.Access
				}
			}
			break
		}
	}

	i.editingAPIKey = false
	i.showInputModeSelector = false
	return nil
}

// isValidCredential checks if a credential matches the expected format for the given provider.
//
// Expected:
//   - providerName is a valid provider name (anthropic, github-copilot, openai).
//   - credential is the credential string to validate.
//
// Returns:
//   - true if the credential format is valid for the provider, false otherwise.
//
// Side effects:
//   - None.
func isValidCredential(providerName, credential string) bool {
	if credential == "" {
		return false
	}
	switch providerName {
	case "anthropic":
		return len(credential) >= 13 && (credential[:13] == "sk-ant-api03-" || credential[:13] == "sk-ant-oat01-")
	case "github-copilot":
		return len(credential) >= 4 && credential[:4] == "gho_"
	case "openai":
		return len(credential) >= 3 && credential[:3] == "sk-"
	default:
		return false
	}
}

// saveAPIKey saves the entered API key to the selected provider.
//
// Side effects:
//   - Updates the matching provider status and application configuration with the entered API key if valid.
func (i *Intent) saveAPIKey() {
	if !isValidCredential(i.selectedProviderForKey, i.apiKeyInput) {
		i.apiKeyInput = ""
		return
	}
	for idx := range i.providers {
		if i.providers[idx].Name == i.selectedProviderForKey {
			i.providers[idx].APIKey = i.apiKeyInput
			i.providers[idx].Enabled = i.apiKeyInput != ""
			switch i.providers[idx].Name {
			case "openai":
				i.config.Providers.OpenAI.APIKey = i.apiKeyInput
			case "anthropic":
				i.config.Providers.Anthropic.APIKey = i.apiKeyInput
			case "github-copilot":
				i.config.Providers.GitHub.APIKey = i.apiKeyInput
			}
			break
		}
	}
}

// saveAndReturn saves all configuration changes and dismisses the modal.
//
// Returns:
//   - A tea.Cmd that emits a DismissModalMsg to close the modal.
//
// Side effects:
//   - Persists provider API keys and MCP server settings to the application configuration.
//   - Writes configuration to disk via the shell interface.
func (i *Intent) saveAndReturn() tea.Cmd {
	for _, p := range i.providers {
		switch p.Name {
		case "openai":
			i.config.Providers.OpenAI.APIKey = p.APIKey
		case "anthropic":
			i.config.Providers.Anthropic.APIKey = p.APIKey
		case "github-copilot":
			i.config.Providers.GitHub.APIKey = p.APIKey
		}
	}
	i.config.MCPServers = i.mcpServers

	if i.shell != nil {
		if err := i.shell.WriteConfig(i.config); err != nil {
			slog.Error("saving config", "error", err)
		}
	}
	return func() tea.Msg { return tuiintents.DismissModalMsg{} }
}

// View renders the provider setup interface.
//
// Returns:
//   - A string containing the rendered TUI output.
//
// Side effects:
//   - None.
func (i *Intent) View() string {
	termInfo := &terminal.Info{Width: i.width, Height: i.height}

	var content string
	var helpText string

	if i.showInputModeSelector {
		content, helpText = i.renderInputModeSelector()
	} else if i.editingAPIKey {
		content = i.renderAPIKeyInput()
		helpText = "Paste credential  ·  Esc save and return"
	} else {
		switch i.currentStep {
		case stepProviders:
			content, helpText = i.renderProvidersStep()
		case stepMCPServers:
			content, helpText = i.renderMCPServersStep()
		}
	}

	helpStyled := lipgloss.NewStyle().
		Foreground(lipgloss.Color("245")).
		Render(helpText)

	sl := layout.NewScreenLayout(termInfo).
		WithBreadcrumbs("Chat", "Provider Setup").
		WithTitle("Provider Setup", getStepSubtitle(i.currentStep)).
		WithContent(content).
		WithHelp(helpStyled).
		WithFooterSeparator(true)

	return sl.Render()
}

// getStepSubtitle returns the subtitle for the given step.
//
// Expected:
//   - step is a valid step constant (stepProviders or stepMCPServers).
//
// Returns:
//   - A human-readable subtitle string for the step.
//
// Side effects:
//   - None.
func getStepSubtitle(step int) string {
	switch step {
	case stepProviders:
		return "Configure AI providers"
	case stepMCPServers:
		return "Enable MCP servers"
	}
	return ""
}

// renderProvidersStep renders the provider selection step.
//
// Returns:
//   - The rendered content string and a help text string.
//
// Side effects:
//   - None.
func (i *Intent) renderProvidersStep() (string, string) {
	if i.editingAPIKey {
		return i.renderAPIKeyInput(), "Type API key  ·  Esc save and return"
	}

	helpText := "↑↓ navigate  ·  Enter select/toggle  ·  Tab next step  ·  Esc save and exit"

	var lines []string
	selectedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("205")).
		Bold(true)

	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color("255"))

	for idx, p := range i.providers {
		status := "[*] "
		if !p.Enabled {
			status = "[ ] "
		}
		var line string
		if idx == i.selectedItem {
			line = selectedStyle.Render(fmt.Sprintf("%s%s", status, p.Name))
		} else {
			line = style.Render(fmt.Sprintf("%s%s", status, p.Name))
		}
		lines = append(lines, line)
	}

	return lipgloss.JoinVertical(lipgloss.Left, lines...), helpText
}

// renderAPIKeyInput renders the API key input field.
//
// Returns:
//   - A string containing the rendered API key input form.
//
// Side effects:
//   - None.
func (i *Intent) renderAPIKeyInput() string {
	inputStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("205"))

	return lipgloss.JoinVertical(lipgloss.Left,
		styleFor("Provider: "+i.selectedProviderForKey),
		"",
		inputStyle.Render("API Key: "+i.apiKeyInput+"_"),
	)
}

// renderInputModeSelector renders the credential input mode selection menu.
//
// Returns:
//   - The rendered content string and a help text string.
//
// Side effects:
//   - None.
func (i *Intent) renderInputModeSelector() (string, string) {
	helpText := "↑↓ navigate  ·  Enter select  ·  Esc cancel"

	modes := []string{"Use OpenCode credentials", "Enter manually"}
	var lines []string
	selectedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("205")).
		Bold(true)

	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color("255"))

	for idx, mode := range modes {
		var line string
		if idx == i.selectedItem {
			line = selectedStyle.Render("> " + mode)
		} else {
			line = style.Render("  " + mode)
		}
		lines = append(lines, line)
	}

	header := styleFor("Select credential source for " + i.selectedProviderForKey + ":")

	return lipgloss.JoinVertical(lipgloss.Left, append([]string{header, ""}, lines...)...), helpText
}

// renderMCPServersStep renders the MCP servers selection step.
//
// Returns:
//   - The rendered content string and a help text string.
//
// Side effects:
//   - None.
func (i *Intent) renderMCPServersStep() (string, string) {
	helpText := "↑↓ navigate  ·  Enter toggle  ·  Tab prev step  ·  Esc save and exit"

	var lines []string
	selectedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("205")).
		Bold(true)

	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color("255"))

	for idx, srv := range i.mcpServers {
		status := "[ ] "
		if srv.Enabled {
			status = "[*] "
		}
		var line string
		if idx == i.selectedItem {
			line = selectedStyle.Render(fmt.Sprintf("%s%s", status, srv.Name))
		} else {
			line = style.Render(fmt.Sprintf("%s%s", status, srv.Name))
		}
		lines = append(lines, line)
	}

	return lipgloss.JoinVertical(lipgloss.Left, lines...), helpText
}

// styleFor applies standard styling to text.
//
// Expected:
//   - text is the string to style.
//
// Returns:
//   - The styled text string.
//
// Side effects:
//   - None.
func styleFor(text string) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("255")).
		Render(text)
}

// Result returns the intent result.
//
// Returns:
//   - nil, as the provider setup intent does not produce a result.
//
// Side effects:
//   - None.
func (i *Intent) Result() *tuiintents.IntentResult {
	return nil
}

// CurrentStep returns the current configuration step.
//
// Returns:
//   - The current step index.
//
// Side effects:
//   - None.
func (i *Intent) CurrentStep() int {
	return i.currentStep
}

// Providers returns the list of provider statuses.
//
// Returns:
//   - A slice of ProviderStatus representing all configured providers.
//
// Side effects:
//   - None.
func (i *Intent) Providers() []ProviderStatus {
	return i.providers
}

// MCPServers returns the list of MCP server configurations.
//
// Returns:
//   - A slice of MCPServerConfig for all known MCP servers.
//
// Side effects:
//   - None.
func (i *Intent) MCPServers() []config.MCPServerConfig {
	return i.mcpServers
}

// SelectedProvider returns the index of the selected provider.
//
// Returns:
//   - The zero-based index of the currently selected provider.
//
// Side effects:
//   - None.
func (i *Intent) SelectedProvider() int {
	return i.selectedItem
}

// IsEditingAPIKey returns whether the intent is currently editing an API key.
//
// Returns:
//   - true if the user is entering an API key, false otherwise.
//
// Side effects:
//   - None.
func (i *Intent) IsEditingAPIKey() bool {
	return i.editingAPIKey
}

// APIKeyInput returns the current API key input value.
//
// Returns:
//   - The current contents of the API key input buffer.
//
// Side effects:
//   - None.
func (i *Intent) APIKeyInput() string {
	return i.apiKeyInput
}

// Width returns the terminal width.
//
// Returns:
//   - The current terminal width in columns.
//
// Side effects:
//   - None.
func (i *Intent) Width() int {
	return i.width
}

// Height returns the terminal height.
//
// Returns:
//   - The current terminal height in rows.
//
// Side effects:
//   - None.
func (i *Intent) Height() int {
	return i.height
}

// OAuthState returns the current OAuth state.
//
// Returns:
//   - The current OAuthState struct.
//
// Side effects:
//   - None.
func (i *Intent) OAuthState() OAuthState {
	return i.oauthState
}

// StartOAuth initiates the OAuth flow for the selected provider.
//
// Expected:
//   - clientID is a valid OAuth client ID.
//
// Returns:
//   - An error if initiation fails, nil otherwise.
//
// Side effects:
//   - Updates the OAuth state with device code and user code.
func (i *Intent) StartOAuth(_ string, clientID string) error {
	ctx := context.Background()
	github := oauth.NewGitHub(clientID)

	deviceResp, err := github.InitiateFlow(ctx)
	if err != nil {
		i.oauthState = OAuthState{
			Active:       true,
			ErrorMessage: err.Error(),
		}
		return err
	}

	i.oauthState = OAuthState{
		Active:          true,
		UserCode:        deviceResp.UserCode,
		DeviceCode:      deviceResp.DeviceCode,
		VerificationURL: deviceResp.VerificationURI,
		Polling:         false,
	}

	return nil
}

// PollOAuth polls for OAuth authorization status.
//
// Expected:
//   - clientID is a valid OAuth client ID.
//
// Returns:
//   - An error if polling fails, nil otherwise.
//
// Side effects:
//   - Updates the OAuth state based on authorization result.
func (i *Intent) PollOAuth(_ string, clientID string) error {
	if !i.oauthState.Active || i.oauthState.DeviceCode == "" {
		return nil
	}

	ctx := context.Background()
	github := oauth.NewGitHub(clientID)

	result, err := github.PollToken(ctx, i.oauthState.DeviceCode, 5)
	if err != nil {
		i.oauthState.ErrorMessage = err.Error()
		return err
	}

	switch result.State {
	case oauth.StateApproved:
		i.oauthState.Success = true
		i.oauthState.Polling = false
	case oauth.StateExpired:
		i.oauthState.ErrorMessage = result.ErrorMessage
		i.oauthState.Active = false
	case oauth.StateError:
		i.oauthState.ErrorMessage = result.ErrorMessage
	}

	return nil
}

// CancelOAuth cancels the OAuth flow.
//
// Side effects:
//   - Resets the OAuth state.
func (i *Intent) CancelOAuth() {
	i.oauthState = OAuthState{}
}

// IsOAuthActive returns whether OAuth flow is active.
//
// Returns:
//   - true if OAuth is active and not yet successful, false otherwise.
//
// Side effects:
//   - None.
func (i *Intent) IsOAuthActive() bool {
	return i.oauthState.Active && !i.oauthState.Success
}
