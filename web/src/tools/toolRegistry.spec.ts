import { defineComponent } from "vue";
import { describe, expect, it } from "vitest";
import {
  CONTEXT_TOOLS,
  getToolComponent,
  isContextTool,
  registerTool,
} from "./toolRegistry";

describe("toolRegistry", () => {
  it("registers a tool and retrieves the matching component", () => {
    const ReadTool = defineComponent({ name: "ReadTool" });

    registerTool({ name: "read", component: ReadTool });

    expect(getToolComponent("read")).toBe(ReadTool);
  });

  it("registers multiple tools and retrieves the correct component for each", () => {
    const GlobTool = defineComponent({ name: "GlobTool" });
    const GrepTool = defineComponent({ name: "GrepTool" });

    registerTool({ name: "glob", component: GlobTool });
    registerTool({ name: "grep", component: GrepTool });

    expect(getToolComponent("glob")).toBe(GlobTool);
    expect(getToolComponent("grep")).toBe(GrepTool);
  });

  it("returns undefined for an unregistered tool", () => {
    expect(getToolComponent("unregistered")).toBeUndefined();
  });

  it("exposes the expected context tool names", () => {
    expect(CONTEXT_TOOLS).toEqual(["read", "glob", "grep", "list"]);
  });

  it("freezes the context tool list", () => {
    expect(Object.isFrozen(CONTEXT_TOOLS)).toBe(true);
  });

  it("identifies read as a context tool", () => {
    expect(isContextTool("read")).toBe(true);
  });

  it("does not identify bash as a context tool", () => {
    expect(isContextTool("bash")).toBe(false);
  });
});
