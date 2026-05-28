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
// approved plan body under, resolved as "<chainID>/plan". The
// deterministic publisher suffix-scans for this key because the
// post-swarm dispatch path does not thread the lead's free-form chainID
// (runSwarmGates leaves GateArgs.ChainID empty) — the same fallback the
// honesty gate uses.
const planSuffix = "plan"

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
//   - Suffix-scans the store for the first "*/plan" key, derives the
//     concrete chainID from the key prefix, and reads the plan body.
//     The post-swarm dispatch path does not thread the lead's free-form
//     chainID, so resolution mirrors the honesty gate's suffix-scan.
//   - When a "<chainID>/review" record is present AND parseable, the
//     verdict MUST be "approve" to publish; a non-approve verdict is a
//     no-op (no file, no record). An absent/malformed review does NOT
//     block publication — the plan-reviewer post-member gate already ran.
//   - Parses the plan as the {"markdown": ...} envelope when it is that
//     JSON shape; otherwise treats the raw value as the plan body.
//   - Derives a deterministic, idempotent filename: the envelope title,
//     else the body's first "# H1" heading, else the chainID; slugified
//     to a safe filename. The same plan always writes the same file
//     (overwrite-safe).
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
func PublishPlanToVault(store coordination.Store, outputDir string) (string, error) {
	if store == nil {
		return "", errors.New("publish plan: coordination store unavailable")
	}
	if strings.TrimSpace(outputDir) == "" {
		// No configured target: a no-op rather than writing to an
		// unknown/uncontainable location. The honesty gate's plan-key
		// check still applies independently.
		return "", nil
	}

	chainID, planRaw, found, err := scanForSuffix(store, planSuffix)
	if err != nil {
		return "", fmt.Errorf("publish plan: scanning coord-store for plan: %w", err)
	}
	if !found || strings.TrimSpace(string(planRaw)) == "" {
		// Nothing to publish — honest no-op, no fabricated record.
		return "", nil
	}

	if approved, ok := reviewApproves(store, chainID); ok && !approved {
		// The reviewer explicitly did not approve; do not publish a
		// rejected plan to the vault.
		return "", nil
	}

	title, body := parsePlan(planRaw)
	if strings.TrimSpace(body) == "" {
		return "", nil
	}

	slug := slugifyPlanName(title, body, chainID)
	vaultPath := filepath.Join(outputDir, slug+".md")

	if err := atomicWriteFile(vaultPath, []byte(body)); err != nil {
		return "", fmt.Errorf("publish plan: writing vault file %q: %w", vaultPath, err)
	}

	if err := recordPublication(store, chainID, vaultPath); err != nil {
		return "", fmt.Errorf("publish plan: recording publication for %q: %w", chainID, err)
	}

	return vaultPath, nil
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

// parsePlan extracts a title and the markdown body from the coord-store
// plan value. When the value is the {"markdown": ...} envelope the
// envelope's Markdown is the body and Title the title; otherwise the raw
// value is the body and the title is left empty (slugifyPlanName then
// derives one from the H1 or chainID).
func parsePlan(raw []byte) (title, body string) {
	var env planEnvelope
	if err := json.Unmarshal(raw, &env); err == nil && strings.TrimSpace(env.Markdown) != "" {
		return env.Title, env.Markdown
	}
	return "", string(raw)
}

// slugifyPlanName derives a deterministic, filesystem-safe slug for the
// plan file. Preference order: explicit title, the body's first "# H1"
// heading, then the chainID. The result is lowercased, spaces and unsafe
// characters collapse to single hyphens, and path separators are
// stripped — the same plan always yields the same slug (idempotent
// overwrite).
func slugifyPlanName(title, body, chainID string) string {
	name := strings.TrimSpace(title)
	if name == "" {
		name = firstH1(body)
	}
	if name == "" {
		name = chainID
	}
	slug := slugify(name)
	if slug == "" {
		slug = slugify(chainID)
	}
	if slug == "" {
		slug = "plan"
	}
	return slug
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
