import MarkdownIt from "markdown-it";
import GithubSlugger from "github-slugger";
import { posix } from "node:path";
import { lint } from "markdownlint-cli2/markdownlint/promise";

const parser = new MarkdownIt({ html: true });
parser.validateLink = () => true;

/** Collect GitHub-style heading anchors from parsed Markdown, including duplicates. */
export function anchors(source) {
  const tokens = parser.parse(source, {});
  const slugger = new GithubSlugger();
  const values = new Set();
  for (let i = 0; i < tokens.length - 1; i++) {
    if (tokens[i].type !== "heading_open") continue;
    const text = (tokens[i + 1].children ?? [])
      .filter((t) => ["text", "code_inline", "image"].includes(t.type))
      .map((t) => t.content).join("");
    values.add(slugger.slug(text));
  }
  return values;
}

/** Check local paths/Markdown anchors against the committed tree, without network access. */
export function localLinkErrors(snapshot) {
  const errors = [];
  for (const [file, source] of Object.entries(snapshot.documents)) {
    const visit = (tokens) => {
      for (const token of tokens) {
        if (["html_inline", "html_block"].includes(token.type) && /<(?:a|img)\b[^>]*(?:href|src)\s*=/i.test(token.content)) errors.push(`${file}: use Markdown links instead of raw HTML links`);
        const href = token.type === "image" ? token.attrGet("src") : token.attrGet("href");
        if (href !== null && href !== undefined) {
          try { checkTarget(file, href, snapshot); }
          catch (error) { errors.push(`${file}: ${error.message}`); }
        }
        if (token.children) visit(token.children);
      }
    };
    visit(parser.parse(source, {}));
  }
  return errors;
}

function checkTarget(file, href, snapshot) {
  if (/^(https?:|mailto:)/i.test(href)) return;
  if (/^[a-z][a-z0-9+.-]*:/i.test(href) || href.startsWith("//")) {
    throw new Error(`unsupported link scheme: ${href}`);
  }
  const [rawPath, rawAnchor] = href.split("#", 2);
  const target = decodeURIComponent(rawPath.split("?", 1)[0]);
  if (target.startsWith("/") || /[\\\0]/.test(target)) throw new Error(`unsafe local link: ${href}`);
  const name = target ? posix.normalize(posix.join(posix.dirname(file), target)) : file;
  if (name === ".." || name.startsWith("../")) throw new Error(`link leaves repository: ${href}`);
  const mode = snapshot.files[name];
  const directory = name === "." || Object.keys(snapshot.files).some((p) => p.startsWith(name.replace(/\/$/, "") + "/"));
  if (!mode && !directory) throw new Error(`missing local target: ${href}`);
  if (mode && !["100644", "100755"].includes(mode)) throw new Error(`nonregular local target: ${href}`);
  if (rawAnchor && snapshot.documents[name] !== undefined && !anchors(snapshot.documents[name]).has(decodeURIComponent(rawAnchor))) {
    throw new Error(`missing Markdown anchor: ${href}`);
  }
}

/** Lint whole changed documents and check every committed document's local links. */
export async function checkDocuments(snapshot, config) {
  const strings = Object.fromEntries(snapshot.changed
    .filter((name) => Object.hasOwn(snapshot.documents, name))
    .map((name) => [name, snapshot.documents[name]]));
  const result = await lint({ strings, config, noInlineConfig: true });
  const errors = Object.entries(result).flatMap(([name, findings]) => findings.map(
    (f) => `${name}:${f.lineNumber}: ${f.ruleNames[0]} ${f.ruleDescription}`));
  return errors.concat(localLinkErrors(snapshot));
}
