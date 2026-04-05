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

// Store manages persistent storage and retrieval of plan documents.
//
// Plans are stored as markdown files with YAML frontmatter in the XDG
// data directory (~/.local/share/flowstate/plans/). Store handles
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
type Store struct {
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

// NewStore creates a new Store for the given data directory.
//
// The directory is created if it does not exist. If directory creation
// fails, the error is returned.
//
// Expected:
//   - dataDir is a valid filesystem path.
//
// Returns:
//   - *Store pointing to the data directory.
//   - error if directory creation fails.
//
// Side effects:
//   - Creates dataDir and any parent directories if needed.
func NewStore(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating plan store directory: %w", err)
	}
	return &Store{dataDir: dataDir}, nil
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
func (s *Store) Create(f File) error {
	filePath := filepath.Join(s.dataDir, f.ID+".md")

	fm := Frontmatter{
		ID:               f.ID,
		Title:            f.Title,
		Description:      f.Description,
		Status:           f.Status,
		CreatedAt:        f.CreatedAt,
		ValidationStatus: f.ValidationStatus,
		AttemptCount:     f.AttemptCount,
		Score:            f.Score,
		ValidationErrors: f.ValidationErrors,
	}

	frontmatterBytes, err := yaml.Marshal(fm)
	if err != nil {
		return fmt.Errorf("marshalling frontmatter: %w", err)
	}

	var body strings.Builder
	body.WriteString("---\n")
	body.Write(frontmatterBytes)
	body.WriteString("---\n\n")

	writeTLDRSection(&body, f.TLDR)
	writeContextSection(&body, f.Context)
	writeWorkObjectivesSection(&body, f.WorkObjectives)
	writeVerificationStrategySection(&body, f.VerificationStrategy)
	writeReviewsSection(&body, f.Reviews)

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
func (s *Store) List() ([]Summary, error) {
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
func (s *Store) Get(id string) (*File, error) {
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

	sections, taskMarkdown := parsePlanBody(parts[2])
	tasks := parseTasksFromMarkdown(taskMarkdown)

	return &File{
		ID:                   fm.ID,
		Title:                fm.Title,
		Description:          fm.Description,
		Status:               fm.Status,
		CreatedAt:            fm.CreatedAt,
		Tasks:                tasks,
		TLDR:                 sections.TLDR,
		Context:              sections.Context,
		WorkObjectives:       sections.WorkObjectives,
		VerificationStrategy: sections.VerificationStrategy,
		Reviews:              sections.Reviews,
		ValidationStatus:     fm.ValidationStatus,
		AttemptCount:         fm.AttemptCount,
		Score:                fm.Score,
		ValidationErrors:     fm.ValidationErrors,
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
func (s *Store) Delete(id string) error {
	filePath := filepath.Join(s.dataDir, id+".md")

	if _, err := os.Stat(filePath); err != nil {
		return fmt.Errorf("plan not found: %s", id)
	}

	if err := os.Remove(filePath); err != nil {
		return fmt.Errorf("deleting plan file: %w", err)
	}

	return nil
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
// writeTasksToMarkdown writes tasks into markdown form.
//
// Expected:
//   - body is a valid builder.
//   - tasks contains zero or more tasks.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Appends task markdown to body.
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
	}
}

// parsedPlanBody holds the structured components extracted from plan body markdown.
//
// Expected:
//   - Fields are populated during parsing.
//
// Returns:
//   - (nothing; type definition only)
//
// Side effects:
//   - None.
type parsedPlanBody struct {
	TLDR                 string
	Context              SourceContext
	WorkObjectives       WorkObjectives
	VerificationStrategy string
	Reviews              []ReviewResult
}

// parsePlanBody splits plan body markdown into structured content.
//
// Expected:
//   - markdown contains plan body text.
//
// Returns:
//   - The parsed plan body and remaining task markdown.
//
// Side effects:
//   - None.
func parsePlanBody(markdown string) (parsedPlanBody, string) {
	if strings.TrimSpace(markdown) == "" {
		return parsedPlanBody{}, ""
	}

	lines := strings.Split(markdown, "\n")
	var taskLines strings.Builder
	parsed := parsedPlanBody{}
	state := planBodyState{}

	for _, line := range lines {
		if handlePlanSectionHeader(line, &state, &parsed, &taskLines) {
			continue
		}

		if state.section == "" {
			appendTaskLine(&taskLines, line)
			continue
		}

		if handlePlanSubsectionHeader(line, &state) {
			continue
		}

		handlePlanSectionContent(line, &state, &parsed)
	}

	if state.currentReview != nil {
		finaliseReview(&state)
		parsed.Reviews = append(parsed.Reviews, *state.currentReview)
	}

	return parsed, taskLines.String()
}

// planBodyState tracks the current position while parsing a plan body.
//
// Expected:
//   - Fields remain zero-valued unless parsing is in progress.
//
// Returns:
//   - (nothing; type definition only)
//
// Side effects:
//   - None.
type planBodyState struct {
	section       string
	subsection    string
	currentReview *ReviewResult
	reviewField   string
	reviewContent strings.Builder
}

// handlePlanSectionHeader processes a plan section heading.
//
// Expected:
//   - line contains a heading.
//
// Returns:
//   - true when the line was handled.
//
// Side effects:
//   - Mutates parsing state and accumulators.
func handlePlanSectionHeader(line string, state *planBodyState, parsed *parsedPlanBody, taskLines *strings.Builder) bool {
	if !isLevelTwoHeader(line) {
		return false
	}

	if state.currentReview != nil {
		finaliseReview(state)
		parsed.Reviews = append(parsed.Reviews, *state.currentReview)
		state.currentReview = nil
	}

	sectionName := strings.TrimSpace(strings.TrimPrefix(line, "## "))
	if isPlanSectionName(sectionName) {
		state.section = sectionName
		state.subsection = ""
		return true
	}

	state.section = ""
	state.subsection = ""
	appendTaskLine(taskLines, line)
	return true
}

// handlePlanSubsectionHeader processes a plan subsection heading.
//
// Expected:
//   - line contains a heading.
//
// Returns:
//   - true when the line was handled.
//
// Side effects:
//   - Mutates parsing state.
func handlePlanSubsectionHeader(line string, state *planBodyState) bool {
	if !isLevelThreeHeader(line) {
		return false
	}

	state.subsection = strings.TrimSpace(strings.TrimPrefix(line, "### "))
	if state.section == "Reviews" {
		finaliseReview(state)
		state.currentReview = &ReviewResult{}
		state.reviewField = ""
		state.reviewContent.Reset()
	}
	return true
}

// handlePlanSectionContent routes content into the current plan section.
//
// Expected:
//   - line contains section content.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Mutates the parsed plan body.
func handlePlanSectionContent(line string, state *planBodyState, parsed *parsedPlanBody) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return
	}

	switch state.section {
	case "TL;DR":
		appendTextSection(&parsed.TLDR, trimmed)
	case "Context":
		handleContextContent(parsed, state.subsection, trimmed)
	case "Work Objectives":
		handleWorkObjectivesContent(parsed, state.subsection, line)
	case "Verification Strategy":
		appendTextSection(&parsed.VerificationStrategy, trimmed)
	case "Reviews":
		handleReviewsContent(state, line, trimmed)
	}
}

// handleContextContent stores content for the plan context section.
//
// Expected:
//   - trimmed contains a non-empty line.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Mutates parsed context fields.
func handleContextContent(parsed *parsedPlanBody, subsection, trimmed string) {
	switch subsection {
	case "Original Request":
		appendTextSection(&parsed.Context.OriginalRequest, trimmed)
	case "Interview Summary":
		appendTextSection(&parsed.Context.InterviewSummary, trimmed)
	case "Research Findings":
		appendTextSection(&parsed.Context.ResearchFindings, trimmed)
	}
}

// handleWorkObjectivesContent stores content for the work objectives section.
//
// Expected:
//   - line contains section content.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Mutates parsed work objectives fields.
func handleWorkObjectivesContent(parsed *parsedPlanBody, subsection, line string) {
	switch subsection {
	case "Core Objective":
		appendTextSection(&parsed.WorkObjectives.CoreObjective, strings.TrimSpace(line))
	case "Deliverables":
		appendSectionListItem(&parsed.WorkObjectives.Deliverables, line)
	case "Definition of Done":
		appendSectionListItem(&parsed.WorkObjectives.DefinitionOfDone, line)
	case "Must Have":
		appendSectionListItem(&parsed.WorkObjectives.MustHave, line)
	case "Must Not Have":
		appendSectionListItem(&parsed.WorkObjectives.MustNotHave, line)
	}
}

// handleReviewsContent stores content for the reviews section.
//
// Expected:
//   - line contains section content.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Mutates review parsing state.
func handleReviewsContent(state *planBodyState, line, trimmed string) {
	if state.currentReview == nil {
		return
	}

	switch {
	case strings.HasPrefix(line, "**Verdict**:"):
		finaliseReview(state)
		state.reviewField = "verdict"
		state.reviewContent.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "**Verdict**:")))
	case strings.HasPrefix(line, "**Confidence**:"):
		finaliseReview(state)
		state.reviewField = "confidence"
		state.reviewContent.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "**Confidence**:")))
	case strings.HasPrefix(line, "**Blocking Issues**:"):
		finaliseReview(state)
		state.reviewField = "blocking_issues"
	case strings.HasPrefix(line, "**Suggestions**:"):
		finaliseReview(state)
		state.reviewField = "suggestions"
	case strings.HasPrefix(line, "- "):
		if state.reviewField == "blocking_issues" || state.reviewField == "suggestions" {
			state.reviewContent.WriteString(strings.TrimPrefix(line, "- "))
			state.reviewContent.WriteString("\n")
		}
	case trimmed != "" && state.reviewField != "":
		state.reviewContent.WriteString(trimmed)
		state.reviewContent.WriteString("\n")
	}
}

