package plan

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	planpkg "github.com/baphled/flowstate/internal/plan"
	"github.com/baphled/flowstate/internal/tool"
)

const (
	enterName = "plan_enter"
	exitName  = "plan_exit"
	listName  = "plan_list"
	readName  = "plan_read"
	writeName = "plan_write"
)

// EnterTool signals that planning has started.
type EnterTool struct{}

// ExitTool signals that planning has finished.
type ExitTool struct{}

// NewEnter creates a new plan_enter tool instance.
//
// Returns:
//   - An EnterTool that marks plan mode entry.
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func NewEnter() *EnterTool { return &EnterTool{} }

// NewExit creates a new plan_exit tool instance.
//
// Returns:
//   - An ExitTool that marks plan mode exit.
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func NewExit() *ExitTool { return &ExitTool{} }

// Name returns the tool identifier.
//
// Returns:
//   - The string "plan_enter".
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *EnterTool) Name() string { return enterName }

// Description returns a human-readable description of the tool.
//
// Returns:
//   - A short summary of the tool's purpose.
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *EnterTool) Description() string { return "Enter plan mode" }

// Schema returns the input schema for the tool.
//
// Returns:
//   - An empty object schema.
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *EnterTool) Schema() tool.Schema {
	return tool.Schema{Type: "object", Properties: map[string]tool.Property{}, Required: []string{}}
}

// Execute marks the start of plan mode.
//
// Expected:
//   - None.
//
// Returns:
//   - A tool.Result indicating plan mode entry.
//
// Side effects:
//   - None.
func (t *EnterTool) Execute(_ context.Context, _ tool.Input) (tool.Result, error) {
	return tool.Result{Title: "Plan", Output: "entered plan mode", Metadata: map[string]interface{}{"mode": "plan", "action": "enter"}}, nil
}

// Name returns the tool identifier.
//
// Returns:
//   - The string "plan_exit".
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *ExitTool) Name() string { return exitName }

// Description returns a human-readable description of the tool.
//
// Returns:
//   - A short summary of the tool's purpose.
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *ExitTool) Description() string { return "Exit plan mode" }

// Schema returns the input schema for the tool.
//
// Returns:
//   - An empty object schema.
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *ExitTool) Schema() tool.Schema {
	return tool.Schema{Type: "object", Properties: map[string]tool.Property{}, Required: []string{}}
}

// Execute marks the end of plan mode.
//
// Expected:
//   - None.
//
// Returns:
//   - A tool.Result indicating plan mode exit.
//
// Side effects:
//   - None.
func (t *ExitTool) Execute(_ context.Context, _ tool.Input) (tool.Result, error) {
	return tool.Result{Title: "Plan", Output: "exited plan mode", Metadata: map[string]interface{}{"mode": "plan", "action": "exit"}}, nil
}

// ListTool lists available FlowState plans on disk.
//
// ListTool reads the configured plans directory, parses the YAML frontmatter
// of each *.md file, and returns a human-readable summary. It is intended to
// let harness agents answer "show me my plans" without delegating to
// filesystem-search agents that may look in the wrong path.
type ListTool struct {
	plansDir string
}

// ReadTool reads a single FlowState plan file by ID and returns its raw
// markdown contents. It complements ListTool so an agent can first enumerate
// plans and then fetch the full text of any specific one.
type ReadTool struct {
	plansDir string
}

// planFrontmatter captures the subset of the plan YAML frontmatter used for
// listing. Only Title and Status are required for the list output.
type planFrontmatter struct {
	Title  string `yaml:"title"`
	Status string `yaml:"status"`
}

// NewList creates a new plan_list tool bound to the given plans directory.
//
// Expected:
//   - plansDir is the directory containing plan markdown files. It does not
//     need to exist at construction time; missing directories are treated as
//     an empty plan set at Execute time.
//
// Returns:
//   - A *ListTool that enumerates plans from the given directory.
//
// Side effects:
//   - None; the directory is read lazily on Execute.
func NewList(plansDir string) *ListTool { return &ListTool{plansDir: plansDir} }

