// Driver-prompt synthesiser for `flowstate autoresearch run`.
//
// Slice 1 of the Autoresearch Live Driver Integration plan (April 2026,
// vault commit `11ee9ed`, § 5.1). The synthesiser produces the per-trial
// prompt-text the live driver consumes; the harness writes the prompt
// to a temp file under the worktree and exposes the path to the driver
// subprocess via FLOWSTATE_AUTORESEARCH_PROMPT_FILE (see
// runDriverScript in autoresearch_loop.go).
//
// The prompt has four sections in fixed order, separated by ASCII
// headings — drivers parse against these markers:
//
//   # PROGRAM       — the program-of-record skill body, verbatim
//   # SURFACE       — the current surface path + full contents
//   # HISTORY       — the last N trial outcomes from the coord-store
//   # INSTRUCTION   — the terse "produce the next candidate edit" prose
//
// Determinism contract (plan § 2.4):
//
//   BuildDriverPrompt is deterministic given the same
//   (programBody, surfacePath, surfaceBytes, history, historyWindow)
//   inputs. No timestamps, no run-IDs, no random values are embedded
//   in the load-bearing prose — operators inspecting two consecutive
//   prompt files via the new `prompt_sha` coord-store field can detect
//   stuck-prompt failure modes (LD1 in plan § 6.2).
//
// Backward compatibility (plan § 4.4 R1.2):
//
//   The synthesiser does not touch existing coord-store fields;
//   per-trial PromptFile / PromptSHA additions are `omitempty` JSON
//   so older readers (predecessor-era records) decode unchanged.

package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// driverPromptHistoryDefault is the default --prompt-history-window value.
// Plan § 4.3 picks 5: enough trial trajectory for the driver to avoid
// repeating a failed angle, short enough to keep the prompt cost
// bounded.
const driverPromptHistoryDefault = 5

// driverPromptSection is one named section in the synthesised prompt.
// The four section names are pinned per plan § 5.1 R1.5; tests assert
// they appear once each in fixed order so a future refactor cannot
// silently shuffle the prompt and break drivers that key off section
// markers.
type driverPromptSection struct {
	Heading string
	Body    string
}

// Driver-prompt section markers — the driver-script contract (plan
// § 5.2) parses against these. Changing them is a breaking change for
// every driver script in the wild.
const (
	driverPromptHeadingProgram     = "# PROGRAM"
	driverPromptHeadingSurface     = "# SURFACE"
	driverPromptHeadingHistory     = "# HISTORY"
	driverPromptHeadingInstruction = "# INSTRUCTION"
)

// driverPromptInstruction is the terse final-section prose the
// synthesiser emits verbatim. The fenced ` ```surface ` block contract
// is what the reference shell driver in
// scripts/autoresearch-drivers/default-assistant-driver.sh parses
// against.
const driverPromptInstruction = `Propose ONE edit to the surface file shown above. Honour the off-limits constraints in the # PROGRAM section. Reply with the full updated surface contents inside a single fenced block tagged ` + "`surface`" + `:

` + "```surface" + `
<the full updated surface contents go here>
` + "```" + `

Do not include any prose outside the fenced block; the harness applies the block's contents verbatim as the new surface. If you cannot improve the surface under the program's constraints, reply with the surface unchanged inside the fenced block — the harness's fixed-point gate will record the no-op trial.`

