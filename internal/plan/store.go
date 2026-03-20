package plan

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
		ID:          f.ID,
		Title:       f.Title,
		Description: f.Description,
		Status:      f.Status,
		CreatedAt:   f.CreatedAt,
	}

	frontmatterBytes, err := yaml.Marshal(fm)
	if err != nil {
		return fmt.Errorf("marshalling frontmatter: %w", err)
	}

	var body strings.Builder
	body.WriteString("---\n")
	body.Write(frontmatterBytes)
	body.WriteString("---\n\n")

	for _, task := range f.Tasks {
		fmt.Fprintf(&body, "## %s\n", task.Title)
		if task.Description != "" {
			body.WriteString(task.Description + "\n\n")
		}

		if len(task.AcceptanceCriteria) > 0 {
			body.WriteString("### Acceptance Criteria\n")
			for _, criterion := range task.AcceptanceCriteria {
				fmt.Fprintf(&body, "- %s\n", criterion)
			}
			body.WriteString("\n")
		}

		if len(task.Skills) > 0 {
			fmt.Fprintf(&body, "**Skills**: %s\n\n", strings.Join(task.Skills, ", "))
		}
	}

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

	tasks := parseTasksFromMarkdown(parts[2])

	return &File{
		ID:          fm.ID,
		Title:       fm.Title,
		Description: fm.Description,
		Status:      fm.Status,
		CreatedAt:   fm.CreatedAt,
		Tasks:       tasks,
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
	return i - 1 // Adjust for outer loop increment
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
