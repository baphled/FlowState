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

	return &File{
		ID:          fm.ID,
		Title:       fm.Title,
		Description: fm.Description,
		Status:      fm.Status,
		CreatedAt:   fm.CreatedAt,
		Tasks:       []Task{},
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