// BuildDriverPrompt synthesises the per-trial driver prompt.
//
// Expected:
//   - programBody is the program-of-record skill body, read by the
//     harness from the manifest record's program_resolved path. May be
//     empty if the operator pointed --program at an empty file (the
//     harness logs a warning earlier; the synthesiser does not
//     re-validate).
//   - surfacePath is the surface path RELATIVE to the worktree root —
//     drivers see this string verbatim. Absolute paths leak the
//     worktree's runID into the prompt and break determinism.
//   - surfaceBytes is the current surface file contents. May be empty
//     for skill-body or source surfaces during early bootstrap; the
//     synthesiser still emits the # SURFACE section with an empty body.
//   - history is the slice of recent trial outcomes, oldest-first. The
//     synthesiser trims to the last historyWindow entries (or fewer if
//     fewer have run). Empty history (trial 1 of a run) yields the
//     literal "(no prior trials)" body.
//   - historyWindow caps the number of history entries rendered.
//     Non-positive values fall back to driverPromptHistoryDefault.
//
// Returns:
//   - The full prompt-text bytes ready to be written to the per-trial
//     prompt file.
//   - An error only on internal-format failures (none today; the
//     signature carries the hook for future caller-visible failures
//     like a malformed history record).
//
// Side effects: none.
func BuildDriverPrompt(
	programBody string,
	surfacePath string,
	surfaceBytes []byte,
	history []trialOutcome,
	historyWindow int,
) ([]byte, error) {
	if historyWindow <= 0 {
		historyWindow = driverPromptHistoryDefault
	}

	sections := []driverPromptSection{
		{Heading: driverPromptHeadingProgram, Body: renderProgramSection(programBody)},
		{Heading: driverPromptHeadingSurface, Body: renderSurfaceSection(surfacePath, surfaceBytes)},
		{Heading: driverPromptHeadingHistory, Body: renderHistorySection(history, historyWindow)},
		{Heading: driverPromptHeadingInstruction, Body: driverPromptInstruction},
	}

	var buf strings.Builder
	for i, section := range sections {
		if i > 0 {
			buf.WriteString("\n\n")
		}
		buf.WriteString(section.Heading)
		buf.WriteString("\n\n")
		buf.WriteString(section.Body)
	}
	// Trailing newline keeps the prompt-file POSIX-friendly.
	buf.WriteString("\n")
	return []byte(buf.String()), nil
}

// driverPromptSHA returns the SHA-256 of the synthesised prompt as a
// lower-case hex string. The harness records this on each trial record
// so operators can detect stuck-prompt patterns post-hoc (LD1 in plan
// § 6.2).
func driverPromptSHA(prompt []byte) string {
	sum := sha256.Sum256(prompt)
	return hex.EncodeToString(sum[:])
}

// renderProgramSection returns the program-of-record body. An empty
// program body yields a literal "(empty program body)" placeholder so
// drivers parsing the section can distinguish "no constraints" from a
// section that was skipped.
func renderProgramSection(body string) string {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return "(empty program body)"
	}
	return trimmed
}

// renderSurfaceSection emits the surface path on its own line followed
// by the file contents inside a fenced block. The fence tag is `text`
// rather than a language-specific tag because the harness must work
// for manifest (.md), skill-body (.md), and source (.go, .ts, etc)
// surfaces; drivers do not branch on tag, they branch on the content
// they generate.
func renderSurfaceSection(surfacePath string, surfaceBytes []byte) string {
	body := strings.TrimRight(string(surfaceBytes), "\n")
	return fmt.Sprintf("Path (relative to worktree): `%s`\n\n```text\n%s\n```",
		surfacePath, body)
}

// renderHistorySection renders the trial history as one bullet per
// trial, oldest-first. Per plan § 4.3 each line carries the trial
// number, score, kept flag, reason, and short candidate SHA so the
// driver sees the full ratchet trajectory. Empty history (the
// first-trial case) yields a literal "(no prior trials)" body.
func renderHistorySection(history []trialOutcome, window int) string {
	if len(history) == 0 {
		return "(no prior trials)"
	}
	if window > len(history) {
		window = len(history)
	}
	tail := history[len(history)-window:]

	var buf strings.Builder
	for i, outcome := range tail {
		if i > 0 {
			buf.WriteString("\n")
		}
		short := ""
		if outcome.CandidateSHA != "" {
			cut := outcome.CandidateSHA
			if len(cut) > 12 {
				cut = cut[:12]
			}
			short = cut
		}
		fmt.Fprintf(&buf,
			"- Trial %d: score=%g, kept=%t, reason=%s, candidate-sha=%s",
			outcome.N, outcome.Score, outcome.Kept, outcome.Reason, short,
		)
	}
	return buf.String()
}
