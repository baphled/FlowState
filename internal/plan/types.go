package plan

import "time"

// File represents a complete plan document with YAML frontmatter and tasks.
//
// Plans are stored as markdown files with YAML frontmatter. The frontmatter
// contains metadata about the plan, and the body contains task descriptions
// in markdown format.
//
// Expected:
//   - ID must be non-empty
//   - Title must be non-empty
//   - Tasks may be empty for draft plans
//
// Returns:
//   - (nothing; type definition only)
//
// Side effects:
//   - None.
type File struct {
	ID               string    `json:"id" yaml:"id"`
	Title            string    `json:"title" yaml:"title"`
	Description      string    `json:"description" yaml:"description"`
	Status           string    `json:"status" yaml:"status"`
	CreatedAt        time.Time `json:"created_at" yaml:"created_at"`
	Tasks            []Task    `json:"tasks" yaml:"tasks"`
	ValidationStatus string    `json:"validation_status,omitempty" yaml:"validation_status,omitempty"`
	AttemptCount     int       `json:"attempt_count,omitempty" yaml:"attempt_count,omitempty"`
	Score            float64   `json:"score,omitempty" yaml:"score,omitempty"`
	ValidationErrors []string  `json:"validation_errors,omitempty" yaml:"validation_errors,omitempty"`
}

// Task represents a single task within a plan.
//
// Tasks are organized within a plan and include acceptance criteria
// to help guide execution. Tasks may have associated skills that
// guide agent selection and capability loading.
//
// Expected:
//   - Title must be non-empty
//   - AcceptanceCriteria and Skills may be empty
//
// Returns:
//   - (nothing; type definition only)
//
// Side effects:
//   - None.
type Task struct {
	Title              string   `json:"title" yaml:"title"`
	Description        string   `json:"description" yaml:"description"`
	Status             string   `json:"status" yaml:"status"`
	AcceptanceCriteria []string `json:"acceptance_criteria" yaml:"acceptance_criteria"`
	Skills             []string `json:"skills" yaml:"skills"`
	Category           string   `json:"category" yaml:"category"`
	Dependencies       []string `json:"dependencies,omitempty" yaml:"dependencies,omitempty"`
	EstimatedEffort    string   `json:"estimated_effort,omitempty" yaml:"estimated_effort,omitempty"`
	Wave               int      `json:"wave,omitempty" yaml:"wave,omitempty"`
}

// Frontmatter represents the YAML frontmatter of a plan markdown file.
//
// This struct is used for parsing the frontmatter section only, before
// the task content begins.
//
// Expected:
//   - All fields optional
//
// Returns:
//   - (nothing; type definition only)
//
// Side effects:
//   - None.
type Frontmatter struct {
	ID               string    `yaml:"id"`
	Title            string    `yaml:"title"`
	Description      string    `yaml:"description"`
	Status           string    `yaml:"status"`
	CreatedAt        time.Time `yaml:"created_at"`
	ValidationStatus string    `yaml:"validation_status,omitempty"`
	AttemptCount     int       `yaml:"attempt_count,omitempty"`
	Score            float64   `yaml:"score,omitempty"`
	ValidationErrors []string  `yaml:"validation_errors,omitempty"`
}
