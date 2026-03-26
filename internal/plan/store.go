package plan

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// PlanStore manages persistent storage and retrieval of plan documents.
//
// Plans are stored as markdown files with YAML frontmatter in the XDG
// data directory (~/.local/share/flowstate/plans/). PlanStore handles
// all file I/O, YAML parsing, and directory creation.
//
// Expected:
//   - dataDir points to an existing or creatable directory.
//   - All plan files are valid markdown with YAML frontmatter.
//
// Returns:
//   - (nothing; type definition only)
//
// Side effects:
//   - None (methods have individual side effects).
//
//nolint:revive // PlanStore name is intentional (not redundant; distinguishes from generic Store)
type PlanStore struct {
	dataDir string
}

// Summary contains metadata about a plan without loading the full document.
//
// Summary is typically used when listing available plans to the user.
// It contains only YAML frontmatter information, not task descriptions
// or the markdown body.
//
// Expected:
//   - ID is non-empty.
//   - CreatedAt is a valid time.
//
// Returns:
//   - (nothing; type definition only)
//
// Side effects:
//   - None.
type Summary struct {
	ID        string
	Title     string
	Status    string
	CreatedAt time.Time
}

// NewPlanStore creates a new PlanStore for the given data directory.
//
// The directory is created if it does not exist. If directory creation
// fails, the error is returned.
//
// Expected:
//   - dataDir is a valid filesystem path.
//
// Returns:
//   - *PlanStore pointing to the data directory.
//   - error if directory creation fails.
//
// Side effects:
//   - Creates dataDir and any parent directories if needed.
func NewPlanStore(dataDir string) (*PlanStore, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating plan store directory: %w", err)
	}
	return &PlanStore{dataDir: dataDir}, nil
}

// Create writes a plan file to the data directory.
//
// The file is named {dataDir}/{f.ID}.md. Existing files with the same
// ID are silently overwritten. The file is written with YAML frontmatter
// followed by the markdown body.
//
// Expected:
//   - f.ID is non-empty and filename-safe (no slashes, etc.).
//
// Returns:
//   - error if file write fails.
//
// Side effects:
//   - Writes {dataDir}/{f.ID}.md to disk.
func (s *PlanStore) Create(f File) error {
	filePath := filepath.Join(s.dataDir, f.ID+".md")

	fm := Frontmatter{
		ID:                   f.ID,
		Title:                f.Title,
		Description:          f.Description,
		Status:               f.Status,
		CreatedAt:            f.CreatedAt,
		ValidationStatus:     f.ValidationStatus,
		AttemptCount:         f.AttemptCount,
		Score:                f.Score,
		ValidationErrors:     f.ValidationErrors,
		TLDR:                 f.TLDR,
		VerificationStrategy: f.VerificationStrategy,
	}

	frontmatterBytes, err := yaml.Marshal(fm)
	if err != nil {
		return fmt.Errorf("marshalling frontmatter: %w", err)
	}

	var body strings.Builder
	body.WriteString("---\n")
	body.Write(frontmatterBytes)
	body.WriteString("---\n\n")

	writePlanSections(&body, f)

	writeTasksToMarkdown(&body, f.Tasks)

	if err := os.WriteFile(filePath, []byte(body.String()), 0o600); err != nil {
		return fmt.Errorf("writing plan file: %w", err)
	}

	return nil
}

// List returns summaries of all plans in the data directory.
//
// Only YAML frontmatter is parsed for each file; the markdown body
// is not read. Files are returned in alphabetical order by ID.
//
// Expected:
//   - dataDir exists and is readable.
//
// Returns:
//   - []Summary containing ID, Title, Status, CreatedAt for each plan.
//   - error if directory read fails.
//
// Side effects:
//   - Reads directory entries.
func (s *PlanStore) List() ([]Summary, error) {
	entries, err := os.ReadDir(s.dataDir)
	if err != nil {
		return nil, fmt.Errorf("reading plan directory: %w", err)
	}

	var summaries []Summary

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		filePath := filepath.Join(s.dataDir, entry.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}

		fm := Frontmatter{}
		parts := strings.SplitN(string(data), "---", 3)
		if len(parts) < 3 {
			continue
		}

		if err := yaml.Unmarshal([]byte(parts[1]), &fm); err != nil {
			continue
		}

		summaries = append(summaries, Summary{
			ID:        fm.ID,
			Title:     fm.Title,
			Status:    fm.Status,
			CreatedAt: fm.CreatedAt,
		})
	}

	return summaries, nil
}