// finaliseReview appends the current review to the parsed plan.
//
// Expected:
//   - state contains a partially built review.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Mutates parsed reviews.
func finaliseReview(state *planBodyState) {
	if state.currentReview == nil {
		return
	}

	switch state.reviewField {
	case "verdict":
		state.currentReview.Verdict = strings.TrimSpace(state.reviewContent.String())
	case "confidence":
		if confidence, err := strconv.ParseFloat(strings.TrimSpace(state.reviewContent.String()), 64); err == nil {
			state.currentReview.Confidence = confidence
		}
	case "blocking_issues":
		state.currentReview.BlockingIssues = append(state.currentReview.BlockingIssues, splitSectionLines(state.reviewContent.String())...)
	case "suggestions":
		state.currentReview.Suggestions = append(state.currentReview.Suggestions, splitSectionLines(state.reviewContent.String())...)
	}

	state.reviewField = ""
	state.reviewContent.Reset()
}

// appendTaskLine appends a raw task line to the task buffer.
//
// Expected:
//   - line contains task markdown.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Appends to taskLines.
func appendTaskLine(taskLines *strings.Builder, line string) {
	taskLines.WriteString(line)
	taskLines.WriteString("\n")
}

// appendTextSection appends text to a multi-line section.
//
// Expected:
//   - target points to the accumulated text.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Mutates the target string.
func appendTextSection(target *string, value string) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return
	}
	if *target == "" {
		*target = trimmed
		return
	}
	*target += "\n" + trimmed
}