// NewRead creates a new plan_read tool bound to the given plans directory.
//
// Expected:
//   - plansDir is the directory containing plan markdown files.
//
// Returns:
//   - A *ReadTool that resolves plan IDs against the given directory.
//
// Side effects:
//   - None; the directory is read lazily on Execute.
func NewRead(plansDir string) *ReadTool { return &ReadTool{plansDir: plansDir} }

// Name returns the tool identifier.
//
// Returns:
//   - The string "plan_list".
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *ListTool) Name() string { return listName }

// Description returns a human-readable description of the tool.
//
// Returns:
//   - A short summary of the tool's purpose.
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *ListTool) Description() string {
	return "List available FlowState plans stored under the plans data directory. " +
		"Returns plan IDs, titles, and statuses. Takes no arguments."
}

// Schema returns the input schema for the tool.
//
// Returns:
//   - An empty object schema; plan_list takes no arguments.
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *ListTool) Schema() tool.Schema {
	return tool.Schema{Type: "object", Properties: map[string]tool.Property{}, Required: []string{}}
}

// Execute enumerates plans in the configured plans directory.
//
// Expected:
//   - ctx may be used for cancellation but is not consulted by the filesystem walk.
//
// Returns:
//   - A tool.Result whose Output is a human-readable tab-separated table of
//     plan IDs, titles, and statuses. An empty plans directory returns
//     "No plans yet." The plans directory path is included in metadata.
//   - A non-nil error only for unexpected I/O failures. A missing plans
//     directory is reported as an empty list, not as an error.
//
// Side effects:
//   - Reads the plans directory and the frontmatter section of each *.md file.
func (t *ListTool) Execute(_ context.Context, _ tool.Input) (tool.Result, error) {
	entries, err := os.ReadDir(t.plansDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return emptyPlanListResult(t.plansDir), nil
		}
		return tool.Result{}, fmt.Errorf("reading plans directory %q: %w", t.plansDir, err)
	}

	rows := collectPlanRows(entries, t.plansDir)
	if len(rows) == 0 {
		return emptyPlanListResult(t.plansDir), nil
	}

	return tool.Result{
		Title:  "Plans",
		Output: formatPlanList(rows, t.plansDir),
		Metadata: map[string]interface{}{
			"plans_dir": t.plansDir,
			"count":     len(rows),
		},
	}, nil
}

// planRow is the per-plan record assembled by collectPlanRows.
type planRow struct {
	id     string
	title  string
	status string
}

// emptyPlanListResult returns the tool.Result used when the plans directory
// is missing or contains no plan markdown files.
//
// Expected:
//   - plansDir is the directory that was inspected (may not exist).
//
// Returns:
//   - A tool.Result with a human-readable "No plans yet." message and
//     zero-count metadata.
//
// Side effects:
//   - None.
func emptyPlanListResult(plansDir string) tool.Result {
	return tool.Result{
		Title:  "Plans",
		Output: "No plans yet.",
		Metadata: map[string]interface{}{
			"plans_dir": plansDir,
			"count":     0,
		},
	}
}

// collectPlanRows scans directory entries for *.md plan files and returns
// them sorted by ID with titles derived from frontmatter (falling back to
// the ID when the title is missing).
//
// Expected:
//   - entries is the result of os.ReadDir on plansDir.
//   - plansDir is the directory containing the entries.
//
// Returns:
//   - A slice of planRow values sorted by id.
//
// Side effects:
//   - Reads each candidate plan file to parse its frontmatter.
func collectPlanRows(entries []os.DirEntry, plansDir string) []planRow {
	var rows []planRow
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".md")
		title, status := readPlanSummary(filepath.Join(plansDir, entry.Name()))
		if title == "" {
			title = id
		}
		rows = append(rows, planRow{id: id, title: title, status: status})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].id < rows[j].id })
	return rows
}

// formatPlanList renders a human-readable tab-separated table of plan rows,
// prefixed with a header that names the plans directory.
//
// Expected:
//   - rows is non-empty.
//   - plansDir is the directory whose plans the rows describe.
//
// Returns:
//   - The rendered table as a string.
//
// Side effects:
//   - None.
func formatPlanList(rows []planRow, plansDir string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Found %d plan(s) under %s:\n\n", len(rows), plansDir)
	b.WriteString("ID\tTitle\tStatus\n")
	for _, r := range rows {
		status := r.status
		if status == "" {
			status = "unknown"
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\n", r.id, r.title, status)
	}
	return b.String()
}

// Name returns the tool identifier.
//
// Returns:
//   - The string "plan_read".
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *ReadTool) Name() string { return readName }