// Get retrieves a complete plan from the data directory.
//
// The file {dataDir}/{id}.md is read and parsed. Both YAML frontmatter
// and markdown body are parsed and reconstructed into a File struct.
//
// Expected:
//   - id corresponds to an existing plan file (without .md extension).
//
// Returns:
//   - *File containing all metadata and tasks.
//   - error if file does not exist or cannot be parsed.
//
// Side effects:
//   - Reads {dataDir}/{id}.md from disk.
func (s *PlanStore) Get(id string) (*File, error) {
	filePath := filepath.Join(s.dataDir, id+".md")
	data, err := os.ReadFile(filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("plan not found: %s", id)
		}
		return nil, fmt.Errorf("reading plan file: %w", err)
	}

	parts := strings.SplitN(string(data), "---", 3)
	if len(parts) < 3 {
		return nil, errors.New("invalid plan file format: missing frontmatter delimiters")
	}

	fm := Frontmatter{}
	if err := yaml.Unmarshal([]byte(parts[1]), &fm); err != nil {
		return nil, fmt.Errorf("parsing frontmatter: %w", err)
	}

	body := parts[2]

	planSections, taskMarkdown := splitPlanSectionsAndTasks(body)
	ctx, wo, reviews := parsePlanSections(planSections)

	tasks := parseTasksFromMarkdown(taskMarkdown)

	return &File{
		ID:                   fm.ID,
		Title:                fm.Title,
		Description:          fm.Description,
		Status:               fm.Status,
		CreatedAt:            fm.CreatedAt,
		Tasks:                tasks,
		ValidationStatus:     fm.ValidationStatus,
		AttemptCount:         fm.AttemptCount,
		Score:                fm.Score,
		ValidationErrors:     fm.ValidationErrors,
		TLDR:                 fm.TLDR,
		VerificationStrategy: fm.VerificationStrategy,
		Context:              ctx,
		WorkObjectives:       wo,
		Reviews:              reviews,
	}, nil
}

// Delete removes a plan file from the data directory.
//
// If the file does not exist, an error is returned.
//
// Expected:
//   - id corresponds to an existing plan file.
//
// Returns:
//   - error if file does not exist or cannot be deleted.
//
// Side effects:
//   - Removes {dataDir}/{id}.md from disk.
func (s *PlanStore) Delete(id string) error {
	filePath := filepath.Join(s.dataDir, id+".md")

	if _, err := os.Stat(filePath); err != nil {
		return fmt.Errorf("plan not found: %s", id)
	}

	if err := os.Remove(filePath); err != nil {
		return fmt.Errorf("deleting plan file: %w", err)
	}

	return nil
}

// parseTasksFromMarkdown extracts tasks from the markdown body of a plan file.
//
// Tasks are identified by level-2 markdown headers (##). Following content
// is parsed for description, acceptance criteria (### Acceptance Criteria
// with bullet list), and skills (**Skills**: comma-separated list).
//
// Expected:
//   - markdown is the content after the second --- delimiter.
//   - tasks may be empty for draft plans.
//
// Returns:
//   - []Task containing extracted tasks, or empty slice if none found.
//
// Side effects:
//   - None.
//
//nolint:gocognit,revive,funlen // Cognitive complexity is inherent to the parsing logic
func parseTasksFromMarkdown(markdown string) []Task {
	if strings.TrimSpace(markdown) == "" {
		return []Task{}
	}

	var tasks []Task
	lines := strings.Split(markdown, "\n")

	var currentTask *Task

	for i := 0; i < len(lines); i++ {
		line := lines[i]

		if isTaskHeader(line) {
			if currentTask != nil {
				tasks = append(tasks, *currentTask)
			}
			currentTask = newTaskFromHeader(line)
			continue
		}

		if currentTask == nil {
			continue
		}

		if isAcceptanceCriteriaHeader(line) {
			i = parseAcceptanceCriteria(lines, i, currentTask)
			continue
		}

		if isSkillsLine(line) {
			parseSkills(line, currentTask)
			continue
		}

		if isDependenciesLine(line) {
			parseDependencies(line, currentTask)
			continue
		}

		if isEstimatedEffortLine(line) {
			parseEstimatedEffort(line, currentTask)
			continue
		}

		if isWaveLine(line) {
			parseWave(line, currentTask)
			continue
		}

		if isFileChangesLine(line) {
			i = parseFileChanges(lines, i, currentTask)
			continue
		}

		if isEvidenceLine(line) {
			parseEvidence(line, currentTask)
			continue
		}

		appendDescription(line, currentTask)
	}

	if currentTask != nil {
		tasks = append(tasks, *currentTask)
	}

	return tasks
}

