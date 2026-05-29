package swarm

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/baphled/flowstate/internal/coordination"
)

// planSuffix is the coord-store sub-key the planning loop writes the
// approved plan body under, resolved as "<chainID>/plan". When a
// chainID is threaded the publisher reads this key directly; only the
// fallback path (no chainID available) suffix-scans for it.
const planSuffix = "plan"

// planMarkdownSuffix is the OPTIONAL coord-store sub-key a curated,
// pre-rendered markdown body may be written under ("<chainID>/plan-markdown").
// When present it takes precedence over the "<chainID>/plan" key — a
// plan-writer that already produced clean markdown should have it published
// verbatim rather than re-derived from the plan key.
const planMarkdownSuffix = "plan-markdown"

// sectionsPrefix is the coord-store sub-key namespace the section-
// decomposed planning SME sub-swarm writes each plan section under,
// resolved as "<chainID>/sections/<name>" (architecture / testing /
// security in v1). The deterministic publisher (Pass 2) fans these
// keys in and appends each section under the OMO spine, so the
// published plan has BOTH structure (the spine) AND depth (the
// sections). Absent for a single-plan run with no SME sub-swarm, in
// which case the spine publishes alone (backwards-compatible).
const sectionsPrefix = "sections"

// sectionNamesOrdered is the STABLE order the publisher renders SME
// sections in (architecture → testing → security), independent of Go
// map-iteration order or coord-store key ordering. A section whose key
// is absent or whose value is not parseable section-v1 JSON is skipped;
// the remaining sections still render in this order. Extend this slice
// (and the SME sub-swarm's member set) to add a new section.
var sectionNamesOrdered = []string{"architecture", "testing", "security"}

// sectionsHeading is the markdown heading that introduces the appended
// SME section block, so a reader can see where the lightweight OMO spine
// ends and the detailed SME depth begins.
const sectionsHeading = "## Detailed Sections"

// reviewSuffix is the coord-store sub-key the plan-reviewer writes its
// verdict under, resolved as "<chainID>/review". The publisher consults
// it to gate publication on an approved plan when the record is present
// and parseable; an absent or malformed review does NOT block publication
// (the plan-reviewer post-member gate has already run by post-swarm time,
// so a non-empty plan key is itself evidence the loop reached completion).
const reviewSuffix = "review"

// approveVerdict is the review verdict that authorises publication.
const approveVerdict = "approve"

// planEnvelope is the JSON envelope the plan-writer emits under
// "<chainID>/plan" in the live planning loop:
//
//	{"markdown": "# Title\n...", "id": "<chainID>", "title": "Title"}
//
// Only Markdown and Title are load-bearing for the publisher. When the
// coord-store value is NOT this JSON shape (e.g. a raw markdown body),
// the publisher falls back to treating the whole value as the plan body.
type planEnvelope struct {
	Markdown string `json:"markdown"`
	Title    string `json:"title"`
}

// sectionEnvelope is the section-v1 JSON shape each SME section
// specialist writes under "<chainID>/sections/<name>" (see
// SectionV1Schema). The publisher parses it to render the section under
// the OMO spine: Title becomes a "## " heading, Body the markdown
// beneath it, and KeyPoints a trailing bullet list. A value that does
// not parse as this shape — or carries no usable Title/Body — is SKIPPED
// (not an error): a malformed section must never block publication of
// the spine and the valid sections.
type sectionEnvelope struct {
	Section   string   `json:"section"`
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	KeyPoints []string `json:"key_points"`
}

// reviewEnvelope is the minimal shape the publisher reads from the
// plan-reviewer's "<chainID>/review" record. Only Verdict is consumed;
// DisallowUnknownFields is intentionally OFF so the reviewer may carry
// confidence/reasoning/etc without breaking the publish gate.
type reviewEnvelope struct {
	Verdict string `json:"verdict"`
}