// isLevelTwoHeader reports whether a line is a level two heading.
//
// Expected:
//   - line contains a single line of text.
//
// Returns:
//   - true when the line is a level two heading.
//
// Side effects:
//   - None.
func isLevelTwoHeader(line string) bool {
	return strings.HasPrefix(line, "## ")
}

// isLevelThreeHeader reports whether a line is a level three heading.
//
// Expected:
//   - line contains a single line of text.
//
// Returns:
//   - true when the line is a level three heading.
//
// Side effects:
//   - None.
func isLevelThreeHeader(line string) bool {
	return strings.HasPrefix(line, "### ")
}

// isPlanSectionName reports whether a heading name is a recognised plan section.
//
// Expected:
//   - name contains a heading label.
//
// Returns:
//   - true when the name matches a plan section.
//
// Side effects:
//   - None.
func isPlanSectionName(name string) bool {
	switch name {
	case "TL;DR", "Context", "Work Objectives", "Verification Strategy", "Reviews":
		return true
	default:
		return false
	}
}

// splitSectionLines breaks section content into trimmed lines.
//
// Expected:
//   - content contains section text.
//
// Returns:
//   - The split lines.
//
// Side effects:
//   - None.
func splitSectionLines(content string) []string {
	lines := strings.Split(content, "\n")
	var values []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			values = append(values, trimmed)
		}
	}
	return values
}

// appendSectionListItem appends a list item to a section accumulator.
//
// Expected:
//   - target points to the destination slice.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Mutates the destination slice.
func appendSectionListItem(target *[]string, line string) {
	trimmed := strings.TrimSpace(strings.TrimPrefix(line, "- "))
	if trimmed == "" {
		return
	}
	*target = append(*target, trimmed)
}