// isTaskHeader checks if a line is a level-2 markdown header.
//
// Expected:
//   - line is a single line of text (no newlines).
//
// Returns:
//   - true if line starts with "## ", false otherwise.
//
// Side effects:
//   - None.
func isTaskHeader(line string) bool {
	return strings.HasPrefix(line, "## ")
}

// newTaskFromHeader creates a new Task with the title extracted from a header.
//
// Expected:
//   - line starts with "## " (checked by caller).
//
// Returns:
//   - *Task with Title field populated from header.
//
// Side effects:
//   - None.
func newTaskFromHeader(line string) *Task {
	title := strings.TrimPrefix(line, "## ")
	return &Task{
		Title: strings.TrimSpace(title),
	}
}

// isAcceptanceCriteriaHeader checks if a line is the acceptance criteria section header.
//
// Expected:
//   - line is a single line of text (no newlines).
//
// Returns:
//   - true if line starts with "### Acceptance Criteria", false otherwise.
//
// Side effects:
//   - None.
func isAcceptanceCriteriaHeader(line string) bool {
	return strings.HasPrefix(line, "### Acceptance Criteria")
}

// parseAcceptanceCriteria extracts acceptance criteria bullet points and returns the updated line index.
//
// Expected:
//   - startIdx points to the acceptance criteria header.
//   - task is non-nil.
//
// Returns:
//   - updated line index for the outer loop to continue from.
//
// Side effects:
//   - Modifies task.AcceptanceCriteria to include parsed criteria.
func parseAcceptanceCriteria(lines []string, startIdx int, task *Task) int {
	i := startIdx + 1
	for i < len(lines) {
		line := lines[i]
		if strings.HasPrefix(line, "- ") {
			criterion := strings.TrimPrefix(line, "- ")
			task.AcceptanceCriteria = append(task.AcceptanceCriteria, strings.TrimSpace(criterion))
			i++
		} else if shouldStopCriteriaParsing(line) {
			break
		} else {
			i++
		}
	}
	return i - 1
}

// shouldStopCriteriaParsing checks if we should stop parsing criteria.
//
// Expected:
//   - line is a single line of text (no newlines).
//
// Returns:
//   - true if line signals end of criteria section, false otherwise.
//
// Side effects:
//   - None.
func shouldStopCriteriaParsing(line string) bool {
	trimmed := strings.TrimSpace(line)
	return trimmed == "" || strings.HasPrefix(line, "**") || strings.HasPrefix(line, "##")
}

// isSkillsLine checks if a line is the skills line.
//
// Expected:
//   - line is a single line of text (no newlines).
//
// Returns:
//   - true if line starts with "**Skills**:", false otherwise.
//
// Side effects:
//   - None.
func isSkillsLine(line string) bool {
	return strings.HasPrefix(line, "**Skills**:")
}

// parseSkills extracts skills from a skills line and adds them to the task.
//
// Expected:
//   - line starts with "**Skills**:" (checked by caller).
//   - task is non-nil.
//
// Returns:
//   - (nothing; type void).
//
// Side effects:
//   - Modifies task.Skills to include parsed skill names.
func parseSkills(line string, task *Task) {
	skillsStr := strings.TrimPrefix(line, "**Skills**: ")
	skillsStr = strings.TrimSpace(skillsStr)
	if skillsStr != "" {
		skills := strings.Split(skillsStr, ", ")
		for _, skill := range skills {
			task.Skills = append(task.Skills, strings.TrimSpace(skill))
		}
	}
}