// Description returns a human-readable description of the tool.
//
// Returns:
//   - A short summary of the tool's purpose.
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *ReadTool) Description() string {
	return "Read a single FlowState plan by ID (filename without .md extension) " +
		"and return its full markdown contents."
}

// Schema returns the input schema for the tool.
//
// Returns:
//   - An object schema with a required string property "id".
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *ReadTool) Schema() tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"id": {
				Type:        "string",
				Description: "Plan identifier (filename without .md extension, e.g. 'ruby-grape-bookstore-api').",
			},
		},
		Required: []string{"id"},
	}
}

// Execute reads the plan file identified by the "id" argument and returns its
// raw markdown contents.
//
// Expected:
//   - input.Arguments contains a non-empty "id" string.
//
// Returns:
//   - A tool.Result whose Output is the full markdown of the plan file.
//   - A non-nil error when "id" is missing or when the plan cannot be located.
//     The error message includes the requested ID and the plans directory so
//     the caller can identify mismatched paths.
//
// Side effects:
//   - Reads the plan file from disk.
func (t *ReadTool) Execute(_ context.Context, input tool.Input) (tool.Result, error) {
	rawID, ok := input.Arguments["id"]
	if !ok {
		return tool.Result{}, fmt.Errorf("plan_read: missing required argument %q (plans dir: %s)", "id", t.plansDir)
	}
	id, ok := rawID.(string)
	if !ok || strings.TrimSpace(id) == "" {
		return tool.Result{}, fmt.Errorf("plan_read: argument %q must be a non-empty string (plans dir: %s)", "id", t.plansDir)
	}

	// Guard against path traversal: we operate only on filenames directly
	// inside plansDir. Any path separator is a hard error.
	if strings.ContainsAny(id, "/\\") || id == "." || id == ".." {
		return tool.Result{}, fmt.Errorf("plan_read: invalid plan id %q (plans dir: %s)", id, t.plansDir)
	}

	filePath := filepath.Join(t.plansDir, id+".md")
	data, err := os.ReadFile(filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return tool.Result{}, fmt.Errorf("plan_read: plan %q not found in %s", id, t.plansDir)
		}
		return tool.Result{}, fmt.Errorf("plan_read: reading plan %q from %s: %w", id, t.plansDir, err)
	}

	return tool.Result{
		Title:  "Plan: " + id,
		Output: string(data),
		Metadata: map[string]interface{}{
			"plans_dir": t.plansDir,
			"plan_id":   id,
			"path":      filePath,
		},
	}, nil
}

// WriteTool persists a plan to the plans directory as a markdown file.
//
// Closes the regression where plan-writer agents were storing plans only in
// coordination_store (an in-process key-value store keyed on chain id) and
// never landing them on disk. With plan_write, the agent's final step is
// `plan_write(markdown=...)` which parses the YAML frontmatter, populates a
// plan.File, and calls plan.Store.Create — landing the plan at
// {plansDir}/{id}.md so the existing plan_list / plan_read tools and the
// `flowstate plan` CLI can find it later.
type WriteTool struct {
	plansDir string
}

// NewWrite creates a new plan_write tool bound to the given plans directory.
//
// Expected:
//   - plansDir is the directory where FlowState plan markdown files live.
//
// Returns:
//   - A *WriteTool that persists plans into the given directory.
//
// Side effects:
//   - None at construction; the directory is opened on Execute.
func NewWrite(plansDir string) *WriteTool { return &WriteTool{plansDir: plansDir} }

// Name returns the tool identifier.
//
// Returns:
//   - The string "plan_write".
//
// Side effects:
//   - None.
func (t *WriteTool) Name() string { return writeName }

// Description returns a human-readable description of the tool.
func (t *WriteTool) Description() string {
	return "Persist a FlowState plan to the plans data directory as a " +
		"markdown file. The input must be the full plan text including " +
		"YAML frontmatter (--- ... ---) with at least an `id` and `title`. " +
		"Returns the on-disk path so the plan can be referenced later via " +
		"plan_list / plan_read or `flowstate plan list`."
}