// PublishPlanToVault writes the planning loop's approved plan from the
// coordination store to a real markdown file in the vault, then records
// the true path under "<chainID>/plan_publication". This is the
// DETERMINISTIC publish path (Defect 2 / Defect 4): the loop produced an
// approved plan in the coord-store but nothing reliably wrote it to the
// vault — the only prior publish path was an LLM agent emitting a write
// tool call, which hits the synthesis-hang and never fires. A code-level
// write cannot be talked past by a stalled model.
//
// Behaviour:
//   - Chain resolution (Bug 1 fix — wrong-chain targeting):
//   - When chainID != "" the publisher reads "<chainID>/plan" DIRECTLY.
//     No suffix-scan: a live coord-store carries dozens of "*/plan" keys
//     across historical chains, so a scan returns an arbitrary stale
//     chain (Go map-iteration order), NOT the current run's plan. The
//     threaded chainID is the authoritative target.
//   - When chainID == "" (genuinely unavailable) the publisher FALLS BACK
//     to a suffix-scan for the first "*/plan" key. This is best-effort:
//     the underlying FileStore is a plain map with no per-key write-order
//     or mtime, so "most recently written" cannot be honoured — the
//     scan returns a non-deterministically-ordered key. Callers SHOULD
//     thread a chainID (the swarm post-phase now does) so this fallback
//     is rarely hit; see the engine's chainID capture in
//     publishPlanForSwarm.
//   - When a "<chainID>/review" record is present AND parseable, the
//     verdict MUST be "approve" to publish; a non-approve verdict is a
//     no-op (no file, no record). An absent/malformed review does NOT
//     block publication — the plan-reviewer post-member gate already ran.
//   - Body resolution, in precedence order (see resolvePlanBody / parsePlan):
//   - an existing "<chainID>/plan-markdown" key (curated markdown), else
//   - the {"markdown": ...} envelope's Markdown, else
//   - the raw value treated as the markdown body.
//   - Plan-document validation (the incident guard): the resolved body MUST
//     be a coherent markdown plan. A raw JSON agent-spec blob, a heading-less
//     prose dump, or a bare-heading stub is NOT a plan and is REFUSED — the
//     publish returns an error and writes NO file, so the loop honest-fails
//     rather than shipping garbage to the vault (see isPlanDocument). The old
//     publisher rendered ANY JSON object to markdown and wrote it; that
//     over-broad salvage is exactly how a JSON agent-spec at "<chainID>/plan"
//     became 200 lines of unusable markdown in the user's vault.
//   - Derives a deterministic, idempotent filename: the envelope title, else
//     the body's first "# H1" heading, else the chainID; rendered as a
//     READABLE, filesystem-safe name that PRESERVES the title's case, spaces,
//     hyphens, parentheses and em-dash (matching the user's Obsidian vault
//     convention) — only filesystem-unsafe characters are stripped/replaced
//     (see planFileName). The same plan always writes the same file
//     (overwrite-safe), and the atomic temp+rename write handles the spaces.
//   - Writes atomically (temp in the same dir + fsync + rename + parent
//     dir fsync), mirroring the pathguard permissions-writer convention
//     (memory: feedback_atomicity_awareness_uneven). No flock is needed
//     for the plan file — a single post-swarm flush owns the write.
//   - Records "<chainID>/plan_publication" as
//     {"vault_path": "<abs path>", "published_at": "<RFC3339>"} — the
//     REAL path just written, so the honesty gate verifies a true claim.
//
// Expected:
//   - store is the active coordination store (non-nil in production).
//   - outputDir is the resolved plan_output_dir (absolute). Empty
//     disables publication — without a configured target the publisher
//     cannot write an honest, containable path, so it is a no-op rather
//     than scattering files at an unknown location.
//   - chainID is the lead-allocated coordination chain identifier for the
//     run. Empty triggers the suffix-scan fallback described above.
//
// Returns:
//   - The absolute vault path written and nil on a successful publish.
//   - "" and nil when there is nothing to publish (no plan key, empty
//     plan body, non-approve verdict, or no output dir configured) — a
//     no-op is honest and must NOT fabricate a publication record.
//   - "" and a non-nil error when a publish was attempted but the write
//     or record failed. The caller surfaces this so the post-swarm
//     honesty gate then fails (the error is NOT swallowed).
//
// Side effects:
//   - On a successful publish: one file written under outputDir and one
//     coord-store key set ("<chainID>/plan_publication").
func PublishPlanToVault(store coordination.Store, outputDir, chainID string) (string, error) {
	if store == nil {
		return "", errors.New("publish plan: coordination store unavailable")
	}
	if strings.TrimSpace(outputDir) == "" {
		// No configured target: a no-op rather than writing to an
		// unknown/uncontainable location. The honesty gate's plan-key
		// check still applies independently.
		return "", nil
	}

	resolvedChain, planRaw, found, err := resolvePlanForChain(store, chainID)
	if err != nil {
		return "", fmt.Errorf("publish plan: resolving plan from coord-store: %w", err)
	}
	if !found || strings.TrimSpace(string(planRaw)) == "" {
		// Nothing to publish — honest no-op, no fabricated record.
		return "", nil
	}

	if approved, ok := reviewApproves(store, resolvedChain); ok && !approved {
		// The reviewer explicitly did not approve; do not publish a
		// rejected plan to the vault.
		return "", nil
	}

	title, body := resolvePlanBody(store, resolvedChain, planRaw)
	if strings.TrimSpace(body) == "" {
		return "", nil
	}

	// Plan-document validation (the incident guard): the resolved body must
	// be a coherent markdown plan, NOT a raw JSON agent-spec blob, empty, or
	// a trivial stub. The old publisher rendered ANY JSON object to markdown
	// and wrote it — a JSON agent spec at "<chainID>/plan" became 200 lines
	// of unusable markdown in the user's vault. Refusing to publish a
	// non-plan artifact (returning an error, never a file) means the loop
	// honest-fails: publishPlanForSwarm propagates this so the post-swarm
	// honesty gate is not reached on a corrupt body, and NO garbage lands in
	// the vault. The gate independently re-validates (defence in depth).
	if ok, reason := isPlanDocument(body); !ok {
		return "", fmt.Errorf("publish plan: refusing to publish chain %q: %s", resolvedChain, reason)
	}

	// Pass 2 — SME section fan-in: the spine (validated above) is the lightweight
	// OMO scaffold; the section-decomposed planning sub-swarm may have written
	// "<chainID>/sections/<name>" depth keys. Assemble the document as the spine
	// FIRST, then each present section appended beneath. The fan-in is PURELY
	// ADDITIVE: a run with no section keys yields assembledBody == body (the
	// spine alone, unchanged), preserving every existing single-plan run. The
	// filename is still derived from the spine's title/body, not the sections.
	assembledBody := assemblePlanBody(store, resolvedChain, body)

	name := planFileName(title, body, resolvedChain)
	vaultPath := filepath.Join(outputDir, name+".md")

	if err := atomicWriteFile(vaultPath, []byte(assembledBody)); err != nil {
		return "", fmt.Errorf("publish plan: writing vault file %q: %w", vaultPath, err)
	}

	if err := recordPublication(store, resolvedChain, vaultPath); err != nil {
		return "", fmt.Errorf("publish plan: recording publication for %q: %w", resolvedChain, err)
	}

	return vaultPath, nil
}