// appendDescription adds a line to the task's description if it's content.
//
// Expected:
//   - line is a single line of text (no newlines).
//   - task is non-nil.
//
// Returns:
//   - (nothing; type void).
//
// Side effects:
//   - Modifies task.Description to append the line if it's valid content.
func appendDescription(line string, task *Task) {
	trimmedLine := strings.TrimSpace(line)
	if trimmedLine == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "**") {
		return
	}

	if task.Description != "" {
		task.Description += "\n" + trimmedLine
	} else {
		task.Description = trimmedLine
	}
}

// writeTasksToMarkdown writes tasks to the markdown body with all metadata fields.
//
// Expected:
//   - body is a non-nil strings.Builder.
//   - tasks may be empty.
//
// Returns:
//   - (nothing; type void).
//
// Side effects:
//   - Writes task markdown to body.
//
//nolint:gocognit,revive // Cognitive complexity is inherent to the writing logic
func writeTasksToMarkdown(body *strings.Builder, tasks []Task) {
	for i := range tasks {
		task := &tasks[i]
		fmt.Fprintf(body, "## %s\n", task.Title)
		if task.Description != "" {
			body.WriteString(task.Description + "\n\n")
		}

		if len(task.AcceptanceCriteria) > 0 {
			body.WriteString("### Acceptance Criteria\n")
			for _, criterion := range task.AcceptanceCriteria {
				fmt.Fprintf(body, "- %s\n", criterion)
			}
			body.WriteString("\n")
		}

		if len(task.Skills) > 0 {
			fmt.Fprintf(body, "**Skills**: %s\n\n", strings.Join(task.Skills, ", "))
		}

		if len(task.Dependencies) > 0 {
			fmt.Fprintf(body, "**Dependencies**: %s\n\n", strings.Join(task.Dependencies, ", "))
		}

		if task.EstimatedEffort != "" {
			fmt.Fprintf(body, "**Estimated Effort**: %s\n\n", task.EstimatedEffort)
		}

		if task.Wave > 0 {
			fmt.Fprintf(body, "**Wave**: %d\n\n", task.Wave)
		}

		if len(task.FileChanges) > 0 {
			body.WriteString("**File Changes**:\n")
			for _, fc := range task.FileChanges {
				fmt.Fprintf(body, "- %s\n", fc)
			}
			body.WriteString("\n")
		}

		if task.Evidence != "" {
			fmt.Fprintf(body, "**Evidence**: %s\n\n", task.Evidence)
		}
	}
}

// isDependenciesLine checks if a line is the dependencies line.
//
// Expected:
//   - line is a single line of text (no newlines).
//
// Returns:
//   - true if line starts with "**Dependencies**:", false otherwise.
//
// Side effects:
//   - None.
func isDependenciesLine(line string) bool {
	return strings.HasPrefix(line, "**Dependencies**:")
}

// parseDependencies extracts dependencies from a dependencies line and adds them to the task.
//
// Expected:
//   - line starts with "**Dependencies**:" (checked by caller).
//   - task is non-nil.
//
// Returns:
//   - (nothing; type void).
//
// Side effects:
//   - Modifies task.Dependencies to include parsed dependency names.
func parseDependencies(line string, task *Task) {
	depsStr := strings.TrimPrefix(line, "**Dependencies**: ")
	depsStr = strings.TrimSpace(depsStr)
	if depsStr != "" {
		deps := strings.Split(depsStr, ", ")
		for _, dep := range deps {
			task.Dependencies = append(task.Dependencies, strings.TrimSpace(dep))
		}
	}
}

// isEstimatedEffortLine checks if a line is the estimated effort line.
//
// Expected:
//   - line is a single line of text (no newlines).
//
// Returns:
//   - true if line starts with "**Estimated Effort**:", false otherwise.
//
// Side effects:
//   - None.
func isEstimatedEffortLine(line string) bool {
	return strings.HasPrefix(line, "**Estimated Effort**:")
}

// parseEstimatedEffort extracts estimated effort from a line and sets it on the task.
//
// Expected:
//   - line starts with "**Estimated Effort**:" (checked by caller).
//   - task is non-nil.
//
// Returns:
//   - (nothing; type void).
//
// Side effects:
//   - Modifies task.EstimatedEffort to the parsed value.
func parseEstimatedEffort(line string, task *Task) {
	effortStr := strings.TrimPrefix(line, "**Estimated Effort**: ")
	task.EstimatedEffort = strings.TrimSpace(effortStr)
}

