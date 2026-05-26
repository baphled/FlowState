/**
 * Static slash-command catalogue mirrored from the TUI's
 * RegisterBuiltins (internal/tui/intents/chat/slashcommand/builtins.go).
 *
 * The TUI registry stays canonical: when a new builtin lands there, this
 * file should be kept in sync. A future enhancement is to expose the
 * registry through an API endpoint and replace this static list — see
 * the bug-fix note that accompanies this delivery for justification.
 */

export interface SlashCommand {
  /** Command name without the leading slash, e.g. "clear". */
  readonly name: string;
  /** Short single-line gloss shown in the picker. */
  readonly description: string;
}

export const SLASH_COMMANDS: readonly SlashCommand[] = [
  { name: "clear", description: "Wipe the chat buffer" },
  { name: "help", description: "List available slash commands" },
  { name: "exit", description: "Exit FlowState" },
  { name: "quit", description: "Alias for /exit" },
  { name: "sessions", description: "Resume a saved session" },
  { name: "plans", description: "Ask the agent to list your saved plans" },
  { name: "agent", description: "Switch the active agent" },
  { name: "agents", description: "Switch the active agent (alias of /agent)" },
  { name: "model", description: "Switch the chat model" },
  { name: "swarm", description: "Create a new swarm manifest interactively" },
  {
    name: "autoresearch",
    description: "Launch an autoresearch optimisation run interactively",
  },
  {
    name: "compact",
    description: "Force-compact the current session’s context now",
  },
];
