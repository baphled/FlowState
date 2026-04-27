package slashcommand

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/plan"
	"github.com/baphled/flowstate/internal/tui/uikit/widgets"
)

// RegisterBuiltins wires the canonical slash-command set into reg. Each
// builtin is independently exported (newClearCommand etc.) so external
// embedders can pick a subset.
//
// Expected:
//   - reg is a non-nil Registry.
//
// Side effects:
//   - Mutates reg.
func RegisterBuiltins(reg *Registry) {
	reg.Register(newClearCommand())
	reg.Register(newHelpCommand())
	reg.Register(newExitCommand("exit"))
	reg.Register(newExitCommand("quit"))
	reg.Register(newSessionsCommand())
	reg.Register(newPlansCommand())
	reg.Register(newAgentCommand())
	reg.Register(newModelCommand())
}

// newClearCommand builds the /clear command which wipes the chat
// message buffer via the registered MessageWiper.
//
// Returns:
//   - The /clear Command.
//
// Side effects:
//   - None (pure constructor).
func newClearCommand() Command {
	return Command{
		Name:        "clear",
		Description: "Wipe the chat buffer",
		Handler: func(ctx CommandContext, _ *widgets.Item) tea.Cmd {
			if ctx.MessageWiper != nil {
				ctx.MessageWiper.ClearMessages()
			}
			return nil
		},
	}
}

// newHelpCommand builds the /help command which opens a sub-picker over
// every registered command. Selecting one dumps its help text into the
// chat as a system message.
//
// Returns:
//   - The /help Command.
//
// Side effects:
//   - None (pure constructor).
func newHelpCommand() Command {
	return Command{
		Name:        "help",
		Description: "List available slash commands",
		ItemsForPicker: func(ctx CommandContext) []widgets.Item {
			if ctx.Registry == nil {
				return nil
			}
			return ctx.Registry.Items()
		},
		Handler: func(ctx CommandContext, arg *widgets.Item) tea.Cmd {
			if ctx.SystemMessageWriter == nil || arg == nil {
				return nil
			}
			cmd, ok := arg.Value.(Command)
			if !ok {
				return nil
			}
			ctx.SystemMessageWriter.AddSystemMessage(formatHelpEntry(cmd))
			return nil
		},
	}
}

// formatHelpEntry composes the system-message dump for a single command.
//
// Expected:
//   - cmd is any registered Command.
//
// Returns:
//   - A multi-line help blurb suitable for chat display.
//
// Side effects:
//   - None.
func formatHelpEntry(cmd Command) string {
	var b strings.Builder
	fmt.Fprintf(&b, "/%s — %s", cmd.Name, cmd.Description)
	if cmd.ItemsForPicker != nil {
		b.WriteString("\nSelecting this command opens a sub-picker for the argument.")
	}
	return b.String()
}

// newExitCommand builds /exit and /quit. tea.Quit exits the program
// cleanly; the chat intent will still run its save-on-exit chain by
// virtue of the way Bubble Tea processes the returned cmd.
//
// Expected:
//   - name is either "exit" or "quit".
//
// Returns:
//   - A Command that returns tea.Quit on selection.
//
// Side effects:
//   - None (pure constructor).
func newExitCommand(name string) Command {
	desc := "Exit FlowState"
	if name == "quit" {
		desc = "Alias for /exit"
	}
	return Command{
		Name:        name,
		Description: desc,
		Handler: func(_ CommandContext, _ *widgets.Item) tea.Cmd {
			return tea.Quit
		},
	}
}