// isWaveLine checks if a line is the wave line.
//
// Expected:
//   - line is a single line of text (no newlines).
//
// Returns:
//   - true if line starts with "**Wave**:", false otherwise.
//
// Side effects:
//   - None.
func isWaveLine(line string) bool {
	return strings.HasPrefix(line, "**Wave**:")
}

// parseWave extracts wave number from a line and sets it on the task.
//
// Expected:
//   - line starts with "**Wave**:" (checked by caller).
//   - task is non-nil.
//
// Returns:
//   - (nothing; type void).
//
// Side effects:
//   - Modifies task.Wave to the parsed integer value; ignores parse errors.
func parseWave(line string, task *Task) {
	waveStr := strings.TrimPrefix(line, "**Wave**: ")
	waveStr = strings.TrimSpace(waveStr)
	if waveStr != "" {
		if wave, err := strconv.Atoi(waveStr); err == nil {
			task.Wave = wave
		}
	}
}

// isFileChangesLine checks if a line is the file changes line.
//
// Expected:
//   - line is a single line of text (no newlines).
//
// Returns:
//   - true if line starts with "**File Changes**:", false otherwise.
//
// Side effects:
//   - None.
func isFileChangesLine(line string) bool {
	return strings.HasPrefix(line, "**File Changes**:")
}

// parseFileChanges extracts file changes from a file changes section.
//
// Expected:
//   - startIdx points to the file changes header.
//   - task is non-nil.
//
// Returns:
//   - updated line index for the outer loop to continue from.
//
// Side effects:
//   - Modifies task.FileChanges to include parsed file paths.
func parseFileChanges(lines []string, startIdx int, task *Task) int {
	i := startIdx + 1
	for i < len(lines) {
		line := lines[i]
		if strings.HasPrefix(line, "- ") {
			fc := strings.TrimPrefix(line, "- ")
			task.FileChanges = append(task.FileChanges, strings.TrimSpace(fc))
			i++
		} else if shouldStopCriteriaParsing(line) {
			break
		} else {
			i++
		}
	}
	return i - 1
}

// isEvidenceLine checks if a line is the evidence line.
//
// Expected:
//   - line is a single line of text (no newlines).
//
// Returns:
//   - true if line starts with "**Evidence**:", false otherwise.
//
// Side effects:
//   - None.
func isEvidenceLine(line string) bool {
	return strings.HasPrefix(line, "**Evidence**:")
}

// parseEvidence extracts evidence from a line and sets it on the task.
//
// Expected:
//   - line starts with "**Evidence**:" (checked by caller).
//   - task is non-nil.
//
// Returns:
//   - (nothing; type void).
//
// Side effects:
//   - Modifies task.Evidence to the parsed value.
func parseEvidence(line string, task *Task) {
	evidenceStr := strings.TrimPrefix(line, "**Evidence**: ")
	task.Evidence = strings.TrimSpace(evidenceStr)
}

