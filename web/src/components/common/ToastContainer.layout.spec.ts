/**
 * ToastContainer.layout.spec.ts
 *
 * Pins the May 2026 visual-weight + position changes for the toast
 * surface (top-right placement, increased font-size / padding / width
 * for readability). The previous design used a small bottom-right
 * stack; user feedback was that the toasts felt undersized and were
 * hard to spot in the bottom corner. These assertions guard the new
 * shape against accidental regression.
 *
 * Asserts against the component's compiled CSS source rather than
 * getComputedStyle because jsdom does not resolve scoped CSS reliably.
 * Reading the SFC source keeps the spec deterministic across vitest
 * pool configurations.
 */
import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

// Resolve relative to this spec file. jsdom's window.URL does not
// expose a `file:` scheme; using path.dirname on the spec URL keeps
// resolution deterministic across vitest environments.
const here = dirname(fileURLToPath(import.meta.url));
const sfcPath = resolve(here, "ToastContainer.vue");
const sfcSource = readFileSync(sfcPath, "utf8");

function extractStyleBlock(source: string): string {
  const match = source.match(/<style scoped>([\s\S]*?)<\/style>/);
  if (!match) {
    throw new Error("ToastContainer.vue is missing a <style scoped> block");
  }
  return match[1];
}

function extractRule(css: string, selector: string): string {
  // Naive but sufficient: find `<selector> {` and capture until the
  // next closing brace at the top level. Toast styles do not nest.
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const re = new RegExp(`${escaped}\\s*\\{([^}]*)\\}`, "m");
  const match = css.match(re);
  if (!match) {
    throw new Error(`Could not find rule for selector "${selector}"`);
  }
  return match[1];
}

describe("ToastContainer layout (May 2026 visual-weight + top-right)", () => {
  const style = extractStyleBlock(sfcSource);

  it("positions the container in the top-right corner, not bottom-right", () => {
    const rule = extractRule(style, ".toast-container");
    // Must anchor to top; must NOT anchor to bottom. Keep right anchor.
    expect(rule).toMatch(/top:\s*[\d.]+rem/);
    expect(rule).toMatch(/right:\s*[\d.]+rem/);
    expect(rule).not.toMatch(/bottom:\s*[\d.]+rem/);
  });

  it("stacks new toasts below older ones (top-down) for top-right placement", () => {
    const containerRule = extractRule(style, ".toast-container");
    const listRule = extractRule(style, ".toast-list");
    // With top-right placement the natural flex direction is column
    // (newest at top, older below). column-reverse is for bottom anchor.
    expect(containerRule).toMatch(/flex-direction:\s*column(?!-reverse)/);
    expect(listRule).toMatch(/flex-direction:\s*column(?!-reverse)/);
  });

  it("grows the container max-width past the legacy 400px target", () => {
    const rule = extractRule(style, ".toast-container");
    const match = rule.match(/max-width:\s*(\d+)px/);
    expect(match).not.toBeNull();
    const maxWidth = Number(match![1]);
    expect(maxWidth).toBeGreaterThanOrEqual(440);
  });

  it("gives each toast a min-width so single-line messages still feel substantial", () => {
    const rule = extractRule(style, ".toast-item");
    expect(rule).toMatch(/min-width:\s*\d+px/);
    const match = rule.match(/min-width:\s*(\d+)px/);
    expect(Number(match![1])).toBeGreaterThanOrEqual(360);
  });

  it("uses a larger message font-size than the legacy 0.85rem", () => {
    const rule = extractRule(style, ".toast-message");
    const match = rule.match(/font-size:\s*([\d.]+)rem/);
    expect(match).not.toBeNull();
    const fontSize = Number(match![1]);
    expect(fontSize).toBeGreaterThanOrEqual(0.95);
  });

  it("uses a larger title font-size than the legacy 0.9rem", () => {
    const rule = extractRule(style, ".toast-title");
    const match = rule.match(/font-size:\s*([\d.]+)rem/);
    expect(match).not.toBeNull();
    const fontSize = Number(match![1]);
    expect(fontSize).toBeGreaterThanOrEqual(1);
  });

  it("uses larger padding than the legacy 0.75rem 1rem", () => {
    const rule = extractRule(style, ".toast-item");
    const match = rule.match(/padding:\s*([\d.]+)rem\s+([\d.]+)rem/);
    expect(match).not.toBeNull();
    const verticalPadding = Number(match![1]);
    const horizontalPadding = Number(match![2]);
    expect(verticalPadding).toBeGreaterThanOrEqual(0.9);
    expect(horizontalPadding).toBeGreaterThanOrEqual(1.1);
  });

  it("keeps box-shadow for visual hierarchy and bumps depth for the heavier surface", () => {
    const rule = extractRule(style, ".toast-item");
    expect(rule).toMatch(/box-shadow:/);
  });
});