// newSessionsCommand builds /sessions which opens a sub-picker over the
// sessionStore's list and resumes the chosen session.
//
// Returns:
//   - The /sessions Command.
//
// Side effects:
//   - None (pure constructor).
func newSessionsCommand() Command {
	return Command{
		Name:        "sessions",
		Description: "Resume a saved session",
		ItemsForPicker: func(ctx CommandContext) []widgets.Item {
			if ctx.SessionLister == nil {
				return nil
			}
			sessions := ctx.SessionLister.List()
			out := make([]widgets.Item, len(sessions))
			for idx := range sessions {
				out[idx] = widgets.Item{
					Label:       sessionLabel(sessions[idx].ID, sessions[idx].Title, sessions[idx].LastActive),
					Description: sessionDescription(sessions[idx].MessageCount, sessions[idx].LastActive),
					Value:       sessions[idx].ID,
				}
			}
			return out
		},
		Handler: func(ctx CommandContext, arg *widgets.Item) tea.Cmd {
			if ctx.SessionResumer == nil || arg == nil {
				return nil
			}
			id, ok := arg.Value.(string)
			if !ok {
				return nil
			}
			ctx.SessionResumer.ResumeSession(id)
			return nil
		},
	}
}

// sessionLabel composes the popover label for a session list entry.
//
// Expected:
//   - id, title, and lastActive are SessionInfo fields; title may be
//     empty.
//
// Returns:
//   - The label string with a sensible fallback when title is empty.
//
// Side effects:
//   - None.
func sessionLabel(id, title string, lastActive time.Time) string {
	if title != "" {
		return title
	}
	if !lastActive.IsZero() {
		return "Session — " + lastActive.Format("2 Jan 2006 15:04")
	}
	if len(id) >= 8 {
		return "Session " + id[:8]
	}
	return "Session " + id
}

// sessionDescription composes the secondary popover text for a session.
//
// Expected:
//   - msgCount and lastActive come from SessionInfo.
//
// Returns:
//   - A "(N messages)" or relative-time hint.
//
// Side effects:
//   - None.
func sessionDescription(msgCount int, lastActive time.Time) string {
	if msgCount > 0 {
		return fmt.Sprintf("%d messages", msgCount)
	}
	if !lastActive.IsZero() {
		return lastActive.Format("2006-01-02 15:04")
	}
	return ""
}

// newPlansCommand builds /plans which opens a sub-picker over plan
// summaries and dumps the selected plan into the chat as a read-only
// system message.
//
// Returns:
//   - The /plans Command.
//
// Side effects:
//   - None (pure constructor).
func newPlansCommand() Command {
	return Command{
		Name:        "plans",
		Description: "Open a saved plan in the chat",
		ItemsForPicker: func(ctx CommandContext) []widgets.Item {
			if ctx.PlanLister == nil {
				return nil
			}
			summaries, err := ctx.PlanLister.List()
			if err != nil {
				return nil
			}
			out := make([]widgets.Item, len(summaries))
			for idx, s := range summaries {
				out[idx] = widgets.Item{
					Label:       planLabel(s),
					Description: s.Status,
					Value:       s.ID,
				}
			}
			return out
		},
		Handler: func(ctx CommandContext, arg *widgets.Item) tea.Cmd {
			if ctx.SystemMessageWriter == nil || arg == nil {
				return nil
			}
			id, ok := arg.Value.(string)
			if !ok || ctx.PlanFetcher == nil {
				return nil
			}
			file, err := ctx.PlanFetcher.Get(id)
			if err != nil {
				ctx.SystemMessageWriter.AddSystemMessage("Failed to load plan: " + err.Error())
				return nil
			}
			ctx.SystemMessageWriter.AddSystemMessage(formatPlan(file))
			return nil
		},
	}
}

// planLabel composes the popover label for a plan.
//
// Expected:
//   - s is a Summary from plan.Store.List.
//
// Returns:
//   - The plan's Title with an ID fallback.
//
// Side effects:
//   - None.
func planLabel(s plan.Summary) string {
	if s.Title != "" {
		return s.Title
	}
	return s.ID
}

// formatPlan composes the read-only chat dump for a plan.
//
// Expected:
//   - file is a non-nil plan.File.
//
// Returns:
//   - A multi-line string with title, status, and TL;DR.
//
// Side effects:
//   - None.
func formatPlan(file *plan.File) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Plan: %s\n", file.Title)
	if file.Status != "" {
		fmt.Fprintf(&b, "Status: %s\n", file.Status)
	}
	if file.Description != "" {
		fmt.Fprintf(&b, "\n%s\n", file.Description)
	}
	if file.TLDR != "" {
		fmt.Fprintf(&b, "\nTL;DR:\n%s\n", file.TLDR)
	}
	return strings.TrimRight(b.String(), "\n")
}