// writePlanSections writes the OMO-style plan sections to the markdown body.
//
// Expected:
//   - body is a non-nil strings.Builder.
//   - f contains the plan with optional sections.
//
// Returns:
//   - (nothing; type void).
//
// Side effects:
//   - Writes section markdown to body.
//
//nolint:gocognit,nestif,gocyclo,revive,funlen // Necessary complexity for handling multiple section types
func writePlanSections(body *strings.Builder, f File) {
	hasContext := f.Context.OriginalRequest != "" || f.Context.InterviewSummary != "" || f.Context.ResearchFindings != ""
	hasWorkObjectives := f.WorkObjectives.CoreObjective != "" || len(f.WorkObjectives.Deliverables) > 0 ||
		len(f.WorkObjectives.DefinitionOfDone) > 0 || len(f.WorkObjectives.MustHave) > 0 || len(f.WorkObjectives.MustNotHave) > 0
	hasReviews := len(f.Reviews) > 0

	if !hasContext && !hasWorkObjectives && !hasReviews {
		return
	}

	if hasContext {
		body.WriteString("## Context\n\n")
		if f.Context.OriginalRequest != "" {
			fmt.Fprintf(body, "**Original Request**: %s\n\n", f.Context.OriginalRequest)
		}
		if f.Context.InterviewSummary != "" {
			fmt.Fprintf(body, "**Interview Summary**: %s\n\n", f.Context.InterviewSummary)
		}
		if f.Context.ResearchFindings != "" {
			fmt.Fprintf(body, "**Research Findings**: %s\n\n", f.Context.ResearchFindings)
		}
	}

	if hasWorkObjectives {
		body.WriteString("## Work Objectives\n\n")
		if f.WorkObjectives.CoreObjective != "" {
			fmt.Fprintf(body, "**Core Objective**: %s\n\n", f.WorkObjectives.CoreObjective)
		}
		if len(f.WorkObjectives.Deliverables) > 0 {
			body.WriteString("**Deliverables**:\n")
			for _, d := range f.WorkObjectives.Deliverables {
				fmt.Fprintf(body, "- %s\n", d)
			}
			body.WriteString("\n")
		}
		if len(f.WorkObjectives.DefinitionOfDone) > 0 {
			body.WriteString("**Definition of Done**:\n")
			for _, d := range f.WorkObjectives.DefinitionOfDone {
				fmt.Fprintf(body, "- %s\n", d)
			}
			body.WriteString("\n")
		}
		if len(f.WorkObjectives.MustHave) > 0 {
			body.WriteString("**Must Have**:\n")
			for _, m := range f.WorkObjectives.MustHave {
				fmt.Fprintf(body, "- %s\n", m)
			}
			body.WriteString("\n")
		}
		if len(f.WorkObjectives.MustNotHave) > 0 {
			body.WriteString("**Must Not Have**:\n")
			for _, m := range f.WorkObjectives.MustNotHave {
				fmt.Fprintf(body, "- %s\n", m)
			}
			body.WriteString("\n")
		}
	}

	if hasReviews {
		body.WriteString("## Reviews\n\n")
		for _, r := range f.Reviews {
			if r.Reviewer != "" {
				fmt.Fprintf(body, "**Reviewer**: %s\n\n", r.Reviewer)
			}
			if r.Verdict != "" {
				fmt.Fprintf(body, "**Verdict**: %s\n\n", r.Verdict)
			}
			if r.Confidence > 0 {
				fmt.Fprintf(body, "**Confidence**: %.2f\n\n", r.Confidence)
			}
			if len(r.Issues) > 0 {
				body.WriteString("**Issues**:\n")
				for _, i := range r.Issues {
					fmt.Fprintf(body, "- %s\n", i)
				}
				body.WriteString("\n")
			}
			if len(r.Suggestions) > 0 {
				body.WriteString("**Suggestions**:\n")
				for _, s := range r.Suggestions {
					fmt.Fprintf(body, "- %s\n", s)
				}
				body.WriteString("\n")
			}
		}
	}
}

// splitPlanSectionsAndTasks separates the plan sections from the tasks section.
//
// Expected:
//   - markdown is the content after the frontmatter delimiter.
//
// Returns:
//   - planSections: content before ## Tasks
//   - taskMarkdown: content starting from ## Tasks (or empty if no tasks)
//
// Side effects:
//   - None.
func splitPlanSectionsAndTasks(markdown string) (string, string) {
	lines := strings.Split(markdown, "\n")

	for i, line := range lines {
		if strings.HasPrefix(line, "## ") && strings.TrimPrefix(line, "## ") != "Context" &&
			strings.TrimPrefix(line, "## ") != "Work Objectives" && strings.TrimPrefix(line, "## ") != "Reviews" {
			planSections := strings.Join(lines[:i], "\n")
			taskMarkdown := strings.Join(lines[i:], "\n")
			return planSections, taskMarkdown
		}
	}

	return markdown, ""
}