// writeTLDRSection writes the TL;DR section when present.
//
// Expected:
//   - body is a valid builder.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Appends to body.
func writeTLDRSection(body *strings.Builder, tldr string) {
	if strings.TrimSpace(tldr) == "" {
		return
	}
	body.WriteString("## TL;DR\n")
	body.WriteString(strings.TrimSpace(tldr))
	body.WriteString("\n\n")
}

// writeContextSection writes the context section when present.
//
// Expected:
//   - body is a valid builder.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Appends to body.
func writeContextSection(body *strings.Builder, context SourceContext) {
	if context == (SourceContext{}) {
		return
	}
	body.WriteString("## Context\n")
	writeTextSubsection(body, "Original Request", context.OriginalRequest)
	writeTextSubsection(body, "Interview Summary", context.InterviewSummary)
	writeTextSubsection(body, "Research Findings", context.ResearchFindings)
}

// writeWorkObjectivesSection writes the work objectives section when present.
//
// Expected:
//   - body is a valid builder.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Appends to body.
func writeWorkObjectivesSection(body *strings.Builder, objectives WorkObjectives) {
	if !hasWorkObjectivesData(objectives) {
		return
	}
	body.WriteString("## Work Objectives\n")
	writeTextSubsection(body, "Core Objective", objectives.CoreObjective)
	writeListSubsection(body, "Deliverables", objectives.Deliverables)
	writeListSubsection(body, "Definition of Done", objectives.DefinitionOfDone)
	writeListSubsection(body, "Must Have", objectives.MustHave)
	writeListSubsection(body, "Must Not Have", objectives.MustNotHave)
}

// writeVerificationStrategySection writes the verification strategy section when present.
//
// Expected:
//   - body is a valid builder.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Appends to body.
func writeVerificationStrategySection(body *strings.Builder, strategy string) {
	if strings.TrimSpace(strategy) == "" {
		return
	}
	body.WriteString("## Verification Strategy\n")
	body.WriteString(strings.TrimSpace(strategy))
	body.WriteString("\n\n")
}

// writeReviewsSection writes the reviews section when present.
//
// Expected:
//   - body is a valid builder.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Appends to body.
func writeReviewsSection(body *strings.Builder, reviews []ReviewResult) {
	if len(reviews) == 0 {
		return
	}
	body.WriteString("## Reviews\n")
	for i := range reviews {
		review := reviews[i]
		fmt.Fprintf(body, "### Review %d\n", i+1)
		fmt.Fprintf(body, "**Verdict**: %s\n\n", review.Verdict)
		fmt.Fprintf(body, "**Confidence**: %g\n\n", review.Confidence)
		writeLabelledList(body, "Blocking Issues", review.BlockingIssues)
		writeLabelledList(body, "Suggestions", review.Suggestions)
	}
}

// writeTextSubsection writes a text subsection when content is present.
//
// Expected:
//   - body is a valid builder.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Appends to body.
func writeTextSubsection(body *strings.Builder, title, content string) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return
	}
	fmt.Fprintf(body, "### %s\n", title)
	body.WriteString(trimmed)
	body.WriteString("\n\n")
}

// writeListSubsection writes a list subsection when items are present.
//
// Expected:
//   - body is a valid builder.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Appends to body.
func writeListSubsection(body *strings.Builder, title string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(body, "### %s\n", title)
	for _, item := range items {
		fmt.Fprintf(body, "- %s\n", item)
	}
	body.WriteString("\n")
}

// writeLabelledList writes a labelled list when items are present.
//
// Expected:
//   - body is a valid builder.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Appends to body.
func writeLabelledList(body *strings.Builder, title string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(body, "**%s**:\n", title)
	for _, item := range items {
		fmt.Fprintf(body, "- %s\n", item)
	}
	body.WriteString("\n")
}

// hasWorkObjectivesData reports whether the work objectives contain any meaningful data.
//
// Expected:
//   - objectives may contain zero or more populated fields.
//
// Returns:
//   - true when at least one work objective field is populated.
//   - false otherwise.
//
// Side effects:
//   - None.
func hasWorkObjectivesData(objectives WorkObjectives) bool {
	return strings.TrimSpace(objectives.CoreObjective) != "" ||
		len(objectives.Deliverables) > 0 ||
		len(objectives.DefinitionOfDone) > 0 ||
		len(objectives.MustHave) > 0 ||
		len(objectives.MustNotHave) > 0
}