// newAgentCommand builds /agent which opens a sub-picker over the agent
// registry and applies the selected manifest to the running engine.
//
// Returns:
//   - The /agent Command.
//
// Side effects:
//   - None (pure constructor).
func newAgentCommand() Command {
	return Command{
		Name:        "agent",
		Description: "Switch the active agent",
		ItemsForPicker: func(ctx CommandContext) []widgets.Item {
			if ctx.AgentRegistry == nil {
				return nil
			}
			agents := ctx.AgentRegistry.List()
			out := make([]widgets.Item, len(agents))
			for idx, a := range agents {
				out[idx] = widgets.Item{
					Label:       a.Name,
					Description: a.ID,
					Value:       a.ID,
				}
			}
			return out
		},
		Handler: func(ctx CommandContext, arg *widgets.Item) tea.Cmd {
			if ctx.AgentSwitcher == nil || ctx.AgentRegistry == nil || arg == nil {
				return nil
			}
			id, ok := arg.Value.(string)
			if !ok {
				return nil
			}
			manifest, found := ctx.AgentRegistry.Get(id)
			if !found {
				return nil
			}
			ctx.AgentSwitcher.SetManifest(*manifest)
			return nil
		},
	}
}

// newModelCommand builds /model which opens a sub-picker over every
// configured provider's model list and applies the selection.
//
// Returns:
//   - The /model Command.
//
// Side effects:
//   - None (pure constructor).
func newModelCommand() Command {
	return Command{
		Name:        "model",
		Description: "Switch the chat model",
		ItemsForPicker: func(ctx CommandContext) []widgets.Item {
			if ctx.ProviderLister == nil {
				return nil
			}
			return collectProviderModels(ctx)
		},
		Handler: func(ctx CommandContext, arg *widgets.Item) tea.Cmd {
			if ctx.ModelSwitcher == nil || arg == nil {
				return nil
			}
			pref, ok := arg.Value.(modelChoice)
			if !ok {
				return nil
			}
			ctx.ModelSwitcher.SetModelPreference(pref.Provider, pref.Model)
			return nil
		},
	}
}

// modelChoice is the opaque payload stored on /model picker items.
type modelChoice struct {
	Provider string
	Model    string
}

// collectProviderModels gathers every model from every provider into a
// single picker slice. Providers that error on Models() are silently
// skipped — the picker should still show whatever subset is reachable.
//
// Expected:
//   - ctx.ProviderLister is non-nil.
//
// Returns:
//   - A slice of widgets.Item with modelChoice payloads.
//
// Side effects:
//   - None beyond calling provider.Models which may issue network calls.
func collectProviderModels(ctx CommandContext) []widgets.Item {
	names := ctx.ProviderLister.List()
	var out []widgets.Item
	for _, name := range names {
		prov, err := ctx.ProviderLister.Get(name)
		if err != nil {
			continue
		}
		models, err := prov.Models()
		if err != nil {
			continue
		}
		for _, m := range models {
			out = append(out, widgets.Item{
				Label:       name + "/" + m.ID,
				Description: modelDescription(m.ContextLength),
				Value:       modelChoice{Provider: name, Model: m.ID},
			})
		}
	}
	return out
}

// modelDescription composes the secondary popover text for a model.
//
// Expected:
//   - contextLength is the model's advertised window; zero is "unknown".
//
// Returns:
//   - A short context-window hint.
//
// Side effects:
//   - None.
func modelDescription(contextLength int) string {
	if contextLength <= 0 {
		return ""
	}
	return fmt.Sprintf("%d ctx", contextLength)
}

// _ ensures the agent import is referenced even when builtins are
// stripped to a subset; keeping the import explicit avoids drift when
// /agent is the only consumer.
var _ = agent.Manifest{}