// resolvePlanForChain locates the plan body and its concrete chainID.
//
// When chainID is non-empty the "<chainID>/plan" key is read DIRECTLY
// (Bug 1 fix): a multi-chain store must not be suffix-scanned, which would
// return an arbitrary stale chain. A missing named key is a clean no-op
// (found=false) — the publisher must NOT silently fall back to a different
// chain when an explicit target was named.
//
// When chainID is empty the legacy suffix-scan fallback applies: the first
// "*/plan" key wins. This is non-deterministic (the FileStore is a plain
// map with no write-order) and exists only for callers that cannot thread
// a chainID; the swarm post-phase now always threads one.
//
// Returns:
//   - The concrete chainID, the plan bytes, found=true on a hit.
//   - ("", nil, false, nil) when no plan is available.
//   - A non-nil error only on a store read/list failure.
func resolvePlanForChain(store coordination.Store, chainID string) (string, []byte, bool, error) {
	if strings.TrimSpace(chainID) != "" {
		key := chainID + "/" + planSuffix
		exists, err := store.Exists(key)
		if err != nil {
			return "", nil, false, fmt.Errorf("probing plan key %q: %w", key, err)
		}
		if !exists {
			// Named chain has no plan: honest no-op, do NOT scan for a
			// different chain.
			return "", nil, false, nil
		}
		raw, err := store.Get(key)
		if err != nil {
			return "", nil, false, fmt.Errorf("reading plan key %q: %w", key, err)
		}
		return chainID, raw, true, nil
	}
	return scanForSuffix(store, planSuffix)
}

