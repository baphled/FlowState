package swarm

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
// When present it takes precedence over the structured-JSON render of the
// "<chainID>/plan" key — a plan-writer that already produced clean markdown
// should have it published verbatim rather than re-derived from JSON.
const planMarkdownSuffix = "plan-markdown"

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
//   - Body resolution (Bug 2 fix — JSON published raw), in precedence
//     order (see resolvePlanBody / parsePlan):
//   - an existing "<chainID>/plan-markdown" key (curated markdown), else
//   - the {"markdown": ...} envelope's Markdown, else
//   - a structured-JSON object rendered to readable markdown (headings +
//     paragraphs + bullet lists), else
//   - the raw value treated as the markdown body (existing behaviour).
//   - Derives a deterministic, idempotent filename: the envelope/JSON
//     title, else the body's first "# H1" heading, else the chainID;
//     slugified to a safe filename. The same plan always writes the same
//     file (overwrite-safe).
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

	slug := slugifyPlanName(title, body, resolvedChain)
	vaultPath := filepath.Join(outputDir, slug+".md")

	if err := atomicWriteFile(vaultPath, []byte(body)); err != nil {
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
// resolvedChain, applying the precedence order (Bug 2 fix):
//
//  1. "<chainID>/plan-markdown" — a curated, pre-rendered markdown body
//     takes precedence over re-deriving from the plan key.
//  2. otherwise the planRaw value is parsed by parsePlan, which handles
//     the {"markdown": ...} envelope, a structured-JSON object (rendered
//     to readable markdown), and a raw-markdown body in turn.
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
// "no override" (ok=false) so the structured-JSON / envelope / raw path
// applies.
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
//  2. a structured-JSON object (a JSON object that is NOT the markdown
//     envelope) — rendered to readable markdown by renderStructuredPlan,
//     with the title derived from a title/name/purpose field (Bug 2 fix:
//     such bodies used to be dumped raw into the vault);
//  3. otherwise the raw value is the body and the title is left empty
//     (slugifyPlanName then derives one from the H1 or chainID).
func parsePlan(raw []byte) (title, body string) {
	var env planEnvelope
	if err := json.Unmarshal(raw, &env); err == nil && strings.TrimSpace(env.Markdown) != "" {
		return env.Title, env.Markdown
	}

	// Try a generic JSON object. encoding/json into a map succeeds only
	// for a JSON object (`{...}`); arrays, numbers, strings, and raw
	// markdown all fail the unmarshal and fall through to the raw path.
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err == nil && len(obj) > 0 {
		return structuredPlanTitle(obj), renderStructuredPlan(obj)
	}

	return "", string(raw)
}

// structuredPlanTitle derives a human title from a structured-JSON plan
// object for the filename slug: an explicit "title", else "name", else a
// short lead fragment of "purpose". Empty when none is present
// (slugifyPlanName then falls back to the H1 or chainID).
//
// The "purpose" field is the WORST title source — a plan's purpose is often
// a single run-on sentence (the live mental-health plan's purpose is one
// ~200-char clause with no early full stop). Taking the whole sentence as a
// title produced a 200+ char filename before the slug cap was added; even
// with the cap, a lead FRAGMENT (first clause up to a comma/em-dash) reads
// far better than a hard-truncated mid-sentence slug. slugifyPlanName still
// length-caps whatever this returns, so the fragment is an upper-quality
// hint, not the sole guard.
func structuredPlanTitle(obj map[string]json.RawMessage) string {
	if t := jsonStringField(obj, "title"); t != "" {
		return t
	}
	if n := jsonStringField(obj, "name"); n != "" {
		return n
	}
	if p := jsonStringField(obj, "purpose"); p != "" {
		return firstPurposeFragment(p)
	}
	return ""
}

// jsonStringField returns obj[key] when it decodes as a non-empty string;
// "" otherwise (missing key or non-string value).
func jsonStringField(obj map[string]json.RawMessage, key string) string {
	raw, ok := obj[key]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return strings.TrimSpace(s)
}

// firstPurposeFragment returns a short lead fragment of a "purpose" string
// to seed the filename title. It stops at the FIRST clause boundary — a
// comma or an em/en-dash (', ', '—', '–') — or a sentence terminator
// ('.', '!', '?'), whichever comes first; the whole (trimmed) string when
// none is present. The clause boundary is checked before the sentence
// terminator because a run-on purpose typically has commas long before its
// only full stop, so the comma yields the readable lead clause while the
// full stop would return the entire run-on sentence.
//
// This is a quality hint only: slugifyPlanName still length-caps the slug,
// so even a fragment with no early delimiter cannot produce an over-long
// filename.
func firstPurposeFragment(s string) string {
	for i, r := range s {
		switch r {
		case ',', '—', '–', '.', '!', '?':
			return strings.TrimSpace(s[:i])
		}
	}
	return strings.TrimSpace(s)
}

// renderStructuredPlan deterministically renders a structured-JSON plan
// object to readable markdown (Bug 2 fix). The transform:
//   - top-level keys become "## Heading" (title-cased, underscores and
//     hyphens → spaces), emitted in stable lexicographic key order so the
//     output is byte-identical across runs (Go map iteration is random);
//   - string values become paragraphs;
//   - arrays of scalars become "- bullet" lists;
//   - nested objects become "### Subheading" + their own rendered fields;
//   - other shapes (numbers, bools, nested arrays) fall back to a compact
//     JSON literal so no data is silently dropped.
func renderStructuredPlan(obj map[string]json.RawMessage) string {
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		b.WriteString("## ")
		b.WriteString(titleCaseKey(k))
		b.WriteString("\n\n")
		b.WriteString(renderJSONValue(obj[k], 3))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// renderJSONValue renders a single JSON value to markdown. headingLevel is
// the level (e.g. 3 for "###") used for nested-object subheadings so a
// "## Boundaries" object renders its fields as "### Must Not".
func renderJSONValue(raw json.RawMessage, headingLevel int) string {
	// String → paragraph.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s) + "\n"
	}

	// Array → bullet list of scalars (objects/arrays inside fall back to
	// a compact literal per element so nothing is dropped).
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err == nil {
		var b strings.Builder
		for _, item := range arr {
			b.WriteString("- ")
			b.WriteString(scalarOrCompact(item))
			b.WriteString("\n")
		}
		return b.String()
	}

	// Object → subheading per field.
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err == nil {
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		hashes := strings.Repeat("#", headingLevel)
		var b strings.Builder
		for _, k := range keys {
			b.WriteString(hashes)
			b.WriteString(" ")
			b.WriteString(titleCaseKey(k))
			b.WriteString("\n\n")
			b.WriteString(renderJSONValue(obj[k], headingLevel+1))
			b.WriteString("\n")
		}
		return strings.TrimRight(b.String(), "\n") + "\n"
	}

	// Number / bool / null → compact literal.
	return scalarOrCompact(raw) + "\n"
}

// scalarOrCompact renders a JSON value as a plain string when it is a
// string scalar, else as its compact JSON literal (numbers, bools, nested
// shapes). Keeps bullet/array elements readable without dropping data.
func scalarOrCompact(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	compact := strings.TrimSpace(string(raw))
	return compact
}

// titleCaseKey converts a JSON object key to a display heading:
// underscores and hyphens become spaces and each word is capitalised
// ("must_not" → "Must Not", "responsibilities" → "Responsibilities").
func titleCaseKey(key string) string {
	replaced := strings.NewReplacer("_", " ", "-", " ").Replace(key)
	fields := strings.Fields(replaced)
	for i, f := range fields {
		runes := []rune(f)
		if len(runes) == 0 {
			continue
		}
		runes[0] = []rune(strings.ToUpper(string(runes[0])))[0]
		fields[i] = string(runes)
	}
	return strings.Join(fields, " ")
}

// slugMaxLen is the hard character cap for a plan filename slug (excluding
// the ".md" extension). A title that slugs longer than this is trimmed at a
// hyphen boundary so no partial word survives. 60 keeps the filename
// comfortably under typical path limits while staying readable.
const slugMaxLen = 60

// slugWordCap is the soft cap on the number of hyphen-delimited words kept
// in a slug. Most readable plan filenames are a handful of words; capping
// the word count first yields a cleaner slug than a raw character truncation
// (which can chop mid-phrase). The slugMaxLen character cap is then applied
// as a backstop for the rare case of a few very long words.
const slugWordCap = 8

// slugifyPlanName derives a deterministic, filesystem-safe slug for the
// plan file. Preference order: explicit title, the body's first "# H1"
// heading, then the chainID. The result is lowercased, spaces and unsafe
// characters collapse to single hyphens, and path separators are
// stripped — the same plan always yields the same slug (idempotent
// overwrite).
//
// The slug is then length-capped (capSlug): without a cap a title derived
// from a long run-on "purpose" sentence produced a 200+ char filename. The
// cap keeps the first slugWordCap words and at most slugMaxLen characters,
// trimming any partial trailing word and trailing hyphens — the cap is the
// load-bearing guard regardless of how the title was derived.
func slugifyPlanName(title, body, chainID string) string {
	name := strings.TrimSpace(title)
	if name == "" {
		name = firstH1(body)
	}
	if name == "" {
		name = chainID
	}
	slug := capSlug(slugify(name))
	if slug == "" {
		slug = capSlug(slugify(chainID))
	}
	if slug == "" {
		slug = "plan"
	}
	return slug
}

// capSlug trims an already-slugified string to a sane filename length:
// keeps at most slugWordCap hyphen-delimited words, then at most slugMaxLen
// characters (dropping a partial trailing word at the last hyphen boundary
// rather than mid-word), and strips any trailing hyphens. Idempotent: a
// slug already within the caps is returned unchanged.
func capSlug(slug string) string {
	if slug == "" {
		return ""
	}

	// Soft cap: keep the first slugWordCap words.
	words := strings.Split(slug, "-")
	if len(words) > slugWordCap {
		words = words[:slugWordCap]
	}
	capped := strings.Join(words, "-")

	// Hard cap: trim to slugMaxLen, preferring a hyphen boundary so no
	// partial word survives.
	if len(capped) > slugMaxLen {
		capped = capped[:slugMaxLen]
		if idx := strings.LastIndex(capped, "-"); idx > 0 {
			capped = capped[:idx]
		}
	}

	return strings.Trim(capped, "-")
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

// slugify lowercases s and collapses every run of non-alphanumeric
// characters to a single hyphen, trimming leading/trailing hyphens. The
// transform is deterministic so re-publishing the same plan overwrites
// the same file rather than littering the vault.
func slugify(s string) string {
	var b strings.Builder
	prevHyphen := false
	for _, r := range strings.ToLower(s) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevHyphen = false
		default:
			if !prevHyphen && b.Len() > 0 {
				b.WriteRune('-')
				prevHyphen = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
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
