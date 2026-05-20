import { describe, expect, it } from "vitest";
import { buildToolRenderSpec } from "./toolRenderSpec";
import type { Message } from "@/types";

function makeToolMessage(
  role: "tool_call" | "tool_result",
  toolName: string,
  toolInput: Record<string, unknown> | undefined,
  content = "",
): Message {
  return {
    id: "t1",
    role,
    content,
    timestamp: "2026-05-03T00:00:00Z",
    toolName,
    toolInput: toolInput === undefined ? undefined : JSON.stringify(toolInput),
  };
}

describe("buildToolRenderSpec", () => {
  it("uses the bash command as the heading when it fits within 80 characters", () => {
    const msg = makeToolMessage("tool_call", "bash", { command: "echo hello" });
    const spec = buildToolRenderSpec(msg);
    expect(spec.toolName).toBe("bash");
    expect(spec.heading).toBe("bash echo hello");
    expect(spec.body).toBe("");
  });

  it("truncates a bash command longer than 80 characters and appends ellipsis", () => {
    const longCommand = "x".repeat(120);
    const msg = makeToolMessage("tool_call", "bash", { command: longCommand });
    const spec = buildToolRenderSpec(msg);
    expect(spec.heading).toBe("bash " + "x".repeat(80) + "...");
  });

  it("uses filePath as the heading for write tool calls", () => {
    const msg = makeToolMessage("tool_call", "write", {
      filePath: "/tmp/a.ts",
    });
    expect(buildToolRenderSpec(msg).heading).toBe("write /tmp/a.ts");
  });

  it("uses filePath as the heading for read tool calls", () => {
    const msg = makeToolMessage("tool_call", "read", { filePath: "/tmp/b.ts" });
    expect(buildToolRenderSpec(msg).heading).toBe("read /tmp/b.ts");
  });

  it("uses filePath as the heading for edit tool calls", () => {
    const msg = makeToolMessage("tool_call", "edit", { filePath: "/tmp/c.ts" });
    expect(buildToolRenderSpec(msg).heading).toBe("edit /tmp/c.ts");
  });

  it("uses filePath as the heading for multiedit tool calls", () => {
    const msg = makeToolMessage("tool_call", "multiedit", {
      filePath: "/tmp/d.ts",
    });
    expect(buildToolRenderSpec(msg).heading).toBe("multiedit /tmp/d.ts");
  });

  it("uses filePath as the heading for apply_patch tool calls", () => {
    const msg = makeToolMessage("tool_call", "apply_patch", {
      filePath: "/tmp/e.patch",
    });
    expect(buildToolRenderSpec(msg).heading).toBe("apply_patch /tmp/e.patch");
  });

  it("uses pattern as the heading for glob tool calls", () => {
    const msg = makeToolMessage("tool_call", "glob", { pattern: "**/*.ts" });
    expect(buildToolRenderSpec(msg).heading).toBe("glob **/*.ts");
  });

  it("uses pattern as the heading for grep tool calls", () => {
    const msg = makeToolMessage("tool_call", "grep", { pattern: "TODO" });
    expect(buildToolRenderSpec(msg).heading).toBe("grep TODO");
  });

  it("uses name as the heading for skill_load tool calls", () => {
    const msg = makeToolMessage("tool_call", "skill_load", { name: "vue" });
    expect(buildToolRenderSpec(msg).heading).toBe("skill_load vue");
  });

  it("renders preferred fallback keys for tools outside the allowlist", () => {
    // Regression for the empty-toolInput bug: search_nodes / delegate /
    // coordination_store / background_output / mcp tools all dropped off
    // to the bare tool name. The tiered fallback restores them by walking
    // a priority-ordered list of common arg keys.
    expect(
      buildToolRenderSpec(
        makeToolMessage("tool_call", "search_nodes", {
          query: "FlowState recall",
          limit: 10,
        }),
      ).heading,
    ).toBe("search_nodes FlowState recall");

    expect(
      buildToolRenderSpec(
        makeToolMessage("tool_call", "delegate", {
          category: "implementation",
          subagent_type: "senior-engineer",
          message: "implement the fallback",
        }),
      ).heading,
    ).toBe("delegate senior-engineer");

    expect(
      buildToolRenderSpec(
        makeToolMessage("tool_call", "coordination_store", {
          operation: "set",
          key: "user.name",
          value: "Alice",
        }),
      ).heading,
    ).toBe("coordination_store user.name");

    expect(
      buildToolRenderSpec(
        makeToolMessage("tool_call", "background_output", { id: "bg-task-42" }),
      ).heading,
    ).toBe("background_output bg-task-42");
  });

  it("falls back to a deterministic compact-JSON object when no preferred key matches", () => {
    const msg = makeToolMessage("tool_call", "mystery_tool", {
      alpha: "first",
      zeta: "last",
    });
    // Keys are sorted to keep the rendered heading stable across reloads.
    expect(buildToolRenderSpec(msg).heading).toBe(
      'mystery_tool {"alpha":"first","zeta":"last"}',
    );
  });

  it("falls back to the bare tool name when args contains only non-string values", () => {
    const msg = makeToolMessage("tool_call", "foo_bar", {
      count: 5,
      enabled: true,
    });
    expect(buildToolRenderSpec(msg).heading).toBe("foo_bar");
  });

  it("falls back to the tool name when the primary argument is missing", () => {
    const msg = makeToolMessage("tool_call", "write", {});
    expect(buildToolRenderSpec(msg).heading).toBe("write");
  });

  it("renders a persisted bare-string toolInput as the heading directly", () => {
    // Backend accumulator persists a bare display string (not JSON) for
    // hand-coded tools. Older sessions also persisted bare strings for
    // unknown tools. The renderer must accept both shapes.
    const msg: Message = {
      id: "t-bare",
      role: "tool_call",
      content: "skill_load",
      timestamp: "2026-05-03T00:00:00Z",
      toolName: "skill_load",
      toolInput: "pre-action",
    };
    expect(buildToolRenderSpec(msg).heading).toBe("skill_load pre-action");
  });

  it("truncates long fallback values at 80 characters with an ellipsis", () => {
    const longQuery = "a".repeat(100);
    const msg = makeToolMessage("tool_call", "search_nodes", {
      query: longQuery,
    });
    expect(buildToolRenderSpec(msg).heading).toBe(
      "search_nodes " + "a".repeat(80) + "...",
    );
  });

  it("redacts sensitive arg values before rendering", () => {
    const msg = makeToolMessage("tool_call", "external_api", {
      api_key: "sk-real-key-do-not-leak",
    });
    const heading = buildToolRenderSpec(msg).heading;
    expect(heading).not.toContain("sk-real-key");
    expect(heading).toContain("[REDACTED]");
  });

  it("redacts sensitive keys inside the JSON-fallback path too", () => {
    const msg = makeToolMessage("tool_call", "external_api", {
      endpoint: "https://api.example.com",
      auth: "bearer-xyz",
    });
    const heading = buildToolRenderSpec(msg).heading;
    expect(heading).not.toContain("bearer-xyz");
    expect(heading).toContain("[REDACTED]");
    expect(heading).toContain("https://api.example.com");
  });

  it("uses message.content as the body for tool_result messages", () => {
    const msg = makeToolMessage(
      "tool_result",
      "write",
      { filePath: "/tmp/a.ts" },
      "wrote 12 bytes",
    );
    const spec = buildToolRenderSpec(msg);
    expect(spec.heading).toBe("write /tmp/a.ts");
    expect(spec.body).toBe("wrote 12 bytes");
  });

  it("returns an empty body for tool_call messages", () => {
    const msg = makeToolMessage(
      "tool_call",
      "write",
      { filePath: "/tmp/a.ts" },
      "ignored",
    );
    expect(buildToolRenderSpec(msg).body).toBe("");
  });

  it("returns empty fields for non-tool messages", () => {
    const msg: Message = {
      id: "u1",
      role: "user",
      content: "hello",
      timestamp: "2026-05-03T00:00:00Z",
    };
    expect(buildToolRenderSpec(msg)).toEqual({
      toolName: "",
      heading: "",
      body: "",
    });
  });
});
