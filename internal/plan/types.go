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
	TLDR                 string         `json:"tldr,omitempty" yaml:"tldr,omitempty"`
	Context              SourceContext  `json:"context" yaml:"context"`
	WorkObjectives       WorkObjectives `json:"work_objectives" yaml:"work_objectives"`
	VerificationStrategy string         `json:"verification_strategy,omitempty" yaml:"verification_strategy,omitempty"`
	Reviews              []ReviewResult `json:"reviews,omitempty" yaml:"reviews,omitempty"`
	ValidationStatus     string         `json:"validation_status,omitempty" yaml:"validation_status,omitempty"`
	AttemptCount         int            `json:"attempt_count,omitempty" yaml:"attempt_count,omitempty"`
	Score                float64        `json:"score,omitempty" yaml:"score,omitempty"`
	ValidationErrors     []string       `json:"validation_errors,omitempty" yaml:"validation_errors,omitempty"`
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
	FileChanges        []string `json:"file_changes,omitempty" yaml:"file_changes,omitempty"`
	Evidence           string   `json:"evidence,omitempty" yaml:"evidence,omitempty"`
	Dependencies       []string `json:"dependencies,omitempty" yaml:"dependencies,omitempty"`
	EstimatedEffort    string   `json:"estimated_effort,omitempty" yaml:"estimated_effort,omitempty"`
	Wave               int      `json:"wave,omitempty" yaml:"wave,omitempty"`
}

// SourceContext captures the source material that shaped a plan.
//
// It groups the original request, interview summary, and research findings so
// the plan body can preserve the reasoning behind the work.
type SourceContext struct {
	OriginalRequest  string `json:"original_request,omitempty" yaml:"original_request,omitempty"`
	InterviewSummary string `json:"interview_summary,omitempty" yaml:"interview_summary,omitempty"`
	ResearchFindings string `json:"research_findings,omitempty" yaml:"research_findings,omitempty"`
}

// WorkObjectives captures the desired outcome and scope for a plan.
//
// It records the core objective alongside the key deliverables, completion
// criteria, and explicit scope boundaries.
type WorkObjectives struct {
	CoreObjective    string   `json:"core_objective,omitempty" yaml:"core_objective,omitempty"`
	Deliverables     []string `json:"deliverables,omitempty" yaml:"deliverables,omitempty"`
	DefinitionOfDone []string `json:"definition_of_done,omitempty" yaml:"definition_of_done,omitempty"`
	MustHave         []string `json:"must_have,omitempty" yaml:"must_have,omitempty"`
	MustNotHave      []string `json:"must_not_have,omitempty" yaml:"must_not_have,omitempty"`
}

// ReviewResult captures the outcome of a plan review step.
//
// It records the verdict, reviewer confidence, and any blocking issues or
// suggestions that arose during assessment.
type ReviewResult struct {
	Verdict        string   `json:"verdict,omitempty" yaml:"verdict,omitempty"`
	Confidence     float64  `json:"confidence,omitempty" yaml:"confidence,omitempty"`
	BlockingIssues []string `json:"blocking_issues,omitempty" yaml:"blocking_issues,omitempty"`
	Suggestions    []string `json:"suggestions,omitempty" yaml:"suggestions,omitempty"`
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