// parsePlanSections extracts OMO-style plan sections from the markdown body.
//
// Expected:
//   - markdown is the content after the frontmatter delimiter.
//   - May contain Context, Work Objectives, Reviews sections before ## Tasks.
//
// Returns:
//   - Context, WorkObjectives, Reviews extracted from markdown.
//
// Side effects:
//   - None.
//
//nolint:gocognit,nestif,gocyclo,revive,funlen // Necessary complexity for parsing multiple section types
func parsePlanSections(markdown string) (Context, WorkObjectives, []ReviewResult) {
	var ctx Context
	var wo WorkObjectives
	var reviews []ReviewResult

	if strings.TrimSpace(markdown) == "" {
		return ctx, wo, reviews
	}

	lines := strings.Split(markdown, "\n")

	var currentSection string
	var currentReview *ReviewResult

	//nolint:revive // Required to allow modifying loop variable in parseBulletList
	for i := 0; i < len(lines); i++ {
		line := lines[i]

		if strings.HasPrefix(line, "## Context") {
			currentSection = "context"
			continue
		}

		if strings.HasPrefix(line, "## Work Objectives") {
			currentSection = "workObjectives"
			continue
		}

		if strings.HasPrefix(line, "## Reviews") {
			currentSection = "reviews"
			continue
		}

		if strings.HasPrefix(line, "## ") && !strings.HasPrefix(line, "## Context") &&
			!strings.HasPrefix(line, "## Work Objectives") && !strings.HasPrefix(line, "## Reviews") {
			break
		}

		switch currentSection {
		case "context":
			if strings.HasPrefix(line, "**Original Request**:") {
				ctx.OriginalRequest = strings.TrimPrefix(line, "**Original Request**: ")
			}
			if strings.HasPrefix(line, "**Interview Summary**:") {
				ctx.InterviewSummary = strings.TrimPrefix(line, "**Interview Summary**: ")
			}
			if strings.HasPrefix(line, "**Research Findings**:") {
				ctx.ResearchFindings = strings.TrimPrefix(line, "**Research Findings**: ")
			}

		case "workObjectives":
			if strings.HasPrefix(line, "**Core Objective**:") {
				wo.CoreObjective = strings.TrimPrefix(line, "**Core Objective**: ")
			}
			if strings.HasPrefix(line, "**Deliverables**:") {
				wo.Deliverables, i = parseBulletList(lines, i)
			}
			if strings.HasPrefix(line, "**Definition of Done**:") {
				wo.DefinitionOfDone, i = parseBulletList(lines, i)
			}
			if strings.HasPrefix(line, "**Must Have**:") {
				wo.MustHave, i = parseBulletList(lines, i)
			}
			if strings.HasPrefix(line, "**Must Not Have**:") {
				wo.MustNotHave, i = parseBulletList(lines, i)
			}

		case "reviews":
			if strings.HasPrefix(line, "**Reviewer**:") {
				if currentReview != nil {
					reviews = append(reviews, *currentReview)
				}
				currentReview = &ReviewResult{
					Reviewer: strings.TrimPrefix(line, "**Reviewer**: "),
				}
			}
			if currentReview != nil {
				if strings.HasPrefix(line, "**Verdict**:") {
					currentReview.Verdict = strings.TrimPrefix(line, "**Verdict**: ")
				}
				if strings.HasPrefix(line, "**Confidence**:") {
					confStr := strings.TrimPrefix(line, "**Confidence**: ")
					if conf, err := strconv.ParseFloat(confStr, 64); err == nil {
						currentReview.Confidence = conf
					}
				}
				if strings.HasPrefix(line, "**Issues**:") {
					currentReview.Issues, i = parseBulletList(lines, i)
				}
				if strings.HasPrefix(line, "**Suggestions**:") {
					currentReview.Suggestions, i = parseBulletList(lines, i)
				}
			}
		}
	}

	if currentReview != nil {
		reviews = append(reviews, *currentReview)
	}

	return ctx, wo, reviews
}

// parseBulletList parses bullet list items from subsequent lines.
//
// Expected:
//   - lines is the full markdown lines.
//   - startIdx points to the line with the bullet list header.
//
// Returns:
//   - slice of bullet item strings.
//   - updated index to continue parsing from.
//
// Side effects:
//   - None.
func parseBulletList(lines []string, startIdx int) ([]string, int) {
	var items []string
	i := startIdx + 1

	for i < len(lines) {
		line := lines[i]
		if strings.HasPrefix(line, "- ") {
			items = append(items, strings.TrimPrefix(line, "- "))
			i++
		} else if strings.TrimSpace(line) == "" {
			i++
			continue
		} else {
			break
		}
	}

	return items, i - 1
}