// scanForSuffix returns the chainID prefix and value of the first
// coord-store key ending in "/<suffix>". The chainID is the key with the
// "/<suffix>" tail stripped (e.g. "readyz-2026-05-28/plan" → chainID
// "readyz-2026-05-28"). found is false when no such key exists.
func scanForSuffix(store coordination.Store, suffix string) (chainID string, value []byte, found bool, err error) {
	keys, listErr := store.List("")
	if listErr != nil {
		return "", nil, false, fmt.Errorf("listing coord-store keys: %w", listErr)
	}
	tail := "/" + suffix
	for _, k := range keys {
		if strings.HasSuffix(k, tail) {
			v, getErr := store.Get(k)
			if getErr != nil {
				return "", nil, false, fmt.Errorf("reading coord-store key %q: %w", k, getErr)
			}
			return strings.TrimSuffix(k, tail), v, true, nil
		}
	}
	return "", nil, false, nil
}

// reviewApproves reports the reviewer's verdict for chainID. ok is false
// when no review record exists or it does not parse — the caller then
// treats publication as authorised (the post-member review gate already
// ran). approved is true only when a parseable record carries
// verdict == "approve".
func reviewApproves(store coordination.Store, chainID string) (approved bool, ok bool) {
	key := chainID + "/" + reviewSuffix
	exists, err := store.Exists(key)
	if err != nil || !exists {
		return false, false
	}
	raw, err := store.Get(key)
	if err != nil {
		return false, false
	}
	var rev reviewEnvelope
	if err := json.Unmarshal(raw, &rev); err != nil {
		return false, false
	}
	return strings.EqualFold(strings.TrimSpace(rev.Verdict), approveVerdict), true
}

// resolvePlanBody returns the title and markdown body to publish for
// resolvedChain, applying the precedence order:
//
//  1. "<chainID>/plan-markdown" — a curated, pre-rendered markdown body
//     takes precedence over the plan key.
//  2. otherwise the planRaw value is parsed by parsePlan, which unwraps the
//     {"markdown": ...} envelope and otherwise returns the raw value
//     verbatim. The caller (PublishPlanToVault) then validates the body as
//     a plan document before writing — a bare JSON object surfaced verbatim
//     here is REFUSED there, not rendered.
//
// store is consulted only for the optional plan-markdown key; planRaw is
// the already-read "<chainID>/plan" value so the hot path makes at most
// one extra Exists probe.
func resolvePlanBody(store coordination.Store, resolvedChain string, planRaw []byte) (title, body string) {
	if md, ok := planMarkdownOverride(store, resolvedChain); ok {
		// A curated markdown body wins; title is derived from its first
		// H1 (or falls through to the chainID) by slugifyPlanName.
		return "", md
	}
	return parsePlan(planRaw)
}

// planMarkdownOverride returns the "<chainID>/plan-markdown" body when the
// key exists and is non-empty. A missing key or read error is treated as
// "no override" (ok=false) so the envelope / raw plan-key path applies.
func planMarkdownOverride(store coordination.Store, chainID string) (string, bool) {
	key := chainID + "/" + planMarkdownSuffix
	exists, err := store.Exists(key)
	if err != nil || !exists {
		return "", false
	}
	raw, err := store.Get(key)
	if err != nil {
		return "", false
	}
	if strings.TrimSpace(string(raw)) == "" {
		return "", false
	}
	return string(raw), true
}

// parsePlan extracts a title and the markdown body from a single plan
// value, in precedence order:
//
//  1. the {"markdown": ...} envelope — Markdown is the body, Title the
//     title (the live planning-loop plan-writer shape);
//  2. otherwise the raw value is the body verbatim and the title is left
//     empty (slugifyPlanName then derives one from the H1 or chainID).
//
// A bare structured-JSON object (NOT the markdown envelope) is DELIBERATELY
// returned verbatim as the body, NOT rendered to markdown. The earlier "Bug
// 2 fix" rendered any JSON object to headings+bullets; that over-broad
// salvage is exactly how a JSON agent-spec blob at "<chainID>/plan" became
// 200 lines of garbage in the vault (the incident). isPlanDocument then
// rejects the verbatim JSON object as a non-plan artifact, so the publish
// refuses it and the loop honest-fails. The publisher's job is to write a
// COHERENT plan or refuse — not to dress up a spec blob as one.
func parsePlan(raw []byte) (title, body string) {
	var env planEnvelope
	if err := json.Unmarshal(raw, &env); err == nil && strings.TrimSpace(env.Markdown) != "" {
		return env.Title, env.Markdown
	}

	return "", string(raw)
}

