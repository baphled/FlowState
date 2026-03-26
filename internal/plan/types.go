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
	ID                   string         `json:"id" yaml:"id"`
	Title                string         `json:"title" yaml:"title"`
	Description          string         `json:"description" yaml:"description"`
	Status               string         `json:"status" yaml:"status"`
	CreatedAt            time.Time      `json:"created_at" yaml:"created_at"`
	Tasks                []Task         `json:"tasks" yaml:"tasks"`
	ValidationStatus     string         `json:"validation_status,omitempty" yaml:"validation_status,omitempty"`
	AttemptCount         int            `json:"attempt_count,omitempty" yaml:"attempt_count,omitempty"`
	Score                float64        `json:"score,omitempty" yaml:"score,omitempty"`
	ValidationErrors     []string       `json:"validation_errors,omitempty" yaml:"validation_errors,omitempty"`
	TLDR                 string         `json:"tldr,omitempty" yaml:"tldr,omitempty"`
	Context              Context        `json:"context,omitempty" yaml:"context,omitempty"`
	WorkObjectives       WorkObjectives `json:"work_objectives,omitempty" yaml:"work_objectives,omitempty"`
	VerificationStrategy string         `json:"verification_strategy,omitempty" yaml:"verification_strategy,omitempty"`
	Reviews              []ReviewResult `json:"reviews,omitempty" yaml:"reviews,omitempty"`
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
	FileChanges        []string `json:"file_changes,omitempty" yaml:"file_changes,omitempty"`
	Evidence           string   `json:"evidence,omitempty" yaml:"evidence,omitempty"`
}

// Context provides additional context about the origin and background of a plan.
//
// This struct captures information about where the plan came from, including
// the original request that triggered its creation, any interview or discussion
// summaries, and research findings that informed the plan's development.
//
// Expected:
//   - All fields optional (zero values = valid)
//
// Returns:
//   - (nothing; type definition only)
//
// Side effects:
//   - None.
type Context struct {
	OriginalRequest  string `json:"original_request,omitempty" yaml:"original_request,omitempty"`
	InterviewSummary string `json:"interview_summary,omitempty" yaml:"interview_summary,omitempty"`
	ResearchFindings string `json:"research_findings,omitempty" yaml:"research_findings,omitempty"`
}

// WorkObjectives defines the goals, deliverables, and acceptance criteria for a plan.
//
// This struct captures the core objective being pursued, what deliverables are
// expected, the definition of done for the work, and any explicit constraints
// about what must or must not be included.
//
// Expected:
//   - All fields optional (zero values = valid)
//
// Returns:
//   - (nothing; type definition only)
//
// Side effects:
//   - None.
type WorkObjectives struct {
	CoreObjective    string   `json:"core_objective,omitempty" yaml:"core_objective,omitempty"`
	Deliverables     []string `json:"deliverables,omitempty" yaml:"deliverables,omitempty"`
	DefinitionOfDone []string `json:"definition_of_done,omitempty" yaml:"definition_of_done,omitempty"`
	MustHave         []string `json:"must_have,omitempty" yaml:"must_have,omitempty"`
	MustNotHave      []string `json:"must_not_have,omitempty" yaml:"must_not_have,omitempty"`
}

// ReviewResult represents the outcome of a plan review.
//
// This struct captures who performed the review, the verdict reached, the
// reviewer's confidence level, any issues identified, and suggestions for
// improvement.
//
// Expected:
//   - All fields optional (zero values = valid)
//
// Returns:
//   - (nothing; type definition only)
//
// Side effects:
//   - None.
type ReviewResult struct {
	Reviewer    string   `json:"reviewer" yaml:"reviewer"`
	Verdict     string   `json:"verdict" yaml:"verdict"`
	Confidence  float64  `json:"confidence,omitempty" yaml:"confidence,omitempty"`
	Issues      []string `json:"issues,omitempty" yaml:"issues,omitempty"`
	Suggestions []string `json:"suggestions,omitempty" yaml:"suggestions,omitempty"`
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
	ID                   string    `yaml:"id"`
	Title                string    `yaml:"title"`
	Description          string    `yaml:"description"`
	Status               string    `yaml:"status"`
	CreatedAt            time.Time `yaml:"created_at"`
	ValidationStatus     string    `yaml:"validation_status,omitempty"`
	AttemptCount         int       `yaml:"attempt_count,omitempty"`
	Score                float64   `yaml:"score,omitempty"`
	ValidationErrors     []string  `yaml:"validation_errors,omitempty"`
	TLDR                 string    `yaml:"tldr,omitempty"`
	VerificationStrategy string    `yaml:"verification_strategy,omitempty"`
}
