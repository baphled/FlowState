import { describe, expect, it } from "vitest";
import { SLASH_COMMANDS } from "./slashCommands";

describe("SLASH_COMMANDS", () => {
  it("mirrors the TUI builtins in registration order, with web-only commands appended", () => {
    // Source of truth for the TUI-mirrored commands:
    //   internal/tui/intents/chat/slashcommand/builtins.go RegisterBuiltins.
    // Web-only commands (currently `/compress`) are appended after the
    // TUI-mirrored block so the mirror invariant stays easy to audit.
    //
    // Web-only additions:
    //   - /compress (Deliverable 3 of the May 2026 context-accuracy
    //     bundle) — force-fires the L2 auto-compactor against the
    //     current session via POST /api/v1/sessions/{id}/compress.
    //     The TUI does not yet expose this command; when it does,
    //     this entry moves up into the TUI-mirrored block above.
    expect(SLASH_COMMANDS.map((c) => c.name)).toEqual([
      "clear",
      "help",
      "exit",
      "quit",
      "sessions",
      "plans",
      "agent",
      "agents",
      "model",
      "swarm",
      "autoresearch",
      "compress",
    ]);
  });

  it("every command has a non-empty description", () => {
    for (const cmd of SLASH_COMMANDS) {
      expect(cmd.description.length).toBeGreaterThan(0);
    }
  });
});