// assemblePlanBody returns the final markdown to write: the OMO spine first,
// then — when the section-decomposed planning SME sub-swarm wrote any
// "<chainID>/sections/<name>" keys — a "## Detailed Sections" block with each
// present section rendered beneath it in the STABLE sectionNamesOrdered order.
//
// The fan-in is PURELY ADDITIVE and backwards-compatible: when NO section key
// is present (a single-plan run with no SME sub-swarm) the spine is returned
// VERBATIM — assembledBody == spine, byte-for-byte, exactly as before Pass 2 —
// so no sections heading and no trailing content are added. A section key that
// is absent, empty, or not parseable section-v1 JSON is SKIPPED (never an
// error): a malformed section must not block publication of the spine and the
// valid sections.
//
// resolvedChain is the concrete chain the spine resolved under; the section
// keys share it ("<chainID>/sections/<name>"), so the same resolution is
// reused — no separate chain lookup.
func assemblePlanBody(store coordination.Store, resolvedChain, spine string) string {
	sections := collectSections(store, resolvedChain)
	if len(sections) == 0 {
		// Backwards-compat: no sections → the spine alone, unchanged.
		return spine
	}

	var b strings.Builder
	b.WriteString(strings.TrimRight(spine, "\n"))
	b.WriteString("\n\n")
	b.WriteString(sectionsHeading)
	b.WriteString("\n")
	for _, sec := range sections {
		b.WriteString("\n")
		b.WriteString(renderSection(sec))
	}
	return b.String()
}

// collectSections reads every present "<chainID>/sections/<name>" key in the
// stable sectionNamesOrdered order, parses each as section-v1 JSON, and returns
// the renderable sections. A key that is absent, empty, unreadable, not
// section-v1 JSON, or carries no usable title/body is SKIPPED — so a malformed
// or missing section never blocks the others. The result preserves
// sectionNamesOrdered, so the output is deterministic regardless of coord-store
// key ordering.
func collectSections(store coordination.Store, resolvedChain string) []sectionEnvelope {
	var out []sectionEnvelope
	for _, name := range sectionNamesOrdered {
		key := resolvedChain + "/" + sectionsPrefix + "/" + name
		exists, err := store.Exists(key)
		if err != nil || !exists {
			continue
		}
		raw, err := store.Get(key)
		if err != nil || strings.TrimSpace(string(raw)) == "" {
			continue
		}
		var sec sectionEnvelope
		if err := json.Unmarshal(raw, &sec); err != nil {
			// Not section-v1 JSON (malformed / a bare string / garbage):
			// skip gracefully, do not fail the publish.
			continue
		}
		if strings.TrimSpace(sec.Title) == "" && strings.TrimSpace(sec.Body) == "" {
			// No usable content to render: nothing to contribute, skip.
			continue
		}
		out = append(out, sec)
	}
	return out
}

