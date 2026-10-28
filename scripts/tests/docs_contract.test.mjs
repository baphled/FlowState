import assert from "node:assert/strict";
import { test } from "node:test";
import { anchors, checkDocuments, localLinkErrors } from "../docs_contract.mjs";

const snapshot = (source, target = "# Target\n") => ({
  documents: { "README.md": source, "guide.md": target },
  files: { "README.md": "100644", "guide.md": "100644", "image.png": "100644" },
  changed: ["README.md"],
});

for (const [title, source, failures] of [
  ["root directory link", "[Root](.)",0],
  ["inert HTML example", "```html\n<a href=\"not-a-real-link\">example</a>\n```",0],
  ["actual HTML link", "<a href=\"missing.md\">broken</a>",1],
  ["valid inline link", "# Readme\n\n[Guide](guide.md#target)\n", 0],
  ["missing file", "[Guide](missing.md)", 1],
  ["missing anchor", "[Guide](guide.md#absent)", 1],
  ["reference link", "[Guide][ref]\n\n[ref]: guide.md#absent", 1],
  ["image target", "![Image](absent.png)", 1],
  ["external links remain offline", "[Site](https://example.invalid)", 0],
  ["traversal", "[Escape](../outside.md)", 1],
  ["encoded traversal", "[Escape](%2e%2e/outside.md)", 1],
  ["unsafe scheme", "[File](file:///etc/passwd)", 1],
]) {
  test(title, () => assert.equal(localLinkErrors(snapshot(source)).length, failures));
}

test("duplicate headings have distinct GitHub slugs", () => {
  assert.deepEqual([...anchors("# Hello `Code`\n\n# Hello Code\n")], ["hello-code", "hello-code-1"]);
});

test("nonregular targets cannot pass through a committed link", () => {
  const value = snapshot("[Link](guide.md)"); value.files["guide.md"] = "120000";
  assert.equal(localLinkErrors(value).length, 1);
});

test("whole changed Markdown is linted", async () => {
  assert.ok((await checkDocuments(snapshot("#Title\n"), { default: true })).length > 0);
});

test("inline lint suppressions do not waive errors", async () => {
  assert.ok((await checkDocuments(snapshot("<!-- markdownlint-disable -->\n#Title\n"), { default: true })).length > 0);
});

test("deleted targets break untouched referring documents", () => {
  const value = snapshot("[Link](guide.md)"); delete value.files["guide.md"]; delete value.documents["guide.md"];
  value.changed = [];
  assert.equal(localLinkErrors(value).length, 1);
});