// Schema returns the input schema for the tool.
//
// Returns:
//   - An object schema with a required string property "markdown" carrying
//     the full plan text including YAML frontmatter.
func (t *WriteTool) Schema() tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"markdown": {
				Type: "string",
				Description: "Full plan text including YAML frontmatter " +
					"(`---\\nid: ...\\ntitle: ...\\n---\\n# ...`). " +
					"The frontmatter's `id` becomes the filename.",
			},
		},
		Required: []string{"markdown"},
	}
}

// Execute parses the supplied markdown into a plan.File and persists it via
// plan.Store. Returns the on-disk path on success.
//
// Expected:
//   - input.Arguments contains a non-empty "markdown" string with valid
//     YAML frontmatter at the top (--- ... ---) carrying at least `id`.
//
// Returns:
//   - A tool.Result describing what was written and where.
//   - A non-nil error when the input is missing/empty, when frontmatter
//     cannot be parsed, when `id` is missing or unsafe, or when the
//     filesystem write fails.
//
// Side effects:
//   - Writes {plansDir}/{id}.md. Existing files with the same id are
//     silently overwritten by plan.Store.Create.
func (t *WriteTool) Execute(_ context.Context, input tool.Input) (tool.Result, error) {
	rawMD, ok := input.Arguments["markdown"]
	if !ok {
		return tool.Result{}, fmt.Errorf("plan_write: missing required argument %q (plans dir: %s)", "markdown", t.plansDir)
	}
	md, ok := rawMD.(string)
	if !ok || strings.TrimSpace(md) == "" {
		return tool.Result{}, fmt.Errorf("plan_write: argument %q must be a non-empty string (plans dir: %s)", "markdown", t.plansDir)
	}

	parsed, err := planpkg.ParseFile(md)
	if err != nil {
		return tool.Result{}, fmt.Errorf("plan_write: parsing plan markdown: %w", err)
	}
	if parsed == nil || strings.TrimSpace(parsed.ID) == "" {
		return tool.Result{}, fmt.Errorf("plan_write: plan frontmatter must include a non-empty `id` field")
	}
	id := parsed.ID
	if strings.ContainsAny(id, "/\\") || id == "." || id == ".." {
		return tool.Result{}, fmt.Errorf("plan_write: invalid plan id %q (no path separators, no relative paths)", id)
	}

	parsed.Tasks = planpkg.TasksFromPlanText(md)
	if parsed.CreatedAt.IsZero() {
		parsed.CreatedAt = time.Now().UTC()
	}

	store, err := planpkg.NewStore(t.plansDir)
	if err != nil {
		return tool.Result{}, fmt.Errorf("plan_write: opening plan store at %s: %w", t.plansDir, err)
	}
	if err := store.Create(*parsed); err != nil {
		return tool.Result{}, fmt.Errorf("plan_write: persisting plan %q: %w", id, err)
	}

	filePath := filepath.Join(t.plansDir, id+".md")
	return tool.Result{
		Title: "Plan: " + id,
		Output: fmt.Sprintf("Plan %q saved to %s (%d task(s)).",
			id, filePath, len(parsed.Tasks)),
		Metadata: map[string]interface{}{
			"plans_dir":  t.plansDir,
			"plan_id":    id,
			"path":       filePath,
			"task_count": len(parsed.Tasks),
		},
	}, nil
}

// readPlanSummary extracts the title and status from a plan file's YAML
// frontmatter. Returns empty strings for either field when parsing fails or
// the frontmatter is missing; callers should fall back to the plan ID.
//
// Expected:
//   - filePath points to a readable file (the caller has already decided
//     the entry is a plan).
//
// Returns:
//   - Title and status strings, or empty strings when the frontmatter cannot
//     be parsed.
//
// Side effects:
//   - Reads the file from disk.
func readPlanSummary(filePath string) (string, string) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", ""
	}
	parts := strings.SplitN(string(data), "---", 3)
	if len(parts) < 3 {
		return "", ""
	}
	var fm planFrontmatter
	if err := yaml.Unmarshal([]byte(parts[1]), &fm); err != nil {
		return "", ""
	}
	return fm.Title, fm.Status
}

var _ = errors.New