// renderSection renders one section-v1 envelope as a markdown block: the
// section Title as a "## " heading, the Body markdown beneath, and KeyPoints
// (when any) as a trailing "- " bullet list. An empty Title falls back to the
// canonical Section name (already title-handled by collectSections's
// usable-content guard) so a heading is always present.
func renderSection(sec sectionEnvelope) string {
	var b strings.Builder
	heading := strings.TrimSpace(sec.Title)
	if heading == "" {
		heading = strings.TrimSpace(sec.Section)
	}
	b.WriteString("## ")
	b.WriteString(heading)
	b.WriteString("\n")

	if body := strings.TrimSpace(sec.Body); body != "" {
		b.WriteString("\n")
		b.WriteString(body)
		b.WriteString("\n")
	}

	points := nonEmptyPoints(sec.KeyPoints)
	if len(points) > 0 {
		b.WriteString("\n")
		for _, p := range points {
			b.WriteString("- ")
			b.WriteString(p)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// nonEmptyPoints returns the key points with blank entries dropped and each
// trimmed, so a stray empty string in key_points does not render a bare "- "
// bullet.
func nonEmptyPoints(points []string) []string {
	var out []string
	for _, p := range points {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// fileNameMaxLen is the hard character cap for a plan filename (excluding
// the ".md" extension). A name longer than this is trimmed at a WORD
// boundary so no partial word survives. 80 keeps the filename comfortably
// under typical path limits (255 bytes) while leaving room for long, readable
// Title Case names that match the vault convention.
const fileNameMaxLen = 80

// unsafeFileNameChars are the characters that are illegal or hazardous in a
// filename across Windows/macOS/Linux. Each is REMOVED rather than replaced
// in place: a path separator ("/" or "\") would punch a hole in the output
// directory, and ":" / "*" / "?" / quotes / angle brackets / pipe are
// reserved on at least one common filesystem. Collapsing the gap they leave
// to a single space (see normaliseFileName) keeps the name clean.
//
// NOT in this set, deliberately: spaces, hyphens, parentheses and the "—"
// em-dash — all valid in filenames and load-bearing for the user's Obsidian
// vault convention (e.g. "Mental Health Companion — Plan.md").
const unsafeFileNameChars = `/\:*?"<>|`

// planFileName derives a deterministic, filesystem-safe, READABLE filename
// for the plan file (without the ".md" extension). Preference order for the
// source text: explicit title, the body's first "# H1" heading, then a
// readable form of the chainID.
//
// Unlike the previous kebab-slug, the title's CASE, internal spaces, hyphens,
// parentheses and "—" em-dash are PRESERVED so the published file matches the
// user's Obsidian vault naming (Title Case with spaces). Only filesystem-
// unsafe characters and control characters are stripped, repeated whitespace
// is collapsed, a leading "." (a dotfile) is dropped, and the name is capped
// at fileNameMaxLen on a word boundary with no trailing punctuation or space.
//
// The transform is deterministic and idempotent: the same plan always yields
// the same filename, so re-publishing overwrites the same file rather than
// littering the vault. When no usable title is available the chainID is
// rendered readably (hyphens → spaces, title-cased) rather than as a raw slug.
func planFileName(title, body, chainID string) string {
	source := strings.TrimSpace(title)
	if source == "" {
		source = firstH1(body)
	}

	name := normaliseFileName(source)
	if name == "" {
		// No usable title: fall back to a READABLE form of the chainID
		// (e.g. "off-chain-plan" → "Off Chain Plan") rather than a raw slug.
		name = normaliseFileName(readableChainID(chainID))
	}
	if name == "" {
		name = "Plan"
	}
	return name
}

// normaliseFileName renders s as a clean, filesystem-safe filename while
// preserving its case, spaces, hyphens, parentheses and the "—" em-dash.
// It strips filesystem-unsafe and control characters, collapses every run of
// whitespace to a single space, drops a leading "." (so the result is never a
// dotfile), caps the length at fileNameMaxLen on a word boundary, and trims
// trailing punctuation and whitespace so the name never ends mid-word or with
// a stray separator. Returns "" when nothing safe remains.
func normaliseFileName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case strings.ContainsRune(unsafeFileNameChars, r):
			// Unsafe char (path separators, reserved chars): drop it and
			// leave a space so the surrounding words stay separated; the
			// whitespace collapse below tidies any resulting run.
			b.WriteRune(' ')
		case r < 0x20 || r == 0x7f:
			// Control characters (incl. NUL, tab, newline): drop, leaving a
			// space for the same separation reason.
			b.WriteRune(' ')
		default:
			b.WriteRune(r)
		}
	}

	name := collapseWhitespace(b.String())
	// A leading "." would make the file hidden; strip it (and re-tidy).
	name = collapseWhitespace(strings.TrimLeft(name, "."))
	name = capFileName(name)
	return name
}

// collapseWhitespace replaces every run of whitespace with a single space and
// trims leading/trailing whitespace, so "Foo   Bar " becomes "Foo Bar".
func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// capFileName trims name to at most fileNameMaxLen runes, preferring the last
// space boundary so no partial word survives, then strips any trailing
// punctuation or whitespace. Idempotent: a name already within the cap is
// returned unchanged (modulo trailing-punctuation tidy).
func capFileName(name string) string {
	runes := []rune(name)
	if len(runes) > fileNameMaxLen {
		runes = runes[:fileNameMaxLen]
		// Prefer a word boundary so the name never ends mid-word.
		if idx := lastSpace(runes); idx > 0 {
			runes = runes[:idx]
		}
		name = string(runes)
	}
	// No trailing space or sentence punctuation on the filename.
	return strings.TrimRight(name, " \t.,;:!-—")
}

// lastSpace returns the index of the last ASCII space in runes, or -1 when
// none is present.
func lastSpace(runes []rune) int {
	for i := len(runes) - 1; i >= 0; i-- {
		if runes[i] == ' ' {
			return i
		}
	}
	return -1
}

// readableChainID renders a chainID as a human-readable phrase: hyphens and
// underscores become spaces and each word is title-cased, so
// "off-chain-plan" → "Off Chain Plan". This is the no-title fallback — a
// readable name rather than a raw slug. A chainID that is already prose (e.g.
// "Mental Health Companion") survives unchanged.
func readableChainID(chainID string) string {
	chainID = strings.TrimSpace(chainID)
	if chainID == "" {
		return ""
	}
	replaced := strings.NewReplacer("-", " ", "_", " ").Replace(chainID)
	words := strings.Fields(replaced)
	for i, w := range words {
		words[i] = titleWord(w)
	}
	return strings.Join(words, " ")
}

// titleWord upper-cases the first rune of w and leaves the remainder as-is, so
// an already-capitalised or acronymic word ("API") is not flattened.
func titleWord(w string) string {
	if w == "" {
		return ""
	}
	runes := []rune(w)
	runes[0] = []rune(strings.ToUpper(string(runes[0])))[0]
	return string(runes)
}

// firstH1 returns the text of the first ATX "# " heading in body, or ""
// when none is present. Only a single leading "# " (H1) is matched;
// deeper headings ("##", "###") are ignored so the filename tracks the
// plan's title, not a sub-section.
func firstH1(body string) string {
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
		}
	}
	return ""
}

