package plan

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// parseTasksFromMarkdown extracts tasks from markdown content.
//
// Expected:
//   - markdown contains plan body text.
//
// Returns:
//   - The parsed tasks.
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
//
// isTaskHeader reports whether a line begins a task block.
//
// Expected:
//   - line contains a single line of text.
//
// Returns:
//   - true when the line is a task header.
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
//
// newTaskFromHeader creates a task from a header line.
//
// Expected:
//   - line is a task header.
//
// Returns:
//   - A task parsed from the header.
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
//
// isAcceptanceCriteriaHeader reports whether a line starts the acceptance criteria list.
//
// Expected:
//   - line contains a single line of text.
//
// Returns:
//   - true when the line is an acceptance criteria header.
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
//
// parseAcceptanceCriteria reads acceptance criteria lines into a task.
//
// Expected:
//   - lines contains plan body lines.
//   - task is the task being populated.
//
// Returns:
//   - The next line index to process.
//
// Side effects:
//   - Mutates task.AcceptanceCriteria.
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
//
// shouldStopCriteriaParsing reports whether criteria parsing should stop.
//
// Expected:
//   - line contains a single line of text.
//
// Returns:
//   - true when parsing should stop.
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
//
// isSkillsLine reports whether a line contains skills metadata.
//
// Expected:
//   - line contains a single line of text.
//
// Returns:
//   - true when the line holds skills.
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
//
// parseSkills extracts skills from a metadata line.
//
// Expected:
//   - line contains skills metadata.
//   - task is the task being populated.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Mutates task.Skills.
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
//
// appendDescription adds a description line to a task.
//
// Expected:
//   - line contains task body text.
//   - task is the task being populated.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Mutates task.Description.
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

// isDependenciesLine reports whether a line begins a dependencies entry.
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

// tasksFromPlanText extracts and normalises tasks from a plan's markdown body.
//
// Expected:
//   - planText contains a plan with YAML frontmatter delimiters.
//
// Returns:
//   - A slice of Task values parsed from the markdown body.
//
// Side effects:
//   - None.
func tasksFromPlanText(planText string) []Task {
	parts := strings.SplitN(planText, "---", 3)
	if len(parts) < 3 {
		return []Task{}
	}
	tasks := parseTasksFromMarkdown(parts[2])
	for i := range tasks {
		tasks[i].Dependencies = normalizeDependencies(tasks[i].Dependencies)
	}
	return tasks
}

// parseFile extracts and unmarshals YAML frontmatter from a plan text into a File struct.
//
// Expected:
//   - planText contains a plan with YAML frontmatter delimited by "---".
//
// Returns:
//   - A File struct populated from the YAML frontmatter.
//   - An error if the frontmatter is missing or cannot be unmarshalled.
//
// Side effects:
//   - None.

// parseFile extracts and unmarshals YAML frontmatter from a plan text into a File struct.
//
// Expected:
//   - planText contains a plan with YAML frontmatter delimited by "---".
//
// Returns:
//   - A File struct populated from the YAML frontmatter.
//   - An error if the frontmatter is missing or cannot be unmarshalled.
//
// Side effects:
//   - None.
func parseFile(planText string) (*File, error) {
	parts := strings.SplitN(planText, "---", 3)
	if len(parts) < 3 {
		return nil, errors.New("missing YAML frontmatter")
	}

	var file File
	if err := yaml.Unmarshal([]byte(parts[1]), &file); err != nil {
		return nil, fmt.Errorf("failed to unmarshal YAML: %w", err)
	}

	return &file, nil
}