// recordPublication writes the honest "<chainID>/plan_publication" record
// pointing at the real path just written. The shape matches the honesty
// gate's planPublication reader: {"vault_path": ..., "published_at": ...}.
func recordPublication(store coordination.Store, chainID, vaultPath string) error {
	record := struct {
		VaultPath   string `json:"vault_path"`
		PublishedAt string `json:"published_at"`
	}{
		VaultPath:   vaultPath,
		PublishedAt: time.Now().UTC().Format(time.RFC3339),
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshalling publication record: %w", err)
	}
	if err := store.Set(chainID+"/"+planPublicationSuffix, encoded); err != nil {
		return fmt.Errorf("writing publication record: %w", err)
	}
	return nil
}

// atomicWriteFile writes data to path durably: a temp file in the same
// directory is written, fsync'd, then renamed over the target, and the
// parent directory is fsync'd to commit the rename. On any failure the
// target is left untouched and the temp file is cleaned up. Mirrors the
// pathguard permissions-writer convention (memory:
// feedback_atomicity_awareness_uneven) minus the flock — a single
// post-swarm flush owns this write, so cross-process locking is not
// required for the plan file.
func atomicWriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating output dir %q: %w", dir, err)
	}

	tempFile, err := os.CreateTemp(dir, ".plan-*.md.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tempPath := tempFile.Name()
	// Clean up the temp file on any failure path. After a successful
	// rename the temp is gone and Stat returns ENOENT, which we ignore.
	defer func() {
		if _, statErr := os.Stat(tempPath); statErr == nil {
			_ = os.Remove(tempPath)
		}
	}()

	if _, err := tempFile.Write(data); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tempFile.Sync(); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("fsync temp file: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}

	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("renaming temp file into place: %w", err)
	}

	// fsync the parent directory so the rename's directory entry is
	// committed to the journal — without this a crash between rename and
	// the next sync can leave the entry pointing at the old inode.
	if dirFile, err := os.Open(dir); err == nil {
		_ = dirFile.Sync()
		_ = dirFile.Close()
	}

	return nil
}
